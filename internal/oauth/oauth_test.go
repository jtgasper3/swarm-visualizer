package oauth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

func mustParseCIDR(s string) *net.IPNet {
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return cidr
}

func TestClientIP(t *testing.T) {
	trustedProxy := mustParseCIDR("10.0.0.0/8")

	tests := []struct {
		name           string
		remoteAddr     string
		xRealIP        string
		xForwardedFor  string
		trustedProxies []*net.IPNet
		want           string
	}{
		{
			name:       "no trusted proxies uses RemoteAddr",
			remoteAddr: "1.2.3.4:5678",
			xRealIP:    "9.9.9.9",
			want:       "1.2.3.4",
		},
		{
			name:           "trusted proxy with X-Real-IP",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "203.0.113.5",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.5",
		},
		{
			name:           "trusted proxy with X-Forwarded-For single entry",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.10",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.10",
		},
		{
			name:           "trusted hops on the right of X-Forwarded-For are skipped",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.10, 10.5.6.7, 10.5.6.8",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.10",
		},
		{
			// The client sent "X-Forwarded-For: 1.2.3.4"; the proxy appended
			// the address the request really came from.
			name:           "entries the client supplied are not believed",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "1.2.3.4, 203.0.113.9",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.9",
		},
		{
			name:           "a forged trusted address on the left changes nothing",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "10.9.9.9, 203.0.113.9, 10.5.6.7",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.9",
		},
		{
			name:           "all entries trusted returns where the request started",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "10.7.7.7, 10.5.6.7",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "10.7.7.7",
		},
		{
			name:           "an entry with a port is keyed by its address",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.9:41000",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.9",
		},
		{
			name:           "IPv6 entries",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "2001:db8::1, 10.5.6.7",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "2001:db8::1",
		},
		{
			name:           "empty entries are ignored",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.9, , ",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.9",
		},
		{
			// A proxy that appends to X-Forwarded-For may pass X-Real-IP
			// through untouched, so the client could have written it.
			name:           "X-Forwarded-For is preferred over X-Real-IP",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "1.2.3.4",
			xForwardedFor:  "203.0.113.10",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.10",
		},
		{
			name:           "untrusted remote ignores headers",
			remoteAddr:     "5.5.5.5:1234",
			xRealIP:        "9.9.9.9",
			xForwardedFor:  "8.8.8.8",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "5.5.5.5",
		},
		{
			name:           "trusted proxy with no headers returns RemoteAddr host",
			remoteAddr:     "10.1.2.3:5678",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "10.1.2.3",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:           "X-Real-IP header with whitespace is trimmed",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "  203.0.113.5  ",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xRealIP != "" {
				req.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			got := clientIP(req, tc.trustedProxies)
			if got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A request can carry X-Forwarded-For on more than one line; a proxy that adds
// its own line rather than appending to the client's must not be fooled either.
func TestClientIP_MultipleForwardedLines(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:5678"
	req.Header.Add("X-Forwarded-For", "1.2.3.4")
	req.Header.Add("X-Forwarded-For", "203.0.113.9")

	if got := clientIP(req, []*net.IPNet{mustParseCIDR("10.0.0.0/8")}); got != "203.0.113.9" {
		t.Errorf("clientIP() = %q, want %q", got, "203.0.113.9")
	}
}

func TestRegisterOAuthHandlersUsesDiscoveredEndpoints(t *testing.T) {
	_, cert := rsaX5c(t)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwksBody(t, testJWK{kid: "rsa1", x5c: []string{cert}})))
	}))
	defer jwks.Close()

	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer":"https://issuer.example.com",
			"authorization_endpoint":"https://issuer.example.com/auth",
			"token_endpoint":"https://issuer.example.com/token",
			"jwks_uri":"` + jwks.URL + `"
		}`))
	}))
	defer discovery.Close()

	cfg := &config.Config{
		ContextRoot: "/",
		AuthEnabled: true,
		OAuthConfig: config.OAuthConfig{
			ClientID:         "client-id",
			RedirectURL:      "https://app.example.com/callback",
			OIDCWellKnownURL: discovery.URL,
		},
	}

	mux := http.NewServeMux()
	auth := RegisterOAuthHandlers(mux, cfg)
	if auth == nil {
		t.Fatal("expected an Authenticator when auth is enabled")
	}

	if auth.oauthConfig.Endpoint.AuthURL != "https://issuer.example.com/auth" {
		t.Fatalf("AuthURL = %q, want discovered endpoint", auth.oauthConfig.Endpoint.AuthURL)
	}
	if auth.oauthConfig.Endpoint.TokenURL != "https://issuer.example.com/token" {
		t.Fatalf("TokenURL = %q, want discovered endpoint", auth.oauthConfig.Endpoint.TokenURL)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://issuer.example.com/auth?") {
		t.Fatalf("login redirect Location = %q, want discovered authorization endpoint", loc)
	}
}
