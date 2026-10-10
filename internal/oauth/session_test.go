package oauth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

func newSessionAuthenticator(key string) *Authenticator {
	return &Authenticator{
		cfg:        &config.Config{OAuthConfig: config.OAuthConfig{ClientID: "client-1", Issuer: "https://issuer.example.com", UsernameClaim: "email", SessionMaxAge: 3600}},
		sessionKey: []byte(key),
	}
}

func TestNewSessionKey(t *testing.T) {
	deployment := config.OAuthConfig{
		ClientSecret: "client-secret",
		Issuer:       "https://issuer.example.com",
		ClientID:     "client-1",
		RedirectURL:  "https://prod.example.com/callback",
	}

	first, err := newSessionKey(&deployment)
	if err != nil {
		t.Fatalf("newSessionKey: %v", err)
	}
	replica := deployment
	second, _ := newSessionKey(&replica)
	if !bytes.Equal(first, second) {
		t.Error("the same configuration must give the same key, or replicas and restarts would not share sessions")
	}
	if len(first) != 32 {
		t.Errorf("key is %d bytes, want 32", len(first))
	}
	if bytes.Contains(first, []byte("client-secret")) {
		t.Error("the key must be derived from the client secret, not contain it")
	}

	// Anything that makes it a different deployment gives a different key,
	// even with the client secret in common.
	others := map[string]func(*config.OAuthConfig){
		"client secret": func(c *config.OAuthConfig) { c.ClientSecret = "another-secret" },
		"issuer":        func(c *config.OAuthConfig) { c.Issuer = "https://other-issuer.example.com" },
		"client ID":     func(c *config.OAuthConfig) { c.ClientID = "client-2" },
		"redirect URL":  func(c *config.OAuthConfig) { c.RedirectURL = "https://dev.example.com/callback" },
	}
	for name, change := range others {
		other := deployment
		change(&other)
		if key, _ := newSessionKey(&other); bytes.Equal(first, key) {
			t.Errorf("a different %s must give a different key", name)
		}
	}

	// The parts are kept apart, so moving text between them changes the key.
	shifted := deployment
	shifted.Issuer, shifted.ClientID = "https://issuer.example.comclient-1", ""
	if key, _ := newSessionKey(&shifted); bytes.Equal(first, key) {
		t.Error("the issuer and client ID must not run together in the derivation")
	}

	// No client secret: a random key, different every time.
	random1, err := newSessionKey(&config.OAuthConfig{Issuer: deployment.Issuer, ClientID: deployment.ClientID})
	if err != nil {
		t.Fatalf("newSessionKey without a secret: %v", err)
	}
	random2, _ := newSessionKey(&config.OAuthConfig{Issuer: deployment.Issuer, ClientID: deployment.ClientID})
	if len(random1) != 32 || bytes.Equal(random1, random2) {
		t.Error("without a client secret the key must be 32 random bytes")
	}
}

// Two deployments can share a client secret, or a whole client registration
// served at two addresses. A session from one must not open the other.
func TestSession_NotAcceptedByAnotherDeployment(t *testing.T) {
	newDeployment := func(redirectURL string) *Authenticator {
		cfg := &config.Config{OAuthConfig: config.OAuthConfig{
			ClientSecret: "shared-secret", Issuer: "https://issuer.example.com", ClientID: "shared-client",
			RedirectURL: redirectURL, UsernameClaim: "email", SessionMaxAge: 3600,
		}}
		key, err := newSessionKey(&cfg.OAuthConfig)
		if err != nil {
			t.Fatalf("newSessionKey: %v", err)
		}
		return &Authenticator{cfg: cfg, sessionKey: key}
	}
	dev := newDeployment("https://dev.example.com/callback")
	prod := newDeployment("https://prod.example.com/callback")
	prodReplica := newDeployment("https://prod.example.com/callback")

	now := time.Now()
	session, _, err := dev.newSession(jwt.MapClaims{"sub": "user-1", "exp": now.Add(time.Hour).Unix()}, now)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if _, err := dev.validateSession(session); err != nil {
		t.Fatalf("the issuing deployment must accept its own session: %v", err)
	}
	if claims, err := prod.validateSession(session); err == nil {
		t.Errorf("a session from another deployment was accepted: %v", claims)
	}

	prodSession, _, _ := prod.newSession(jwt.MapClaims{"sub": "user-1", "exp": now.Add(time.Hour).Unix()}, now)
	if _, err := prodReplica.validateSession(prodSession); err != nil {
		t.Errorf("a replica of the same deployment must accept the session: %v", err)
	}
}

func TestSession_RoundTrip(t *testing.T) {
	a := newSessionAuthenticator("key-1")
	now := time.Now()

	session, expires, err := a.newSession(jwt.MapClaims{
		"sub":    "user-1",
		"email":  "alice@example.com",
		"groups": []string{"admins"},
		"exp":    now.Add(8 * time.Hour).Unix(),
	}, now)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if got := expires.Sub(now); got != time.Hour {
		t.Errorf("session lifetime = %v, want SessionMaxAge (1h)", got)
	}

	claims, err := a.validateSession(session)
	if err != nil {
		t.Fatalf("validateSession: %v", err)
	}
	if sub, _ := claims.GetSubject(); sub != "user-1" {
		t.Errorf("sub = %q, want user-1", sub)
	}
	// The name is returned under the configured claim, as an ID token has it.
	if claims["email"] != "alice@example.com" {
		t.Errorf("email = %v, want alice@example.com", claims["email"])
	}
	// Nothing else from the ID token is carried along.
	if _, ok := claims["groups"]; ok {
		t.Error("the session should not carry the ID token's other claims")
	}
}

func TestSession_Rejected(t *testing.T) {
	a := newSessionAuthenticator("key-1")
	now := time.Now()
	idClaims := jwt.MapClaims{"sub": "user-1", "exp": now.Add(time.Hour).Unix()}

	valid, _, err := a.newSession(idClaims, now)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	expired, _, _ := a.newSession(idClaims, now.Add(-2*time.Hour))
	otherKey, _, _ := newSessionAuthenticator("key-2").newSession(idClaims, now)

	sign := func(method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString(key)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}
	future := now.Add(time.Hour).Unix()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// Flip one character in the payload.
	parts := strings.Split(valid, ".")
	payload := []byte(parts[1])
	payload[5] ^= 1
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	tests := []struct {
		name    string
		session string
	}{
		{"expired", expired},
		{"signed with another key", otherKey},
		{"tampered payload", tampered},
		{"wrong audience", sign(jwt.SigningMethodHS256, a.sessionKey, jwt.MapClaims{"aud": "something-else", "sub": "user-1", "exp": future})},
		{"no audience", sign(jwt.SigningMethodHS256, a.sessionKey, jwt.MapClaims{"sub": "user-1", "exp": future})},
		{"no expiry", sign(jwt.SigningMethodHS256, a.sessionKey, jwt.MapClaims{"aud": sessionAudience, "sub": "user-1"})},
		{"a different HMAC algorithm", sign(jwt.SigningMethodHS512, a.sessionKey, jwt.MapClaims{"aud": sessionAudience, "sub": "user-1", "exp": future})},
		{"an RSA-signed token", sign(jwt.SigningMethodRS256, rsaKey, jwt.MapClaims{"aud": sessionAudience, "sub": "user-1", "exp": future})},
		{"unsigned", sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, jwt.MapClaims{"aud": sessionAudience, "sub": "user-1", "exp": future})},
		{"not a token", "garbage"},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if claims, err := a.validateSession(tc.session); err == nil {
				t.Errorf("expected the session to be rejected, got claims %v", claims)
			}
		})
	}

	if _, err := a.validateSession(valid); err != nil {
		t.Errorf("the untouched session should still be accepted: %v", err)
	}
}

// A session is not an ID token: presenting one as a bearer token must fail,
// just as presenting an ID token in the cookie does.
func TestSession_IsNotABearerToken(t *testing.T) {
	a := newSessionAuthenticator("key-1")
	a.keys = &keyStore{keys: map[string]*rsa.PublicKey{}, lastRefresh: time.Now()}
	now := time.Now()
	session, _, err := a.newSession(jwt.MapClaims{"sub": "user-1", "exp": now.Add(time.Hour).Unix()}, now)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	if claims, err := a.ValidateToken(req); err == nil {
		t.Errorf("a session presented as a bearer token was accepted: %v", claims)
	}
}

func TestSession_NeedsAKey(t *testing.T) {
	a := newSessionAuthenticator("")
	a.sessionKey = nil

	if _, _, err := a.newSession(jwt.MapClaims{"sub": "user-1"}, time.Now()); err == nil {
		t.Error("expected newSession to fail without a key")
	}
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"aud": sessionAudience, "sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte{})
	if _, err := a.validateSession(forged); err == nil {
		t.Error("a session signed with an empty key must not be accepted")
	}
}
