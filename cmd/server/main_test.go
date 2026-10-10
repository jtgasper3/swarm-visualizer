package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
