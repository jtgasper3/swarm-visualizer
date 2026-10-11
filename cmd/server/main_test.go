package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// TestNewHandler_StopsWhatItStarted verifies that once main's shutdown steps
// have run, the goroutines the server started are gone: the one that polls the
// swarm, and the one that hands each snapshot to the connected clients. The
// second used to run for as long as the process did.
func TestNewHandler_StopsWhatItStarted(t *testing.T) {
	running := func() (found []string) {
		stacks := make([]byte, 1<<16)
		for {
			if n := runtime.Stack(stacks, true); n < len(stacks) {
				stacks = stacks[:n]
				break
			}
			stacks = make([]byte, 2*len(stacks))
		}
		for _, name := range []string{"docker.(*inspector).run(", "docker.(*Hub).runBroadcasts("} {
			if bytes.Contains(stacks, []byte(name)) {
				found = append(found, name)
			}
		}
		return found
	}
	within := func(limit time.Duration, done func() bool) bool {
		for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if done() {
				return true
			}
		}
		return done()
	}

	server := startDashboard(t, "empty")
	ready := func() bool {
		resp, err := http.Get(server.URL + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	if !within(5*time.Second, ready) {
		t.Fatal("the server did not become ready")
	}
	if got := running(); len(got) != 2 {
		t.Fatalf("found %q running before the shutdown, want both; have they been renamed?", got)
	}

	server.Stop()
	if !within(2*time.Second, func() bool { return len(running()) == 0 }) {
		t.Errorf("still running after the shutdown: %q", running())
	}
}

func TestHealthzHandler(t *testing.T) {
	tests := []struct {
		name  string
		ready bool
		want  int
	}{
		{name: "ready returns 200", ready: true, want: http.StatusOK},
		{name: "not ready returns 503", ready: false, want: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := healthzHandler(func() bool { return tc.ready })
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
			if tc.ready && rr.Body.String() != "ok" {
				t.Fatalf("body = %q, want %q", rr.Body.String(), "ok")
			}
		})
	}
}

// TestRevalidated verifies that static files are served so that a browser
// revalidates them on every load, and that revalidation tells an unchanged
// file from one an upgrade has replaced.
func TestRevalidated(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, modTime time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("set time on %s: %v", name, err)
		}
	}
	released := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	write("index.html", "<html>v1</html>", released)
	write("utils.js", "export const v = 1;", released)

	h := revalidated(http.FileServer(http.Dir(dir)))
	get := func(path, ifModifiedSince string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if ifModifiedSince != "" {
			req.Header.Set("If-Modified-Since", ifModifiedSince)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// "/index.html" is a redirect to "/", which a browser would otherwise be
	// free to remember indefinitely.
	for _, path := range []string{"/", "/utils.js", "/index.html"} {
		if got := get(path, "").Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, "no-cache")
		}
	}

	for _, path := range []string{"/", "/utils.js"} {
		first := get(path, "")
		if first.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", path, first.Code, http.StatusOK)
		}
		lastModified := first.Header().Get("Last-Modified")
		if lastModified == "" {
			t.Fatalf("GET %s: no Last-Modified, so the browser has nothing to revalidate with", path)
		}

		again := get(path, lastModified)
		if again.Code != http.StatusNotModified {
			t.Errorf("GET %s, unchanged: status = %d, want %d", path, again.Code, http.StatusNotModified)
		}
		if got := again.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s, unchanged: Cache-Control = %q, want %q", path, got, "no-cache")
		}
	}

	// An upgrade replaces the file; the copy the browser holds no longer validates.
	cached := get("/utils.js", "").Header().Get("Last-Modified")
	write("utils.js", "export const v = 2;", released.Add(24*time.Hour))

	upgraded := get("/utils.js", cached)
	if upgraded.Code != http.StatusOK {
		t.Errorf("GET /utils.js after an upgrade: status = %d, want %d", upgraded.Code, http.StatusOK)
	}
	if body := upgraded.Body.String(); body != "export const v = 2;" {
		t.Errorf("GET /utils.js after an upgrade: body = %q, want the new file", body)
	}
}

// TestWithETags verifies that files with no modification time, as the ones
// built into the binary are, can still be revalidated, and that the validator
// follows the content.
func TestWithETags(t *testing.T) {
	serve := func(files fstest.MapFS, path, ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rr := httptest.NewRecorder()
		revalidated(withETags(files, http.FileServerFS(files))).ServeHTTP(rr, req)
		return rr
	}
	v1 := fstest.MapFS{
		"index.html":     {Data: []byte("<html>v1</html>")},
		"utils.js":       {Data: []byte("export const v = 1;")},
		"vendor/lib.js":  {Data: []byte("export default 1;")},
		"vendor/same.js": {Data: []byte("export default 1;")},
	}

	tags := map[string]string{}
	for _, path := range []string{"/", "/utils.js", "/vendor/lib.js"} {
		first := serve(v1, path, "")
		if first.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", path, first.Code, http.StatusOK)
		}
		tag := first.Header().Get("ETag")
		if tag == "" {
			t.Fatalf("GET %s: no ETag, so the browser has nothing to revalidate with", path)
		}
		if first.Header().Get("Last-Modified") != "" {
			t.Fatalf("GET %s: this test is meant to cover files with no modification time", path)
		}
		tags[path] = tag

		again := serve(v1, path, tag)
		if again.Code != http.StatusNotModified {
			t.Errorf("GET %s with its own ETag: status = %d, want %d", path, again.Code, http.StatusNotModified)
		}
		if again.Body.Len() != 0 {
			t.Errorf("GET %s with its own ETag: the body was sent again", path)
		}
	}

	if tags["/"] == tags["/utils.js"] {
		t.Error("different files must have different ETags")
	}
	if same := serve(v1, "/vendor/same.js", "").Header().Get("ETag"); same != tags["/vendor/lib.js"] {
		t.Error("the ETag should follow the content, so identical files share one")
	}
	if tag := serve(v1, "/missing.js", "").Header().Get("ETag"); tag != "" {
		t.Errorf("a missing file was given the ETag %s", tag)
	}

	// Another release: one file changed, in either direction of time.
	v2 := fstest.MapFS{
		"index.html": {Data: []byte("<html>v1</html>")},
		"utils.js":   {Data: []byte("export const v = 2;")},
	}
	changed := serve(v2, "/utils.js", tags["/utils.js"])
	if changed.Code != http.StatusOK || changed.Body.String() != "export const v = 2;" {
		t.Errorf("a changed file was not sent: status %d, body %q", changed.Code, changed.Body.String())
	}
	if unchanged := serve(v2, "/", tags["/"]); unchanged.Code != http.StatusNotModified {
		t.Errorf("an unchanged file was sent again: status %d", unchanged.Code)
	}
}

// TestStaticHandler verifies both sources of the frontend: the copy built into
// the binary, and a directory given in its place.
func TestStaticHandler(t *testing.T) {
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://dashboard.example.com"+path, nil))
		return rr
	}

	builtIn := staticHandler("")
	page := get(builtIn, "/")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<div id="app">`) {
		t.Fatalf("the built-in page was not served: status %d", page.Code)
	}
	for _, header := range []string{"ETag", "Cache-Control", "Content-Security-Policy"} {
		if page.Header().Get(header) == "" {
			t.Errorf("the built-in page has no %s header", header)
		}
	}
	for _, module := range []string{"/utils.js", "/node.js", "/unassigned.js"} {
		if rr := get(builtIn, module); rr.Code != http.StatusOK || rr.Header().Get("ETag") == "" {
			t.Errorf("GET %s: status %d, ETag %q", module, rr.Code, rr.Header().Get("ETag"))
		}
	}

	// The libraries and fonts come from the binary too. Their types matter:
	// with nosniff set, a browser will not run a module or apply a stylesheet
	// that is served as anything else.
	vendored := map[string]string{
		"/vendor/vue/vue.esm-browser.prod.js":                    "text/javascript",
		"/vendor/vueuse/core/index.mjs":                          "text/javascript",
		"/vendor/vuetify/vuetify.min.css":                        "text/css",
		"/vendor/roboto/files/roboto-latin-400-normal.woff2":     "font/woff2",
		"/vendor/mdi/fonts/materialdesignicons-webfont.woff2":    "font/woff2",
		"/vendor/mdi/css/materialdesignicons.min.css?v=anything": "text/css",
	}
	for path, wantType := range vendored {
		rr := get(builtIn, path)
		if rr.Code != http.StatusOK || rr.Header().Get("ETag") == "" {
			t.Errorf("GET %s: status %d, ETag %q", path, rr.Code, rr.Header().Get("ETag"))
		}
		if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, wantType) {
			t.Errorf("GET %s: Content-Type %q, want %s", path, got, wantType)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>from disk</html>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fromDisk := get(staticHandler(dir), "/")
	if fromDisk.Body.String() != "<html>from disk</html>" {
		t.Errorf("STATIC_DIR was not served: %q", fromDisk.Body.String())
	}
	if fromDisk.Header().Get("Cache-Control") != "no-cache" || fromDisk.Header().Get("Last-Modified") == "" {
		t.Errorf("files from disk should be revalidated by modification time: %v", fromDisk.Header())
	}
}

func TestPagePolicy(t *testing.T) {
	policy := func(host string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rr := httptest.NewRecorder()
		pagePolicy(http.NotFoundHandler()).ServeHTTP(rr, req)
		return rr.Header().Get("Content-Security-Policy")
	}

	csp := policy("dashboard.example.com:8080")
	// Everything the page loads is served with it, so no other site is named.
	if strings.Contains(csp, "https://") || strings.Contains(csp, "http://") {
		t.Errorf("policy %q names another site", csp)
	}
	for _, want := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"connect-src 'self' ws://dashboard.example.com:8080 wss://dashboard.example.com:8080",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy %q does not contain %q", csp, want)
		}
	}
	if got := policy("[2001:db8::1]:8080"); !strings.Contains(got, "ws://[2001:db8::1]:8080") {
		t.Errorf("an IPv6 host was not allowed its WebSocket: %q", got)
	}

	// The Host header is the client's to write. One that is not plainly a host
	// must not be copied into the policy, where it could add sources of its own.
	for _, host := range []string{"evil.example.com; script-src *", "a b", "evil.example.com/x", "*", ""} {
		got := policy(host)
		if !strings.Contains(got, "connect-src 'self';") {
			t.Errorf("Host %q changed the policy: %q", host, got)
		}
		if strings.Contains(got, "script-src *") || strings.Count(got, "script-src") != 1 {
			t.Errorf("Host %q added to the policy: %q", host, got)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	securityHeaders(healthzHandler(func() bool { return true })).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("the wrapped handler did not run: %q", rr.Body.String())
	}
}
