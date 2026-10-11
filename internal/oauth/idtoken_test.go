package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

func TestValidateToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	const (
		kid    = "test-kid"
		issuer = "https://issuer.example.com"
		client = "client-123"
	)

	// keyStore seeded with our test key. lastRefresh is recent so an unknown kid
	// does not trigger a (network) refresh.
	keys := &keyStore{
		keys:        map[string]*rsa.PublicKey{kid: &key.PublicKey},
		lastRefresh: time.Now(),
	}

	validClaims := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": issuer,
			"aud": client,
			"sub": "user-1",
			"exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
	}

	signRS256 := func(claims jwt.MapClaims, signKey *rsa.PrivateKey, hdrKid string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = hdrKid
		s, err := tok.SignedString(signKey)
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		return s
	}

	// alg:none token — must be rejected by the RSA-only key function.
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
	noneTok.Header["kid"] = kid
	noneStr, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}

	// HMAC token — an algorithm-confusion attempt, also rejected.
	hmacTok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	hmacTok.Header["kid"] = kid
	hmacStr, err := hmacTok.SignedString([]byte("symmetric-secret"))
	if err != nil {
		t.Fatalf("sign hmac token: %v", err)
	}

	expiredClaims := validClaims()
	expiredClaims["exp"] = time.Now().Add(-time.Hour).Unix()

	wrongAudClaims := validClaims()
	wrongAudClaims["aud"] = "someone-else"

	wrongIssClaims := validClaims()
	wrongIssClaims["iss"] = "https://evil.example.com"

	defaultCfg := &config.Config{OAuthConfig: config.OAuthConfig{ClientID: client, Issuer: issuer}}

	noExpClaims := validClaims()
	delete(noExpClaims, "exp")

	zeroExpClaims := validClaims()
	zeroExpClaims["exp"] = 0

	noIssClaims := validClaims()
	delete(noIssClaims, "iss")

	tests := []struct {
		name    string
		token   string
		cookie  bool
		noToken bool
		cfg     *config.Config
		wantErr bool
	}{
		{name: "valid token via bearer header", token: signRS256(validClaims(), key, kid)},
		// The cookie holds a session, which an ID token is not.
		{name: "ID token in the session cookie is rejected", token: signRS256(validClaims(), key, kid), cookie: true, wantErr: true},
		{name: "issuer not configured rejects every token", token: signRS256(noIssClaims, key, kid), cfg: &config.Config{OAuthConfig: config.OAuthConfig{ClientID: client}}, wantErr: true},
		{name: "token without an issuer", token: signRS256(noIssClaims, key, kid), wantErr: true},
		{name: "expired token", token: signRS256(expiredClaims, key, kid), wantErr: true},
		// Correctly signed, for this client, from the right issuer, and with
		// nothing to say when it stops being valid.
		{name: "token without an expiry", token: signRS256(noExpClaims, key, kid), wantErr: true},
		{name: "token with a zero expiry", token: signRS256(zeroExpClaims, key, kid), wantErr: true},
		{name: "wrong audience", token: signRS256(wrongAudClaims, key, kid), wantErr: true},
		{name: "wrong issuer", token: signRS256(wrongIssClaims, key, kid), wantErr: true},
		{name: "unknown kid", token: signRS256(validClaims(), key, "other-kid"), wantErr: true},
		{name: "wrong signing key", token: signRS256(validClaims(), otherKey, kid), wantErr: true},
		{name: "alg none rejected", token: noneStr, wantErr: true},
		{name: "hmac signature rejected", token: hmacStr, wantErr: true},
		{name: "no token", noToken: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			if cfg == nil {
				cfg = defaultCfg
			}

			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			if !tc.noToken {
				if tc.cookie {
					req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.token})
				} else {
					req.Header.Set("Authorization", "Bearer "+tc.token)
				}
			}

			a := &Authenticator{cfg: cfg, keys: keys, sessionKey: []byte("test-session-key")}
			claims, err := a.ValidateToken(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (claims=%v)", claims)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if claims == nil {
				t.Fatal("expected claims, got nil")
			}
			if sub, _ := claims.GetSubject(); sub != "user-1" {
				t.Errorf("sub = %q, want user-1", sub)
			}
		})
	}
}
