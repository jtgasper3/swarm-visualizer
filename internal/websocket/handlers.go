package websocket

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/jtgasper3/swarm-visualizer/internal/shared"
)

var (
	upgrader = websocket.Upgrader{}
	clients  = make(map[*websocket.Conn]bool)
)

func HandleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		http.Error(w, "Could not upgrade to WebSocket", http.StatusInternalServerError)
		return
	}
	defer ws.Close()

	shared.Mu.Lock()
	clients[ws] = true

	data, err := json.Marshal(shared.LastBroadcastedData)
	if err != nil {
		log.Println("Error marshalling combined data:", err)
	}
	err = ws.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		log.Printf("Write error: %v", err)
		ws.Close()
		delete(clients, ws)
	}
	shared.Mu.Unlock()
	log.Printf("New client connected: %s", r.RemoteAddr)

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Client disconnected: %v", err)
			shared.Mu.Lock()
			delete(clients, ws)
			shared.Mu.Unlock()
			break
		}
	}
}

func HandleMessages() {
	for {
		msg := <-shared.Broadcast
		shared.Mu.Lock()
		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				log.Printf("Write error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
		shared.Mu.Unlock()
	}
}
