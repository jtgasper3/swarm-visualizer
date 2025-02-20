package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jtgasper3/swarm-visualizer/internal/docker"
	"github.com/jtgasper3/swarm-visualizer/internal/shared"
	"github.com/jtgasper3/swarm-visualizer/internal/websocket"
)

const (
	defaultContextRoot  = "/"
	defaultListenerPort = "8080"
)

func main() {
	shared.ClusterName = os.Getenv("CLUSTER_NAME")

	contextRoot := os.Getenv("CONTEXT_ROOT")
	if contextRoot == "" {
		contextRoot = defaultContextRoot
	}
	if !strings.HasSuffix(contextRoot, "/") {
		contextRoot += "/"
	}
	log.Printf("Server root context is %s", contextRoot)

	go docker.InspectSwarmServices()

	fs := http.FileServer(http.Dir("./static"))
	http.Handle(contextRoot, http.StripPrefix(contextRoot, fs))
	http.HandleFunc(contextRoot+"ws", websocket.HandleConnections)

	go websocket.HandleMessages()

	listenerPort := os.Getenv("LISTENER_PORT")
	if listenerPort == "" {
		listenerPort = defaultListenerPort
	}
	log.Printf("Server started on :%s", listenerPort)
	err := http.ListenAndServe(":"+listenerPort, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
