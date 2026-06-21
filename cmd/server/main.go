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

	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir("./static"))
	mux.Handle(contextRoot, http.StripPrefix(contextRoot, fs))

	hub := docker.RegisterDockerHandlers(mux, cfg)
	oauth.RegisterOAuthHandlers(mux, cfg)

	// Unauthenticated readiness endpoint at a fixed path (independent of
	// CONTEXT_ROOT) for orchestrator health checks.
	mux.Handle("/healthz", healthzHandler(hub.Ready))

	server := &http.Server{
		Addr:              ":" + cfg.ListenerPort,
		Handler:           mux,
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

// healthzHandler reports readiness for orchestrator health checks. It returns
// 200 once ready returns true (the first successful Docker poll has published
// data) and 503 otherwise.
func healthzHandler(ready func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ready() {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}
}
