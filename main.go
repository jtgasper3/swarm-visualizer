package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/gorilla/websocket"
)

type ServiceViewModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type NodeViewModel struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
}

type TaskViewModel struct {
	ID        string `json:"id"`
	NodeID    string `json:"nodeId"`
	ServiceID string `json:"serviceId"`
}

type SwarmData struct {
	Nodes    []NodeViewModel    `json:"nodes"`
	Services []ServiceViewModel `json:"services"`
	Tasks    []TaskViewModel    `json:"tasks"`
}

var (
	upgrader            = websocket.Upgrader{}
	clients             = make(map[*websocket.Conn]bool)
	broadcast           = make(chan []byte)
	lastBroadcastedData *SwarmData
	mu                  sync.Mutex
)

func main() {
	// Start inspecting Swarm services in a separate goroutine
	go inspectSwarmServices()

	// Serve static files from the "./static" directory
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	// Handle WebSocket connections
	http.HandleFunc("/ws", handleConnections)

	// Start broadcasting messages to clients
	go handleMessages()

	// Start the server
	log.Println("Server started on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	// Upgrade initial GET request to a WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer ws.Close()

	// Register new client
	mu.Lock()
	clients[ws] = true

	// Marshal the combined data into JSON
	data, err := json.Marshal(lastBroadcastedData)
	if err != nil {
		log.Println("Error marshalling combined data:", err)
	}

	// Send the last message
	err = ws.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		log.Printf("Write error: %v", err)
		ws.Close()
		delete(clients, ws)
	}
	mu.Unlock()
	log.Println("New client connected")

	for {
		// Keep the connection open
		_, _, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Client disconnected: %v", err)
			mu.Lock()
			delete(clients, ws)
			mu.Unlock()
			break
		}
	}
}

func handleMessages() {
	for {
		// Grab the next message from the broadcast channel
		msg := <-broadcast

		// Send it to every connected client
		mu.Lock()

		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				log.Printf("Write error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
		mu.Unlock()
	}
}

func inspectSwarmServices() {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	for {
		// Fetch the list of Swarm services
		services, err := cli.ServiceList(context.Background(), types.ServiceListOptions{})
		if err != nil {
			log.Println("Error fetching services:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Fetch the list of Swarm nodes
		nodes, err := cli.NodeList(context.Background(), types.NodeListOptions{})
		if err != nil {
			log.Println("Error fetching nodes:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Fetch the list of Swarm tasks
		tasks, err := cli.TaskList(context.Background(), types.TaskListOptions{})
		if err != nil {
			log.Println("Error fetching task:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Map services to ServiceViewModel
		var serviceViewModels []ServiceViewModel
		for _, service := range services {
			mode := "Unknown"
			if service.Spec.Mode.Replicated != nil {
				mode = "Replicated"
			} else if service.Spec.Mode.Global != nil {
				mode = "Global"
			}
			serviceViewModels = append(serviceViewModels, ServiceViewModel{
				ID:   service.ID,
				Name: service.Spec.Name,
				Mode: mode,
			})
		}

		// Map nodes to NodeViewModel
		var nodeViewModels []NodeViewModel
		for _, node := range nodes {
			nodeViewModels = append(nodeViewModels, NodeViewModel{
				ID:       node.ID,
				Hostname: node.Description.Hostname,
				Status:   string(node.Status.State),
			})
		}

		// Map nodes to NodeViewModel
		var taskViewModels []TaskViewModel
		for _, task := range tasks {
			taskViewModels = append(taskViewModels, TaskViewModel{
				ID:        task.ID,
				NodeID:    task.NodeID,
				ServiceID: task.ServiceID,
			})
		}

		// Combine services and nodes into a single struct
		data := SwarmData{
			Services: serviceViewModels,
			Nodes:    nodeViewModels,
			Tasks:    taskViewModels,
		}

		// Compare the new data with the last broadcasted data
		if lastBroadcastedData == nil || !reflect.DeepEqual(data, *lastBroadcastedData) {
			// Update the last broadcasted data
			lastBroadcastedData = &data

			// Marshal the combined data into JSON
			jsonData, err := json.Marshal(data)
			if err != nil {
				log.Println("Error marshalling combined data:", err)
				time.Sleep(10 * time.Second)
				continue
			}

			// Send the combined JSON data to the broadcast channel
			broadcast <- jsonData
		}

		// Wait for 10 seconds before the next fetch
		time.Sleep(10 * time.Second)
	}
}
