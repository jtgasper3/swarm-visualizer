package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/gorilla/websocket"
)

type SwarmData struct {
	Nodes    []swarm.Node    `json:"nodes"`
	Services []swarm.Service `json:"services"`
	Tasks    []swarm.Task    `json:"tasks"`
}

var (
	upgrader  = websocket.Upgrader{}
	clients   = make(map[*websocket.Conn]bool)
	broadcast = make(chan []byte)
	last_msg  []byte
	mu        sync.Mutex
)

func main() {
	// Start inspecting Swarm services in a separate goroutine
	go inspectSwarmServices()

	// Handle WebSocket connections
	http.HandleFunc("/ws", handleConnections)

	// Serve the HTML page
	http.HandleFunc("/", serveHome)

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
	err = ws.WriteMessage(websocket.TextMessage, last_msg)
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

		// Cache it for new clients and send it to every connected client
		mu.Lock()
		last_msg = msg

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
	cli, err := client.NewClientWithOpts(client.WithHost(client.DefaultDockerHost))
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

		nodes, err := cli.NodeList(context.Background(), types.NodeListOptions{})
		if err != nil {
			log.Println("Error fetching nodes:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		tasks, err := cli.TaskList(context.Background(), types.TaskListOptions{})
		if err != nil {
			log.Println("Error fetching tasks:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// combine the two
		info := SwarmData{nodes, services, tasks}

		// Marshal the services into JSON
		data, err := json.Marshal(info)

		if err != nil {
			log.Println("Error marshalling swarm info:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Send the data to the broadcast channel
		broadcast <- data

		// Wait for 10 seconds before the next fetch
		time.Sleep(10 * time.Second)
	}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}
