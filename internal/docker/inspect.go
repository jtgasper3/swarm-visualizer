package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal"
	"github.com/jtgasper3/swarm-visualizer/internal/config"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

type SwarmData struct {
	ClusterName string `json:"clusterName"`
	AuthEnabled bool   `json:"authEnabled"`
	// Stale is set while the swarm cannot be read, and LastUpdated then says
	// how old the rest of the snapshot is. A snapshot is only sent when it
	// changes, so without these a view of a swarm the server has lost touch
	// with looks the same as a view of one where nothing is happening.
	Stale       bool              `json:"stale,omitempty"`
	LastUpdated *time.Time        `json:"lastUpdated,omitempty"`
	Networks    []network.Summary `json:"networks"`
	Nodes       []swarm.Node      `json:"nodes"`
	Services    []swarm.Service   `json:"services"`
	Tasks       []swarm.Task      `json:"tasks"`
}

// validateSensitiveDataPaths checks every configured redaction path against
// the shape of SwarmData, so that a mistyped path stops the server at startup
// instead of leaving the data it was meant to remove in every snapshot.
func validateSensitiveDataPaths(paths []string) error {
	var errs []error
	for _, path := range paths {
		if err := internal.ValidatePath(reflect.TypeFor[SwarmData](), path); err != nil {
			errs = append(errs, fmt.Errorf("invalid SENSITIVE_DATA_PATHS entry %q: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// cachedTask is a stopped task and when the inspector first saw it stopped.
type cachedTask struct {
	task      swarm.Task
	firstSeen time.Time
}

// swarmSource is the subset of the Docker API the inspector needs. It is an
// interface so tests can substitute a fake daemon for the real client.
type swarmSource interface {
	Nodes(ctx context.Context) ([]swarm.Node, error)
	Services(ctx context.Context) ([]swarm.Service, error)
	Tasks(ctx context.Context) ([]swarm.Task, error)
	Networks(ctx context.Context) ([]network.Summary, error)
}

// mobySource adapts the real Docker client to swarmSource.
type mobySource struct{ cli *client.Client }

// newMobySource creates a swarmSource backed by a Docker client configured from
// the environment.
func newMobySource() (swarmSource, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return mobySource{cli: cli}, nil
}

func (m mobySource) Nodes(ctx context.Context) ([]swarm.Node, error) {
	res, err := m.cli.NodeList(ctx, client.NodeListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (m mobySource) Services(ctx context.Context) ([]swarm.Service, error) {
	res, err := m.cli.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (m mobySource) Tasks(ctx context.Context) ([]swarm.Task, error) {
	res, err := m.cli.TaskList(ctx, client.TaskListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (m mobySource) Networks(ctx context.Context) ([]network.Summary, error) {
	res, err := m.cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("scope", "swarm")})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

const (
	// taskPollInterval is how often task state is refreshed. Tasks change the
	// most and are not observable via the Docker events API (there is no task
	// event type), so they are polled at this cadence.
	taskPollInterval = 1 * time.Second
	// structuralPollInterval is how often the slower-changing nodes, services,
	// and networks are refreshed.
	structuralPollInterval = 5 * time.Second
	// idleTaskPollInterval replaces taskPollInterval while nobody is connected:
	// there is no one to keep the tasks fresh to the second for. A client that
	// connects is polled for at once.
	idleTaskPollInterval = structuralPollInterval
	// dockerCallTimeout bounds one poll of the Docker API: the task list, or
	// the nodes, services and networks together. Without a bound, a daemon or
	// socket proxy that accepts the connection and then says nothing would
	// hold the poll loop for good.
	dockerCallTimeout = 10 * time.Second
	// staleAfter is how long the swarm can go unread before a snapshot says
	// so. It has to be longer than every poll interval above. The snapshot is
	// reassessed between polls, so when calls hang rather than fail it is said
	// up to two timeouts later.
	staleAfter = 15 * time.Second
	// pollErrorLogInterval is how often a failure that keeps repeating is
	// logged again after its first report.
	pollErrorLogInterval = 30 * time.Second
)

// pollErrors keeps a failure that repeats on every poll from filling the log.
// The first failure is logged, then one reminder per pollErrorLogInterval, then
// the recovery.
type pollErrors struct {
	// what was being fetched, for the log line.
	what string
	// logf is log.Printf outside tests.
	logf func(format string, args ...any)

	// since is when the current run of failures began; zero while healthy.
	since   time.Time
	lastLog time.Time
	skipped int
}

func (p *pollErrors) failed(now time.Time, err error) {
	// Several calls can fail in one poll; keep their errors on one log line.
	reason := strings.ReplaceAll(err.Error(), "\n", "; ")

	switch {
	case p.since.IsZero():
		p.since, p.lastLog, p.skipped = now, now, 0
		p.logf("Error fetching %s: %s", p.what, reason)
	case now.Sub(p.lastLog) < pollErrorLogInterval:
		p.skipped++
	default:
		p.logf("Still failing to fetch %s after %s (%d errors not logged): %s",
			p.what, now.Sub(p.since).Round(time.Second), p.skipped, reason)
		p.lastLog, p.skipped = now, 0
	}
}

func (p *pollErrors) succeeded(now time.Time) {
	if p.since.IsZero() {
		return
	}
	p.logf("Fetching %s works again after %s", p.what, now.Sub(p.since).Round(time.Second))
	p.since = time.Time{}
}

// inspector polls the swarm and publishes a snapshot whenever it changes. Tasks
// are polled frequently for freshness; nodes, services, and networks change
// rarely, so they are polled on a slower cadence. The most recently fetched
// value for each group is cached between ticks and reassembled on every
// publish. All of its state belongs to the single goroutine that calls run.
//
// Each group is sanitized once, when it is fetched, and the cached slices are
// shared with the previously published snapshot. Anything that later changes
// them in place therefore has to drop that snapshot, or the change would not
// be seen as one.
type inspector struct {
	cfg *config.Config
	src swarmSource
	hub *Hub
	// now is the clock; tests replace it.
	now func() time.Time
	// callTimeout is dockerCallTimeout outside tests.
	callTimeout time.Duration

	nodes    []swarm.Node
	services []swarm.Service
	networks []network.Summary
	tasks    []swarm.Task

	haveStructural bool
	haveTasks      bool

	// lastStructural and lastTasks are when each group was last read
	// successfully. lastTaskPoll is when the tasks were last asked for.
	lastStructural time.Time
	lastTasks      time.Time
	lastTaskPoll   time.Time

	structuralErrors pollErrors
	taskErrors       pollErrors

	// hiddenEnvs is each service's hide-envs list as of the last structural
	// refresh, kept so it can be applied to the tasks' own copies of the env.
	hiddenEnvs hiddenEnvs
	// seenTasks holds the ID of every task the previous task poll returned.
	seenTasks map[string]struct{}
	// stoppedTasks holds the tasks seen to have failed or completed within
	// the last failedTaskGracePeriod, by ID. The swarm goes on listing a
	// stopped task for much longer than that, so what is shown is kept here.
	stoppedTasks map[string]cachedTask

	// The configured redaction paths, by the group they reach into, so that
	// each is applied when its group is fetched and not on every publish.
	// otherPaths are the few that address the snapshot itself.
	structuralPaths []string
	taskPaths       []string
	otherPaths      []string

	// lastPublished is the previous snapshot, kept for change detection.
	lastPublished *SwarmData
}

func newInspector(cfg *config.Config, src swarmSource, hub *Hub) *inspector {
	in := &inspector{
		cfg:              cfg,
		src:              src,
		hub:              hub,
		now:              time.Now,
		callTimeout:      dockerCallTimeout,
		stoppedTasks:     make(map[string]cachedTask),
		structuralErrors: pollErrors{what: "nodes, services and networks", logf: log.Printf},
		taskErrors:       pollErrors{what: "tasks", logf: log.Printf},
	}

	for _, path := range cfg.SensitiveDataPaths {
		group, _, _ := strings.Cut(path, ".")
		switch {
		case strings.EqualFold(group, "nodes"), strings.EqualFold(group, "services"), strings.EqualFold(group, "networks"):
			in.structuralPaths = append(in.structuralPaths, path)
		case strings.EqualFold(group, "tasks"):
			in.taskPaths = append(in.taskPaths, path)
		default:
			in.otherPaths = append(in.otherPaths, path)
		}
	}
	return in
}

// clearPaths applies redaction paths to data. A path that reaches into a group
// clears the caller's values through the slice, but one that names a whole
// group empties the field in data, so callers take the groups back from data
// afterwards.
func clearPaths(data *SwarmData, paths []string) {
	for _, path := range paths {
		if err := internal.ClearByPath(data, path); err != nil {
			log.Println("Error clearing sensitive data:", err, path)
		}
	}
}

func (in *inspector) refreshStructural(ctx context.Context) bool {
	// One deadline for the three calls, so a daemon that has stopped answering
	// costs a single timeout and not three in a row.
	callCtx, cancel := context.WithTimeout(ctx, in.callTimeout)
	defer cancel()

	n, errN := getNodesInfo(callCtx, in.src, in.cfg)
	s, hidden, errS := getServicesInfo(callCtx, in.src, in.cfg)
	nw, errNw := getNetworksInfo(callCtx, in.src, in.cfg)
	if err := errors.Join(errN, errS, errNw); err != nil {
		in.structuralErrors.failed(in.now(), err)
		return false
	}
	in.structuralErrors.succeeded(in.now())

	fetched := SwarmData{Nodes: n, Services: s, Networks: nw}
	clearPaths(&fetched, in.structuralPaths)
	in.nodes, in.services, in.networks = fetched.Nodes, fetched.Services, fetched.Networks
	if !reflect.DeepEqual(hidden, in.hiddenEnvs) {
		// The cached tasks were sanitized under the previous lists, and a label
		// change creates no task to prompt a task poll. Apply the new lists
		// here, or a variable just added to hide-envs would stay in every
		// snapshot until a task poll next succeeds. The tasks share their
		// storage with the last snapshot, which therefore cannot show that
		// they changed, so forget it and let the next publish go out.
		in.tasks = sanitizeTasks(in.tasks, in.cfg, hidden)
		in.lastPublished = nil
	}
	in.hiddenEnvs = hidden
	in.haveStructural = true
	in.lastStructural = in.now()
	return true
}

func (in *inspector) refreshTasks(ctx context.Context) bool {
	in.lastTaskPoll = in.now()

	callCtx, cancel := context.WithTimeout(ctx, in.callTimeout)
	all, err := in.src.Tasks(callCtx)
	cancel()
	if err != nil {
		in.taskErrors.failed(in.now(), fmt.Errorf("tasks: %w", err))
		return false
	}
	in.taskErrors.succeeded(in.now())

	// A task's env is redacted using its service's hide-envs label, which is
	// only re-read on the structural cadence. A task that was not there on the
	// previous poll may come from a newer spec than the cached one — a new
	// service, or an update that added a variable along with its hide-envs
	// entry — so re-read the services before sanitizing it. If that fails, the
	// new task waits for a later poll rather than going out under old rules.
	if in.hasUnseenTask(all) && !in.refreshStructural(ctx) {
		return false
	}
	in.seenTasks = make(map[string]struct{}, len(all))
	for _, t := range all {
		in.seenTasks[t.ID] = struct{}{}
	}

	fetched := SwarmData{Tasks: sanitizeTasks(in.selectTasks(all), in.cfg, in.hiddenEnvs)}
	clearPaths(&fetched, in.taskPaths)
	in.tasks = fetched.Tasks
	in.haveTasks = true
	in.lastTasks = in.now()
	return true
}

// hasUnseenTask reports whether tasks contains one the previous poll did not.
func (in *inspector) hasUnseenTask(tasks []swarm.Task) bool {
	for _, t := range tasks {
		if _, seen := in.seenTasks[t.ID]; !seen {
			return true
		}
	}
	return false
}

// publish sends the snapshot if it differs from the last one sent. It runs
// after every poll, failed ones included: a snapshot turning stale is a change
// like any other, and a failed poll is the only thing that brings it about.
func (in *inspector) publish() {
	// Don't publish a partial view before every group has loaded once.
	if !in.haveStructural || !in.haveTasks {
		return
	}

	data := SwarmData{
		ClusterName: in.cfg.ClusterName,
		AuthEnabled: in.cfg.AuthEnabled,
		Services:    in.services,
		Nodes:       in.nodes,
		Networks:    in.networks,
		Tasks:       in.tasks,
	}

	// The time stays put for as long as the reads keep failing, so a stale
	// snapshot is sent once and not on every poll.
	oldest := in.lastTasks
	if in.lastStructural.Before(oldest) {
		oldest = in.lastStructural
	}
	if in.now().Sub(oldest) > staleAfter {
		oldest = oldest.UTC().Truncate(time.Second)
		data.Stale = true
		data.LastUpdated = &oldest
	}

	clearPaths(&data, in.otherPaths)

	if in.lastPublished == nil || !reflect.DeepEqual(data, *in.lastPublished) {
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			log.Println("Error marshalling combined data:", err)
			return
		}
		in.lastPublished = &data
		in.hub.Publish(jsonBytes)
	}
}

// load performs the initial fetch, so clients connecting at startup get a full
// snapshot promptly rather than waiting for the first structural tick. Every
// task is unseen on the first poll, so refreshTasks loads the structural data
// itself; the explicit call covers a swarm that has no tasks at all.
func (in *inspector) load(ctx context.Context) {
	in.refreshTasks(ctx)
	if !in.haveStructural {
		in.refreshStructural(ctx)
	}
	in.publish()
}

// tasksDue reports whether a task tick should poll. With nobody connected the
// tasks are read at the slower idle interval.
func (in *inspector) tasksDue() bool {
	return !in.hub.Idle() || in.now().Sub(in.lastTaskPoll) >= idleTaskPollInterval
}

// taskTick is the loop's response to the task ticker.
func (in *inspector) taskTick(ctx context.Context) {
	if in.tasksDue() {
		in.pollTasks(ctx)
	}
}

// pollTasks reads the tasks and publishes. It publishes whether or not the
// read worked, because a failed read is how a snapshot comes to be stale.
func (in *inspector) pollTasks(ctx context.Context) {
	in.refreshTasks(ctx)
	in.publish()
}

// pollStructural does the same for the nodes, services and networks.
func (in *inspector) pollStructural(ctx context.Context) {
	in.refreshStructural(ctx)
	in.publish()
}

// run polls until ctx ends.
func (in *inspector) run(ctx context.Context) {
	runStep(func() { in.load(ctx) })

	taskTicker := time.NewTicker(taskPollInterval)
	defer taskTicker.Stop()
	structuralTicker := time.NewTicker(structuralPollInterval)
	defer structuralTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-taskTicker.C:
			runStep(func() { in.taskTick(ctx) })
		case <-in.hub.arrivals:
			// Someone connected while the tasks were being read slowly. They
			// were given the last snapshot; bring it up to date for them now.
			runStep(func() { in.pollTasks(ctx) })
		case <-structuralTicker.C:
			runStep(func() { in.pollStructural(ctx) })
		}
	}
}

// runStep runs one step of the poll loop and contains a panic to that step.
// Snapshots are built from whatever the Docker API returns, and one value in an
// unexpected shape must not take the whole server down with it: the step is
// abandoned, the last good snapshot stays published, and the next tick retries.
func runStep(step func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic while polling the swarm: %v\n%s", r, debug.Stack())
		}
	}()
	step()
}

func getNetworksInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]network.Summary, error) {
	networks, err := src.Networks(ctx)
	if err != nil {
		return nil, fmt.Errorf("networks: %w", err)
	}

	filteredNetworks := make([]network.Summary, 0, len(networks))
	for _, net := range networks {
		if hidesLabels(cfg, "network") {
			net.Labels = nil
		}

		// Remove the Ingress network, if present
		if net.Name != "ingress" {
			filteredNetworks = append(filteredNetworks, net)
		}
	}

	return filteredNetworks, nil
}

func getNodesInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]swarm.Node, error) {
	nodes, err := src.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}

	return sanitizeNodes(nodes, cfg), nil
}

// getServicesInfo returns the sanitized services along with each one's
// hide-envs list, which the caller needs for sanitizing tasks.
func getServicesInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]swarm.Service, hiddenEnvs, error) {
	services, err := src.Services(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("services: %w", err)
	}

	// Read the lists before sanitizing: HIDE_LABELS can remove the labels
	// that hold them.
	hidden := make(hiddenEnvs, len(services))
	for i := range services {
		hidden[services[i].ID] = hiddenEnvNames(&services[i])
	}

	return sanitizeServices(services, cfg), hidden, nil
}

const failedTaskGracePeriod = 30 * time.Second

// selectTasks picks the tasks worth showing out of everything the swarm
// reports: those meant to be running, plus the most recently stopped task per
// slot for a short grace period.
func (in *inspector) selectTasks(tasks []swarm.Task) []swarm.Task {
	now := in.now()

	// Single pass: update the stopped-task cache and collect running/accepted tasks.
	var result []swarm.Task
	resultIDs := make(map[string]struct{})
	for _, t := range tasks {
		if t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateComplete {
			if entry, exists := in.stoppedTasks[t.ID]; exists {
				in.stoppedTasks[t.ID] = cachedTask{task: t, firstSeen: entry.firstSeen}
			} else if now.Sub(t.UpdatedAt) < failedTaskGracePeriod {
				// Only cache tasks that stopped recently; skip historical tasks.
				in.stoppedTasks[t.ID] = cachedTask{task: t, firstSeen: now}
			}
		}
		if t.DesiredState == swarm.TaskStateRunning || t.DesiredState == swarm.TaskStateAccepted {
			result = append(result, t)
			resultIDs[t.ID] = struct{}{}
		}
	}

	// Evict expired cache entries.
	for id, entry := range in.stoppedTasks {
		if now.Sub(entry.firstSeen) >= failedTaskGracePeriod {
			delete(in.stoppedTasks, id)
		}
	}

	// Append the most recently stopped task per slot from the cache.
	// For replicated services the key is "serviceID:slot"; for global
	// services (slot == 0) it is "serviceID:nodeID". Only the newest entry
	// per slot is kept so we don't flood the view with historical tasks.
	newestStopped := make(map[string]swarm.Task)
	for _, entry := range in.stoppedTasks {
		t := entry.task
		if _, inResult := resultIDs[t.ID]; inResult {
			continue
		}
		var key string
		if t.Slot > 0 {
			key = fmt.Sprintf("%s:%d", t.ServiceID, t.Slot)
		} else {
			key = fmt.Sprintf("%s:%s", t.ServiceID, t.NodeID)
		}
		if existing, ok := newestStopped[key]; !ok || t.CreatedAt.After(existing.CreatedAt) {
			newestStopped[key] = t
		}
	}

	// Map iteration order is random. Without a fixed order an unchanged swarm
	// would differ from the last snapshot and be republished on every poll.
	stopped := slices.SortedFunc(maps.Values(newestStopped), func(a, b swarm.Task) int {
		return strings.Compare(a.ID, b.ID)
	})

	return append(result, stopped...)
}

const (
	// hideEnvsLabel is a service label listing the environment variables whose
	// values are replaced with sanitizedValue.
	hideEnvsLabel = "io.github.jtgasper3.visualizer.hide-envs"
	// hideLabelsLabel is a service or container label listing the labels, in
	// the label set it appears in, whose values are replaced with
	// sanitizedValue.
	hideLabelsLabel = "io.github.jtgasper3.visualizer.hide-labels"

	sanitizedValue = "(sanitized)"
)

// hiddenEnvs maps a service ID to the environment variable names its hide-envs
// label redacts. Every known service has an entry, even if it is empty, so a
// missing entry means the service itself is unknown.
type hiddenEnvs map[string]map[string]struct{}

// hiddenEnvNames returns the variable names that a service's hide-envs label
// redacts. The previous spec's label counts as well: when an update removes a
// variable together with its hide-envs entry, tasks from the old spec keep
// running with the value until the rollout replaces them.
func hiddenEnvNames(svc *swarm.Service) map[string]struct{} {
	names := make(map[string]struct{})
	for _, spec := range []*swarm.ServiceSpec{&svc.Spec, svc.PreviousSpec} {
		if spec == nil {
			continue
		}
		for _, name := range splitLabelList(spec.Labels[hideEnvsLabel]) {
			names[name] = struct{}{}
		}
	}
	return names
}

// splitLabelList parses the comma-separated value of a hide-envs or
// hide-labels label.
func splitLabelList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// hidesLabels reports whether HIDE_LABELS covers the given category.
func hidesLabels(cfg *config.Config, category string) bool {
	return slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, category)
}

// sanitizeNodes removes or redacts fields on nodes according to the configuration.
func sanitizeNodes(nodes []swarm.Node, cfg *config.Config) []swarm.Node {
	for i := range nodes {
		if hidesLabels(cfg, "node") {
			nodes[i].Spec.Labels = nil
		}
	}
	return nodes
}

// sanitizeServices removes or redacts fields on services according to the configuration.
func sanitizeServices(services []swarm.Service, cfg *config.Config) []swarm.Service {
	for i := range services {
		svc := &services[i]

		// Read the hide-envs list before any labels are removed below.
		hidden := hiddenEnvNames(svc)

		// PreviousSpec is the spec from before the last update, which swarm
		// keeps for rollbacks. It holds a second copy of everything redacted
		// here and nothing in the UI reads it, so it is dropped rather than
		// sanitized in parallel.
		svc.PreviousSpec = nil

		sanitizeContainerSpec(svc.Spec.TaskTemplate.ContainerSpec, cfg, func(name string) bool {
			_, hide := hidden[name]
			return hide
		})

		if hidesLabels(cfg, "service") {
			svc.Spec.Labels = nil
		} else {
			redactLabels(svc.Spec.Labels)
		}
	}
	return services
}

// sanitizeTasks removes or redacts fields on tasks according to the
// configuration. hidden supplies the hide-envs list of each task's service,
// because a task carries its own copy of the service's environment.
func sanitizeTasks(tasks []swarm.Task, cfg *config.Config, hidden hiddenEnvs) []swarm.Task {
	for i := range tasks {
		t := &tasks[i]

		// With no entry the service is unknown and there is no list to
		// consult, so every value is hidden rather than none.
		names, known := hidden[t.ServiceID]
		sanitizeContainerSpec(t.Spec.ContainerSpec, cfg, func(name string) bool {
			_, hide := names[name]
			return hide || !known
		})
	}
	return tasks
}

// sanitizeContainerSpec applies the HIDE_* settings, the hide-envs list (as
// hideEnv), and the container's own hide-labels label to one container spec.
// cs is nil for a service or task that does not run a container, such as one
// using the plugin runtime.
func sanitizeContainerSpec(cs *swarm.ContainerSpec, cfg *config.Config, hideEnv func(name string) bool) {
	if cs == nil {
		return
	}

	if cfg.HideAllConfigs {
		cs.Configs = nil
	}
	if cfg.HideAllEnvs {
		cs.Env = nil
	}
	if cfg.HideAllMounts {
		cs.Mounts = nil
	}
	if cfg.HideAllSecrets {
		cs.Secrets = nil
	}

	for i, env := range cs.Env {
		if name, _, _ := strings.Cut(env, "="); hideEnv(name) {
			cs.Env[i] = name + "=" + sanitizedValue
		}
	}

	if hidesLabels(cfg, "container") {
		cs.Labels = nil
	} else {
		redactLabels(cs.Labels)
	}
}

// redactLabels replaces the value of each label that the set's own hide-labels
// label names.
func redactLabels(labels map[string]string) {
	for _, name := range splitLabelList(labels[hideLabelsLabel]) {
		if _, ok := labels[name]; ok {
			labels[name] = sanitizedValue
		}
	}
}
