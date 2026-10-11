package oauth

import (
	"encoding/json"
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

func TestCheckConfig(t *testing.T) {
	valid := config.OAuthConfig{
		ClientID:         "client-id",
		RedirectURL:      "https://app.example.com/visualizer/callback",
		OIDCWellKnownURL: "https://idp.example.com/.well-known/openid-configuration",
	}
	if err := checkConfig(&valid); err != nil {
		t.Fatalf("unexpected error for a complete configuration: %v", err)
	}

	tests := []struct {
		name   string
		change func(*config.OAuthConfig)
		want   []string
	}{
		{"no client id", func(c *config.OAuthConfig) { c.ClientID = "" }, []string{"OIDC_CLIENT_ID is not set"}},
		{"no well-known URL", func(c *config.OAuthConfig) { c.OIDCWellKnownURL = "" }, []string{"OIDC_WELL_KNOWN_URL is not set"}},
		{"no redirect URL", func(c *config.OAuthConfig) { c.RedirectURL = "" }, []string{"OIDC_REDIRECT_URL is not set"}},
		{"relative redirect URL", func(c *config.OAuthConfig) { c.RedirectURL = "/callback" }, []string{`OIDC_REDIRECT_URL "/callback" is not an absolute URL`}},
		{"redirect URL without a scheme", func(c *config.OAuthConfig) { c.RedirectURL = "app.example.com/callback" }, []string{"is not an absolute URL"}},
		{"nothing set", func(c *config.OAuthConfig) { *c = config.OAuthConfig{} },
			[]string{"OIDC_CLIENT_ID is not set", "OIDC_WELL_KNOWN_URL is not set", "OIDC_REDIRECT_URL is not set"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.change(&cfg)
			err := checkConfig(&cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestWithOpenID(t *testing.T) {
	tests := []struct {
		scopes []string
		want   string
	}{
		{nil, "openid"},
		{[]string{"profile", "email"}, "openid profile email"},
		{[]string{"openid", "profile"}, "openid profile"},
		{[]string{"profile", "openid"}, "profile openid"},
	}
	for _, tc := range tests {
		if got := strings.Join(withOpenID(tc.scopes), " "); got != tc.want {
			t.Errorf("withOpenID(%v) = %q, want %q", tc.scopes, got, tc.want)
		}
	}
}

// TestNewAuthenticator_Discovery covers what the well-known document has to
// provide before the server will start with authentication on.
func TestNewAuthenticator_Discovery(t *testing.T) {
	_, cert := rsaX5c(t)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jwksBody(t, testJWK{kid: "rsa1", x5c: []string{cert}})))
	}))
	defer jwks.Close()

	complete := map[string]string{
		"issuer":                 "https://issuer.example.com",
		"authorization_endpoint": "https://issuer.example.com/auth",
		"token_endpoint":         "https://issuer.example.com/token",
		"jwks_uri":               jwks.URL,
	}
	without := func(key string) map[string]string {
		doc := map[string]string{}
		for k, v := range complete {
			if k != key {
				doc[k] = v
			}
		}
		return doc
	}

	tests := []struct {
		name     string
		status   int
		document map[string]string
		authURL  string
		tokenURL string
		wantErr  string
	}{
		{name: "complete document", status: http.StatusOK, document: complete},
		{name: "error status", status: http.StatusInternalServerError, document: map[string]string{"error": "boom"}, wantErr: "returned 500"},
		// An error page can be valid JSON, and a 404 often is.
		{name: "not found", status: http.StatusNotFound, document: complete, wantErr: "returned 404"},
		{name: "no issuer", status: http.StatusOK, document: without("issuer"), wantErr: "provided no issuer"},
		{name: "no jwks_uri", status: http.StatusOK, document: without("jwks_uri"), wantErr: "provided no jwks_uri"},
		{name: "no authorization endpoint", status: http.StatusOK, document: without("authorization_endpoint"), wantErr: "no authorization_endpoint and OIDC_AUTH_URL is not set"},
		{name: "authorization endpoint from the environment", status: http.StatusOK, document: without("authorization_endpoint"), authURL: "https://override.example.com/auth"},
		{name: "no token endpoint", status: http.StatusOK, document: without("token_endpoint"), wantErr: "no token_endpoint and OIDC_TOKEN_URL is not set"},
		{name: "token endpoint from the environment", status: http.StatusOK, document: without("token_endpoint"), tokenURL: "https://override.example.com/token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.document)
			}))
			defer discovery.Close()

			cfg := &config.Config{ContextRoot: "/", AuthEnabled: true, OAuthConfig: config.OAuthConfig{
				ClientID:         "client-id",
				ClientSecret:     "client-secret",
				RedirectURL:      "https://app.example.com/callback",
				OIDCWellKnownURL: discovery.URL,
				AuthURL:          tc.authURL,
				TokenURL:         tc.tokenURL,
			}}

			auth, err := NewAuthenticator(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg.OAuthConfig.Issuer != "https://issuer.example.com" {
					t.Errorf("Issuer = %q, want the discovered issuer", cfg.OAuthConfig.Issuer)
				}
				if len(auth.sessionKey) == 0 {
					t.Error("expected a session key")
				}
				// No scopes were configured; the login still has to ask for an ID token.
				if got := strings.Join(auth.oauthConfig.Scopes, " "); got != "openid" {
					t.Errorf("requested scopes = %q, want openid", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewAuthenticator_RejectsIncompleteConfig(t *testing.T) {
	_, err := NewAuthenticator(&config.Config{AuthEnabled: true})
	if err == nil || !strings.Contains(err.Error(), "OIDC_CLIENT_ID is not set") {
		t.Fatalf("error = %v, want the missing settings named", err)
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
	auth := RegisterOAuthHandlers(t.Context(), mux, cfg)
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
