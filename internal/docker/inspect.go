package docker

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal"
	"github.com/jtgasper3/swarm-visualizer/internal/config"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

type SwarmData struct {
	ClusterName string            `json:"clusterName"`
	Networks    []network.Summary `json:"networks"`
	Nodes       []swarm.Node      `json:"nodes"`
	Services    []swarm.Service   `json:"services"`
	Tasks       []swarm.Task      `json:"tasks"`
}

var (
	broadcast           = make(chan []byte)
	lastBroadcastedData *SwarmData
)

func inspectSwarmServices(cfg *config.Config) {
	sleepDuration := 1 * time.Second

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	for {
		ctx := context.Background()

		nodes, errNode := getNodesInfo(ctx, cli, cfg)
		tasks, errTask := getTasksInfo(ctx, cli, cfg)
		services, errService := getServicesInfo(ctx, cli, cfg)
		networks, errNetwork := getNetworksInfo(ctx, cli, cfg)

		if errNode != nil || errTask != nil || errService != nil || errNetwork != nil {
			time.Sleep(sleepDuration)
			continue
		}

		data := SwarmData{
			ClusterName: cfg.ClusterName,
			Services:    services,
			Nodes:       nodes,
			Networks:    networks,
			Tasks:       tasks,
		}

		for _, item := range cfg.SensitiveDataPaths {
			clearErr := internal.ClearByPath(&data, item)
			if clearErr != nil {
				log.Println("Error clearing sensitive data:", clearErr, item)
			}
		}

		if lastBroadcastedData == nil || !reflect.DeepEqual(data, *lastBroadcastedData) {
			lastBroadcastedData = &data
			jsonBytes, err := json.Marshal(data)
			if err != nil {
				log.Println("Error marshalling combined data:", err)
				time.Sleep(sleepDuration)
				continue
			}
			broadcast <- jsonBytes
		}

		time.Sleep(sleepDuration)
	}
}

func getNetworksInfo(ctx context.Context, cli *client.Client, cfg *config.Config) ([]network.Summary, error) {
	networks, err := cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.KeyValuePair{Key: "scope", Value: "swarm"})})
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

func getNodesInfo(ctx context.Context, cli *client.Client, cfg *config.Config) ([]swarm.Node, error) {
	nodes, err := cli.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		log.Printf("Error fetching nodes: %v", err)
		return nil, err
	}

	// Sanitize nodes
	nodes = sanitizeNodes(nodes, cfg)

	return nodes, nil
}

func getServicesInfo(ctx context.Context, cli *client.Client, cfg *config.Config) ([]swarm.Service, error) {
	services, err := cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		log.Printf("Error fetching services: %v", err)
		return nil, err
	}

	// Sanitize services
	services = sanitizeServices(services, cfg)

	return services, nil
}

func getTasksInfo(ctx context.Context, cli *client.Client, cfg *config.Config) ([]swarm.Task, error) {
	tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{})
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return nil, err
	}

	// Sanitize tasks
	tasks = sanitizeTasks(tasks, cfg)

	return tasks, nil
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
		if slices.Contains(cfg.HideLabels, "all") || slices.Contains(cfg.HideLabels, "task") {
			t.Spec.ContainerSpec.Labels = nil
		}
	}
	return tasks
}
