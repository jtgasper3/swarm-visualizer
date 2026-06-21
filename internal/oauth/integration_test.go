package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"golang.org/x/oauth2"
)

// newTestAuthenticator builds an Authenticator wired for handler tests, without
// the network discovery that NewAuthenticator performs.
func newTestAuthenticator() *Authenticator {
	return &Authenticator{
		cfg: &config.Config{ContextRoot: "/"},
		oauthConfig: &oauth2.Config{
			ClientID: "client-1",
			Endpoint: oauth2.Endpoint{AuthURL: "https://idp.example.com/auth"},
		},
		limiters: make(map[string]*ipLimiter),
	}
}

// TestOAuthRoutes_ThroughMux exercises the registered login and logout routes
// end to end via a ServeMux.
func TestOAuthRoutes_ThroughMux(t *testing.T) {
	a := newTestAuthenticator()
	mux := http.NewServeMux()
	a.register(mux)

	// /login redirects to the IdP authorize endpoint.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "1.2.3.4:1111"
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("login status = %d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "https://idp.example.com/auth?") {
		t.Fatalf("login Location = %q, want IdP authorize URL", loc)
	}

	// /logout clears the session cookie.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.RemoteAddr = "1.2.3.4:1111"
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("logout status = %d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "id_token" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the id_token cookie")
	}
}

// TestOAuthRateLimit verifies the per-IP rate limiter on the auth endpoints
// returns 429 once the burst is exceeded.
func TestOAuthRateLimit(t *testing.T) {
	a := newTestAuthenticator()
	mux := http.NewServeMux()
	a.register(mux)

	got429 := false
	for i := 0; i < 8; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = "9.9.9.9:2222"
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected a 429 after exceeding the burst limit")
	}
}
