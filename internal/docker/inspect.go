package docker

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type nodeViewModel struct {
	ID                   string `json:"id"`
	Hostname             string `json:"hostname"`
	Name                 string `json:"name"`
	Role                 string `json:"role"`
	PlatformArchitecture string `json:"platformArchitecture"`
	MemoryBytes          int64  `json:"memoryBytes"`
	Availability         string `json:"availability"`
	Status               string `json:"status"`
}

type serviceViewModel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Mode  string `json:"mode"`
}

type taskViewModel struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	NodeID       string    `json:"nodeId"`
	ServiceID    string    `json:"serviceId"`
	DesiredState string    `json:"desiredState"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
}

type SwarmData struct {
	ClusterName string             `json:"clusterName"`
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

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	for {
		ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)

		nodeViewModels, errNode := getNodesInfo(ctx, cli)
		taskViewModels, errTask := getTasksInfo(ctx, cli, filterArgs)
		serviceViewModels, errService := getServicesInfo(ctx, cli)

		ctxCancel()

		if errNode != nil || errTask != nil || errService != nil {
			time.Sleep(sleepDuration)
			continue
		}

		data := SwarmData{
			ClusterName: cfg.ClusterName,
			Services:    serviceViewModels,
			Nodes:       nodeViewModels,
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

func getNodesInfo(ctx context.Context, cli *client.Client) ([]nodeViewModel, error) {
	nodes, err := cli.NodeList(ctx, types.NodeListOptions{})
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
			MemoryBytes:          node.Description.Resources.MemoryBytes,
			Availability:         string(node.Spec.Availability),
			Status:               string(node.Status.State),
		})
	}
	return nodeViewModels, nil
}

func getTasksInfo(ctx context.Context, cli *client.Client, filterArgs filters.Args) ([]taskViewModel, error) {
	tasks, err := cli.TaskList(ctx, types.TaskListOptions{Filters: filterArgs})
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return nil, err
	}
	var taskViewModels []taskViewModel
	for _, task := range tasks {
		taskViewModels = append(taskViewModels, taskViewModel{
			ID:           task.ID,
			NodeID:       task.NodeID,
			ServiceID:    task.ServiceID,
			DesiredState: string(task.DesiredState),
			State:        string(task.Status.State),
			CreatedAt:    task.CreatedAt,
		})
	}
	return taskViewModels, nil
}

func getServicesInfo(ctx context.Context, cli *client.Client) ([]serviceViewModel, error) {
	services, err := cli.ServiceList(ctx, types.ServiceListOptions{})
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

		serviceViewModels = append(serviceViewModels, serviceViewModel{
			ID:    service.ID,
			Name:  service.Spec.Name,
			Image: service.Spec.TaskTemplate.ContainerSpec.Image,
			Mode:  mode,
		})
	}
	return serviceViewModels, nil
}
