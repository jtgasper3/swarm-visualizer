package main

import (
	"log"
	"net/http"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/jtgasper3/swarm-visualizer/internal/docker"
	"github.com/jtgasper3/swarm-visualizer/internal/oauth"
)

func main() {
	cfg := config.LoadConfig()

	contextRoot := cfg.ContextRoot
	log.Printf("Server root context is %s", contextRoot)

	fs := http.FileServer(http.Dir("./static"))
	http.Handle(contextRoot, http.StripPrefix(contextRoot, fs))

	docker.RegisterDockerHandlers(cfg)
	oauth.RegisterOAuthHandlers(cfg)

	listenerPort := cfg.ListenerPort
	log.Printf("Server started on :%s", listenerPort)
	err := http.ListenAndServe(":"+listenerPort, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
