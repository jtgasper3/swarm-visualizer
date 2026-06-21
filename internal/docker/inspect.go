package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
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

type cachedTask struct {
	task      swarm.Task
	firstSeen time.Time
}

// stoppedTaskCache is only accessed from the single inspectSwarmServices goroutine.
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

// inspectSwarmServices polls the swarm and publishes a snapshot whenever it
// changes. Tasks are polled frequently for freshness; nodes, services, and
// networks change rarely, so they are polled on a slower cadence. The most
// recently fetched value for each group is cached between ticks and reassembled
// on every publish.
//
// Reassembly applies SensitiveDataPaths via ClearByPath, which only zeroes
// fields and is therefore idempotent; re-applying it to a cached (already
// cleared) slice that is also referenced by the previously published snapshot
// leaves that snapshot's bytes unchanged, so change detection stays correct.
func inspectSwarmServices(cfg *config.Config, src swarmSource, hub *Hub) {
	ctx := context.Background()

	var (
		nodes    []swarm.Node
		services []swarm.Service
		networks []network.Summary
		tasks    []swarm.Task

		haveStructural bool
		haveTasks      bool

		// lastPublished is the previous snapshot, kept for change detection. It is
		// only touched by this single goroutine.
		lastPublished *SwarmData
	)

	refreshStructural := func() bool {
		n, errN := getNodesInfo(ctx, src, cfg)
		s, errS := getServicesInfo(ctx, src, cfg)
		nw, errNw := getNetworksInfo(ctx, src, cfg)
		if errN != nil || errS != nil || errNw != nil {
			return false
		}
		nodes, services, networks = n, s, nw
		haveStructural = true
		return true
	}

	refreshTasks := func() bool {
		t, err := getTasksInfo(ctx, src, cfg)
		if err != nil {
			return false
		}
		tasks = t
		haveTasks = true
		return true
	}

	publish := func() {
		// Don't publish a partial view before every group has loaded once.
		if !haveStructural || !haveTasks {
			return
		}

		data := SwarmData{
			ClusterName: cfg.ClusterName,
			AuthEnabled: cfg.AuthEnabled,
			Services:    services,
			Nodes:       nodes,
			Networks:    networks,
			Tasks:       tasks,
		}

		for _, item := range cfg.SensitiveDataPaths {
			if clearErr := internal.ClearByPath(&data, item); clearErr != nil {
				log.Println("Error clearing sensitive data:", clearErr, item)
			}
		}

		if lastPublished == nil || !reflect.DeepEqual(data, *lastPublished) {
			jsonBytes, err := json.Marshal(data)
			if err != nil {
				log.Println("Error marshalling combined data:", err)
				return
			}
			lastPublished = &data
			hub.Publish(jsonBytes)
		}
	}

	// Initial fetch so clients connecting at startup get a full snapshot
	// promptly rather than waiting for the first structural tick.
	refreshStructural()
	refreshTasks()
	publish()

	taskTicker := time.NewTicker(taskPollInterval)
	defer taskTicker.Stop()
	structuralTicker := time.NewTicker(structuralPollInterval)
	defer structuralTicker.Stop()

	for {
		select {
		case <-taskTicker.C:
			if refreshTasks() {
				publish()
			}
		case <-structuralTicker.C:
			if refreshStructural() {
				publish()
			}
		}
	}
}

func getNetworksInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]network.Summary, error) {
	networks, err := src.Networks(ctx)
	if err != nil {
		log.Printf("Error fetching networks: %v", err)
		return nil, err
	}

	filteredNetworks := make([]network.Summary, 0, len(networks))
	for _, net := range networks {
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "network") {
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

func getServicesInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]swarm.Service, error) {
	services, err := src.Services(ctx)
	if err != nil {
		log.Printf("Error fetching services: %v", err)
		return nil, err
	}

	return sanitizeServices(services, cfg), nil
}

const failedTaskGracePeriod = 30 * time.Second

func getTasksInfo(ctx context.Context, src swarmSource, cfg *config.Config) ([]swarm.Task, error) {
	tasks, err := src.Tasks(ctx)
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return nil, err
	}

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
	for _, t := range newestStopped {
		result = append(result, t)
	}

	// Sanitize tasks
	result = sanitizeTasks(result, cfg)

	return result, nil
}

// sanitizeNodes removes or redacts fields on nodes according to the configuration.
func sanitizeNodes(nodes []swarm.Node, cfg *config.Config) []swarm.Node {
	for i := range nodes {
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "node") {
			nodes[i].Spec.Labels = nil
		}
	}
	return nodes
}

// sanitizeServices removes or redacts fields on services according to the configuration.
func sanitizeServices(services []swarm.Service, cfg *config.Config) []swarm.Service {
	for i := range services {
		svc := &services[i]

		if cfg.HideAllConfigs {
			svc.Spec.TaskTemplate.ContainerSpec.Configs = nil
		}
		if cfg.HideAllEnvs {
			svc.Spec.TaskTemplate.ContainerSpec.Env = nil
		}
		if cfg.HideAllMounts {
			svc.Spec.TaskTemplate.ContainerSpec.Mounts = nil
		}
		if cfg.HideAllSecrets {
			svc.Spec.TaskTemplate.ContainerSpec.Secrets = nil
		}
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "service") {
			svc.Spec.Labels = nil
		}
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "container") {
			svc.Spec.TaskTemplate.ContainerSpec.Labels = nil
		}

		// Service-level hide-envs label
		if svc.Spec.Labels != nil {
			if hideEnvsRaw, ok := svc.Spec.Labels["io.github.jtgasper3.visualizer.hide-envs"]; ok {
				hideEnvs := strings.Split(hideEnvsRaw, ",")
				hideSet := make(map[string]struct{}, len(hideEnvs))
				for j := range hideEnvs {
					hideEnvs[j] = strings.TrimSpace(hideEnvs[j])
					if hideEnvs[j] != "" {
						hideSet[hideEnvs[j]] = struct{}{}
					}
				}
				envs := svc.Spec.TaskTemplate.ContainerSpec.Env
				for k, env := range envs {
					if eq := strings.IndexByte(env, '='); eq > 0 {
						key := env[:eq]
						if _, found := hideSet[key]; found {
							envs[k] = key + "=(sanitized)"
						}
					} else {
						if _, found := hideSet[env]; found {
							envs[k] = env + "=(sanitized)"
						}
					}
				}
				svc.Spec.TaskTemplate.ContainerSpec.Env = envs
			}

			// Service-level hide-labels
			if hideLabels, ok := svc.Spec.Labels["io.github.jtgasper3.visualizer.hide-labels"]; ok {
				labelsToHide := strings.Split(hideLabels, ",")
				for _, label := range labelsToHide {
					label = strings.TrimSpace(label)
					if label == "" {
						continue
					}
					if svc.Spec.Labels == nil {
						continue
					}
					if _, exists := svc.Spec.Labels[label]; exists {
						svc.Spec.Labels[label] = "(sanitized)"
					}
				}
			}
		}

		// Container-level hide-labels
		if svc.Spec.TaskTemplate.ContainerSpec.Labels != nil {
			if hideLabels, ok := svc.Spec.TaskTemplate.ContainerSpec.Labels["io.github.jtgasper3.visualizer.hide-labels"]; ok {
				labelsToHide := strings.Split(hideLabels, ",")
				for _, label := range labelsToHide {
					label = strings.TrimSpace(label)
					if label == "" {
						continue
					}
					if _, exists := svc.Spec.TaskTemplate.ContainerSpec.Labels[label]; exists {
						svc.Spec.TaskTemplate.ContainerSpec.Labels[label] = "(sanitized)"
					}
				}
			}
		}
	}
	return services
}

// sanitizeTasks removes or redacts fields on tasks according to the configuration.
func sanitizeTasks(tasks []swarm.Task, cfg *config.Config) []swarm.Task {
	for i := range tasks {
		t := &tasks[i]
		if cfg.HideAllConfigs {
			t.Spec.ContainerSpec.Configs = nil
		}
		if cfg.HideAllEnvs {
			t.Spec.ContainerSpec.Env = nil
		}
		if cfg.HideAllMounts {
			t.Spec.ContainerSpec.Mounts = nil
		}
		if cfg.HideAllSecrets {
			t.Spec.ContainerSpec.Secrets = nil
		}
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "container") {
			t.Spec.ContainerSpec.Labels = nil
		}
	}
	return tasks
}
