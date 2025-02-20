package docker

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/models"
	"github.com/jtgasper3/swarm-visualizer/internal/shared"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

func InspectSwarmServices() {
	sleepDuration := 2 * time.Second
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	for {
		ctx, ctxCancel := context.WithTimeout(context.Background(), 10*time.Second)

		services, err := cli.ServiceList(ctx, types.ServiceListOptions{})
		if err != nil {
			log.Printf("Error fetching services: %v", err)
			time.Sleep(sleepDuration)
			continue
		}

		nodes, err := cli.NodeList(ctx, types.NodeListOptions{})
		if err != nil {
			log.Printf("Error fetching nodes: %v", err)
			time.Sleep(sleepDuration)
			continue
		}

		filterArgs := filters.NewArgs()
		filterArgs.Add("desired-state", "running")
		tasks, err := cli.TaskList(ctx, types.TaskListOptions{Filters: filterArgs})
		if err != nil {
			log.Printf("Error fetching tasks: %v", err)
			time.Sleep(sleepDuration)
			continue
		}

		ctxCancel()

		var nodeViewModels []models.NodeViewModel
		for _, node := range nodes {
			nodeViewModels = append(nodeViewModels, models.NodeViewModel{
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

		var taskViewModels []models.TaskViewModel
		for _, task := range tasks {
			taskViewModels = append(taskViewModels, models.TaskViewModel{
				ID:           task.ID,
				NodeID:       task.NodeID,
				ServiceID:    task.ServiceID,
				DesiredState: string(task.DesiredState),
				State:        string(task.Status.State),
				CreatedAt:    task.CreatedAt,
			})
		}

		var serviceViewModels []models.ServiceViewModel
		for _, service := range services {
			mode := "unknown"
			if service.Spec.Mode.Replicated != nil {
				mode = "replicated"
			} else if service.Spec.Mode.Global != nil {
				mode = "global"
			}

			serviceViewModels = append(serviceViewModels, models.ServiceViewModel{
				ID:    service.ID,
				Name:  service.Spec.Name,
				Image: service.Spec.TaskTemplate.ContainerSpec.Image,
				Mode:  mode,
			})
		}

		data := models.SwarmData{
			ClusterName: shared.ClusterName,
			Services:    serviceViewModels,
			Nodes:       nodeViewModels,
			Tasks:       taskViewModels,
		}

		if shared.LastBroadcastedData == nil || !reflect.DeepEqual(data, *shared.LastBroadcastedData) {
			shared.LastBroadcastedData = &data
			jsonData, err := json.Marshal(data)
			if err != nil {
				log.Println("Error marshalling combined data:", err)
				time.Sleep(sleepDuration)
				continue
			}

			shared.Broadcast <- jsonData
		}

		time.Sleep(sleepDuration)
	}
}
