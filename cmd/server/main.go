package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os/signal"
	"path"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/jtgasper3/swarm-visualizer/internal/docker"
	"github.com/jtgasper3/swarm-visualizer/internal/oauth"
	"github.com/jtgasper3/swarm-visualizer/web"
)

// version and revision are set at build time with -ldflags; see the Dockerfile.
var (
	version  = "dev"
	revision = "unknown"
)

func main() {
	log.Printf("swarm-visualizer %s (commit %s)", version, revision)

	cfg := config.LoadConfig()

	log.Printf("Server root context is %s", cfg.ContextRoot)

	// ctx ends on the first interrupt or termination signal, which stops the
	// polling and starts the shutdown below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler, hub := newHandler(ctx, cfg)

	server := &http.Server{
		Addr:              ":" + cfg.ListenerPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      90 * time.Second,
		// Without this a keep-alive connection that has gone quiet is held
		// open for as long as the client cares to leave it.
		IdleTimeout: 2 * time.Minute,
	}

	go func() {
		log.Printf("Server started on :%s", cfg.ListenerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("ListenAndServe: ", err)
		}
	}()

	<-ctx.Done()
	// A second signal now ends the process at once instead of being swallowed.
	stop()

	log.Println("Shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Shutdown leaves WebSocket connections alone, because the server handed
	// them over when they were upgraded. Close them first so each dashboard is
	// told the server is going away rather than finding the connection dead.
	hub.Close(shutdownCtx)
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Server forced to shutdown: ", err)
	}
	log.Println("Server stopped")
}

// newHandler assembles everything the server answers: the frontend, sign-in,
// the WebSocket that carries the snapshots, and the health check. What it
// starts in the background, polling the swarm and keeping sign-in's keys
// fresh, stops when ctx ends; the Hub it returns has to be closed as well.
func newHandler(ctx context.Context, cfg *config.Config) (http.Handler, *docker.Hub) {
	mux := http.NewServeMux()

	mux.Handle(cfg.ContextRoot, http.StripPrefix(cfg.ContextRoot, staticHandler(cfg.StaticDir)))

	// Register auth first so its token validator can be handed to the WebSocket
	// handler. auth is nil when authentication is disabled.
	auth := oauth.RegisterOAuthHandlers(ctx, mux, cfg)
	var validate docker.TokenValidator
	if auth != nil {
		validate = auth.ValidateToken
	}

	hub := docker.RegisterDockerHandlers(ctx, mux, cfg, validate)

	// Unauthenticated readiness endpoint at a fixed path (independent of
	// CONTEXT_ROOT) for orchestrator health checks.
	mux.Handle("/healthz", healthzHandler(hub.Ready))

	return securityHeaders(mux), hub
}

// staticHandler serves the frontend: the copy built into the binary, or the
// files in dir when one is given, which lets them be edited while the server
// runs.
func staticHandler(dir string) http.Handler {
	if dir != "" {
		log.Printf("Serving the frontend from %s", dir)
		return pagePolicy(revalidated(http.FileServer(http.Dir(dir))))
	}
	files := web.Static()
	return pagePolicy(revalidated(withETags(files, http.FileServerFS(files))))
}

// revalidated makes browsers check with the server before reusing a cached
// static file. The frontend has no build step and no versioned file names, so
// the page and the modules it imports have to come from the same release. Left
// to their own heuristics, browsers reload the page after an upgrade but keep
// taking its modules from cache, and a new page running against old modules
// can fail to load at all. "no-cache" still permits caching: a file that has
// not changed costs a 304 rather than a download.
func revalidated(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// withETags gives each file in files an ETag made from its contents. Files
// built into the binary have no modification time, so without one a browser
// would have nothing to revalidate with and would download every file on every
// load. A content hash is also exact where a time is not: the tag changes when
// the file does, whichever release is the newer.
func withETags(files fs.FS, h http.Handler) http.Handler {
	tags := map[string]string{}
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		tags[name] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to read the built-in frontend: %v", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if tag, ok := tags[name]; ok {
			w.Header().Set("ETag", tag)
		}
		h.ServeHTTP(w, r)
	})
}

// hostPattern matches a Host header that is safe to repeat in a response
// header: a name or address with an optional port, and nothing else.
var hostPattern = regexp.MustCompile(`^([A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(:[0-9]+)?$`)

// pagePolicy sets the Content-Security-Policy for the page. The dashboard shows
// labels, environment variables and error text taken from the swarm; if any of
// it were ever rendered as markup, this limits what the result could load and
// where it could send what it read.
//
// Scripts and styles may be inline, and scripts may be evaluated, because the
// page is written that way: its templates are compiled in the browser. The
// policy is therefore about origins, and the only one is this server: the
// libraries and fonts the page uses are served with it. It does not forbid
// framing, since nothing here acts on a click and a dashboard is often embedded
// in another page.
func pagePolicy(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 'self' covers the WebSocket in current browsers; older ones need the
		// ws: and wss: forms of this host spelled out.
		connect := "'self'"
		if hostPattern.MatchString(r.Host) {
			connect += " ws://" + r.Host + " wss://" + r.Host
		}

		w.Header().Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self' 'unsafe-inline' 'unsafe-eval'",
			"style-src 'self' 'unsafe-inline'",
			"font-src 'self'",
			"img-src 'self' data:",
			"connect-src " + connect,
			"object-src 'none'",
			"base-uri 'self'",
			"form-action 'self'",
		}, "; "))
		h.ServeHTTP(w, r)
	})
}

// securityHeaders sets the headers that apply to every response. nosniff stops
// a browser from guessing a content type other than the one sent. The referrer
// policy keeps the dashboard's address, which may be an internal host name,
// out of the requests the page makes to other sites.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
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
