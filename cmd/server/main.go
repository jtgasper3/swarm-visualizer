package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	// Unauthenticated readiness endpoint at a fixed path (independent of
	// CONTEXT_ROOT) for orchestrator health checks. Reports ready once the
	// first successful Docker poll has published data.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if docker.Ready() {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	})

	server := &http.Server{
		Addr:              ":" + cfg.ListenerPort,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      90 * time.Second,
	}

	go func() {
		log.Printf("Server started on :%s", cfg.ListenerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("ListenAndServe: ", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown: ", err)
	}
	log.Println("Server stopped")
}
