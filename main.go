package main

import (
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	defaultRootContext  = "/"
	defaultListenerPort = "8080"
)

func main() {
	clusterName = os.Getenv("CLUSTER_NAME")

	rootContext := os.Getenv("ROOT_CONTEXT")
	if rootContext == "" {
		rootContext = defaultRootContext
	}
	if !strings.HasSuffix(rootContext, "/") {
		rootContext += "/"
	}
	log.Printf("Server root context is %s", rootContext)

	go inspectSwarmServices()

	fs := http.FileServer(http.Dir("./static"))
	http.Handle(rootContext, http.StripPrefix(rootContext, fs))
	http.HandleFunc(rootContext+"ws", handleConnections)

	go handleMessages()

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
