package docker

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/jtgasper3/swarm-visualizer/internal/oauth"
)

var (
	upgrader = websocket.Upgrader{}
	clients  = make(map[*websocket.Conn]bool)
	mu       sync.Mutex
)

func RegisterDockerHandlers(cfg *config.Config) {
	go inspectSwarmServices(cfg)

	http.HandleFunc(cfg.ContextRoot+"ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnections(cfg, w, r)
	})

	go handleBroadcasts()
}

func handleConnections(cfg *config.Config, w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		http.Error(w, "Could not upgrade to WebSocket", http.StatusInternalServerError)
		return
	}
	defer ws.Close()

	if cfg.AuthEnabled {
		claims, err := oauth.ValidateToken(cfg, r)
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("401-Unauthorized"))
			log.Printf("Client unauthorized: %s %v", r.RemoteAddr, err)
			return
		}
		log.Printf("Client connected: %s, %s", r.RemoteAddr, claims["email"])
	} else {
		log.Printf("Client connected: %s", r.RemoteAddr)
	}

	jsonBytes, err := json.Marshal(lastBroadcastedData)
	if err != nil {
		log.Println("Error marshalling combined data:", err)

	}
	writeMessage(ws, jsonBytes)

	mu.Lock()
	clients[ws] = true
	mu.Unlock()

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Client disconnected: %s, %v", r.RemoteAddr, err)
			mu.Lock()
			delete(clients, ws)
			mu.Unlock()
			break
		}
	}
}

func handleBroadcasts() {
	for {
		msg := <-broadcast
		mu.Lock()
		for client := range clients {
			_, err := writeMessage(client, msg)
			if err != nil {
				delete(clients, client)
			}
		}
		mu.Unlock()
	}
}

func writeMessage(client *websocket.Conn, msg []byte) (bool, error) {
	err := client.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		log.Printf("Write error; closing: %s, %v", client.RemoteAddr(), err)
		client.Close()
	}

	return true, nil
}
