package docker

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

type nodeViewModel struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Hostname             string `json:"hostname"`
	Role                 string `json:"role"`
	PlatformArchitecture string `json:"platformArchitecture"`
	MemoryBytes          int64  `json:"memoryBytes"`
	CpuCores             int64  `json:"cpuCores"`
	Availability         string `json:"availability"`
	Status               string `json:"status"`
}

type serviceViewModel struct {
	ID                 string                          `json:"id"`
	Name               string                          `json:"name"`
	Image              string                          `json:"image"`
	Mode               string                          `json:"mode"`
	Replicas           *uint64                         `json:"replicas"`
	ReservationsCpu    int64                           `json:"reservationsCpu"`
	ReservationsMemory int64                           `json:"reservationsMemory"`
	LimitsCpu          int64                           `json:"limitsCpu"`
	LimitsMemory       int64                           `json:"limitsMemory"`
	Networks           []swarm.NetworkAttachmentConfig `json:"networks"`
}

type taskViewModel struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	NodeID       string    `json:"nodeId"`
	ServiceID    string    `json:"serviceId"`
	ContainerID  string    `json:"containerId"`
	DesiredState string    `json:"desiredState"`
	State        string    `json:"state"`
	Slot         int       `json:"slot"`
	CreatedAt    time.Time `json:"createdAt"`
}

type networkViewModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SwarmData struct {
	ClusterName string             `json:"clusterName"`
	Networks    []networkViewModel `json:"networks"`
	Nodes       []nodeViewModel    `json:"nodes"`
	Services    []serviceViewModel `json:"services"`
	Tasks       []taskViewModel    `json:"tasks"`
}

var (
	broadcast           = make(chan []byte)
	lastBroadcastedData *SwarmData
)

func inspectSwarmServices(cfg *config.Config) {
	sleepDuration := 1 * time.Second

	filterArgs := filters.NewArgs()
	filterArgs.Add("desired-state", "running")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	for {
		ctx := context.Background()

		nodeViewModels, errNode := getNodesInfo(ctx, cli)
		taskViewModels, errTask := getTasksInfo(ctx, cli, filterArgs)
		serviceViewModels, errService := getServicesInfo(ctx, cli)
		networkViewModels, errNetwork := getNetworksInfo(ctx, cli)

		if errNode != nil || errTask != nil || errService != nil || errNetwork != nil {
			time.Sleep(sleepDuration)
			continue
		}

		data := SwarmData{
			ClusterName: cfg.ClusterName,
			Services:    serviceViewModels,
			Nodes:       nodeViewModels,
			Networks:    networkViewModels,
			Tasks:       taskViewModels,
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

func getNetworksInfo(ctx context.Context, cli *client.Client) ([]networkViewModel, error) {
	networks, err := cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.KeyValuePair{Key: "scope", Value: "swarm"}, filters.KeyValuePair{Key: "dangling", Value: "false"})})
	if err != nil {
		log.Printf("Error fetching networks: %v", err)
		return nil, err
	}

	var networkViewModels []networkViewModel
	for _, network := range networks {
		if !network.Ingress {
			networkViewModels = append(networkViewModels, networkViewModel{
				ID:   network.ID,
				Name: network.Name,
			})
		}
	}
	return networkViewModels, nil
}

func getNodesInfo(ctx context.Context, cli *client.Client) ([]nodeViewModel, error) {
	nodes, err := cli.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		log.Printf("Error fetching nodes: %v", err)
		return nil, err
	}

	var nodeViewModels []nodeViewModel
	for _, node := range nodes {
		nodeViewModels = append(nodeViewModels, nodeViewModel{
			ID:                   node.ID,
			Hostname:             node.Description.Hostname,
			Name:                 node.Spec.Name,
			Role:                 string(node.Spec.Role),
			PlatformArchitecture: node.Description.Platform.Architecture,
			CpuCores:             node.Description.Resources.NanoCPUs / 1e9,
			MemoryBytes:          node.Description.Resources.MemoryBytes,
			Availability:         string(node.Spec.Availability),
			Status:               string(node.Status.State),
		})
	}
	return nodeViewModels, nil
}

func getTasksInfo(ctx context.Context, cli *client.Client, filterArgs filters.Args) ([]taskViewModel, error) {
	tasks, err := cli.TaskList(ctx, swarm.TaskListOptions{Filters: filterArgs})
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return nil, err
	}
	var taskViewModels []taskViewModel
	for _, task := range tasks {
		var containerId string
		if task.Status.ContainerStatus != nil && task.Status.ContainerStatus.ContainerID != "" {
			containerId = task.Status.ContainerStatus.ContainerID
		}

		taskViewModels = append(taskViewModels, taskViewModel{
			ID:           task.ID,
			NodeID:       task.NodeID,
			ServiceID:    task.ServiceID,
			ContainerID:  containerId,
			DesiredState: string(task.DesiredState),
			State:        string(task.Status.State),
			Slot:         task.Slot,
			CreatedAt:    task.CreatedAt,
		})
	}
	return taskViewModels, nil
}

func getServicesInfo(ctx context.Context, cli *client.Client) ([]serviceViewModel, error) {
	services, err := cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		log.Printf("Error fetching services: %v", err)
		return nil, err
	}

	var serviceViewModels []serviceViewModel
	for _, service := range services {
		mode := "unknown"
		if service.Spec.Mode.Replicated != nil {
			mode = "replicated"
		} else if service.Spec.Mode.Global != nil {
			mode = "global"
		}

		var replicas *uint64
		if service.Spec.Mode.Replicated != nil && service.Spec.Mode.Replicated.Replicas != nil {
			r := *service.Spec.Mode.Replicated.Replicas
			replicas = &r
		}

		serviceViewModels = append(serviceViewModels, serviceViewModel{
			ID:                 service.ID,
			Name:               service.Spec.Name,
			Image:              service.Spec.TaskTemplate.ContainerSpec.Image,
			Mode:               mode,
			Replicas:           replicas,
			ReservationsCpu:    service.Spec.TaskTemplate.Resources.Reservations.NanoCPUs / 1e9,
			ReservationsMemory: service.Spec.TaskTemplate.Resources.Reservations.MemoryBytes,
			LimitsCpu:          service.Spec.TaskTemplate.Resources.Limits.NanoCPUs / 1e9,
			LimitsMemory:       service.Spec.TaskTemplate.Resources.Limits.MemoryBytes,
			Networks:           service.Spec.TaskTemplate.Networks,
		})
	}
	return serviceViewModels, nil
}
