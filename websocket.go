package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var (
	upgrader  = websocket.Upgrader{}
	clients   = make(map[*websocket.Conn]bool)
	broadcast = make(chan []byte)
)

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		http.Error(w, "Could not upgrade to WebSocket", http.StatusInternalServerError)
		return
	}
	defer ws.Close()

	mu.Lock()
	clients[ws] = true

	data, err := json.Marshal(lastBroadcastedData)
	if err != nil {
		log.Println("Error marshalling combined data:", err)
	}
	err = ws.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		log.Printf("Write error: %v", err)
		ws.Close()
		delete(clients, ws)
	}
	mu.Unlock()
	log.Printf("New client connected: %s", r.RemoteAddr)

	for {
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
		msg := <-broadcast
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
