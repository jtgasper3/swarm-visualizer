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
	ClusterName string            `json:"clusterName"`
	AuthEnabled bool              `json:"authEnabled"`
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

type cachedTask struct {
	task      swarm.Task
	firstSeen time.Time
}

// stoppedTaskCache is only accessed from the inspector's single goroutine.
var stoppedTaskCache = make(map[string]cachedTask)

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
)

// inspector polls the swarm and publishes a snapshot whenever it changes. Tasks
// are polled frequently for freshness; nodes, services, and networks change
// rarely, so they are polled on a slower cadence. The most recently fetched
// value for each group is cached between ticks and reassembled on every
// publish. All of its state belongs to the single goroutine that calls run.
//
// Reassembly applies SensitiveDataPaths via ClearByPath, which only zeroes
// fields and is therefore idempotent; re-applying it to a cached (already
// cleared) slice that is also referenced by the previously published snapshot
// leaves that snapshot's bytes unchanged, so change detection stays correct.
type inspector struct {
	cfg *config.Config
	src swarmSource
	hub *Hub

	nodes    []swarm.Node
	services []swarm.Service
	networks []network.Summary
	tasks    []swarm.Task

	haveStructural bool
	haveTasks      bool

	// hiddenEnvs is each service's hide-envs list as of the last structural
	// refresh, kept so it can be applied to the tasks' own copies of the env.
	hiddenEnvs hiddenEnvs
	// seenTasks holds the ID of every task the previous task poll returned.
	seenTasks map[string]struct{}

	// lastPublished is the previous snapshot, kept for change detection.
	lastPublished *SwarmData
}

func newInspector(cfg *config.Config, src swarmSource, hub *Hub) *inspector {
	return &inspector{cfg: cfg, src: src, hub: hub}
}

func (in *inspector) refreshStructural(ctx context.Context) bool {
	n, errN := getNodesInfo(ctx, in.src, in.cfg)
	s, hidden, errS := getServicesInfo(ctx, in.src, in.cfg)
	nw, errNw := getNetworksInfo(ctx, in.src, in.cfg)
	if errN != nil || errS != nil || errNw != nil {
		return false
	}
	in.nodes, in.services, in.networks = n, s, nw
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
	return true
}

func (in *inspector) refreshTasks(ctx context.Context) bool {
	all, err := in.src.Tasks(ctx)
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return false
	}

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

	in.tasks = sanitizeTasks(selectTasks(all), in.cfg, in.hiddenEnvs)
	in.haveTasks = true
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

	for _, item := range in.cfg.SensitiveDataPaths {
		if clearErr := internal.ClearByPath(&data, item); clearErr != nil {
			log.Println("Error clearing sensitive data:", clearErr, item)
		}
	}

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

func (in *inspector) run() {
	ctx := context.Background()

	runStep(func() { in.load(ctx) })

	taskTicker := time.NewTicker(taskPollInterval)
	defer taskTicker.Stop()
	structuralTicker := time.NewTicker(structuralPollInterval)
	defer structuralTicker.Stop()

	for {
		select {
		case <-taskTicker.C:
			runStep(func() {
				if in.refreshTasks(ctx) {
					in.publish()
				}
			})
		case <-structuralTicker.C:
			runStep(func() {
				if in.refreshStructural(ctx) {
					in.publish()
				}
			})
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
		log.Printf("Error fetching networks: %v", err)
		return nil, err
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
		log.Printf("Error fetching nodes: %v", err)
		return nil, err
	}

	return sanitizeNodes(nodes, cfg), nil
}

// getServicesInfo returns the sanitized services along with each one's
// hide-envs list, which the caller needs for sanitizing tasks.
func getServicesInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]swarm.Service, hiddenEnvs, error) {
	services, err := src.Services(ctx)
	if err != nil {
		log.Printf("Error fetching services: %v", err)
		return nil, nil, err
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
func selectTasks(tasks []swarm.Task) []swarm.Task {
	now := time.Now()

	// Single pass: update the stopped-task cache and collect running/accepted tasks.
	var result []swarm.Task
	resultIDs := make(map[string]struct{})
	for _, t := range tasks {
		if t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateComplete {
			if entry, exists := stoppedTaskCache[t.ID]; exists {
				stoppedTaskCache[t.ID] = cachedTask{task: t, firstSeen: entry.firstSeen}
			} else if now.Sub(t.UpdatedAt) < failedTaskGracePeriod {
				// Only cache tasks that stopped recently; skip historical tasks.
				stoppedTaskCache[t.ID] = cachedTask{task: t, firstSeen: now}
			}
		}
		if t.DesiredState == swarm.TaskStateRunning || t.DesiredState == swarm.TaskStateAccepted {
			result = append(result, t)
			resultIDs[t.ID] = struct{}{}
		}
	}

	// Evict expired cache entries.
	for id, entry := range stoppedTaskCache {
		if now.Sub(entry.firstSeen) >= failedTaskGracePeriod {
			delete(stoppedTaskCache, id)
		}
	}

	// Append the most recently stopped task per slot from the cache.
	// For replicated services the key is "serviceID:slot"; for global
	// services (slot == 0) it is "serviceID:nodeID". Only the newest entry
	// per slot is kept so we don't flood the view with historical tasks.
	newestStopped := make(map[string]swarm.Task)
	for _, entry := range stoppedTaskCache {
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
