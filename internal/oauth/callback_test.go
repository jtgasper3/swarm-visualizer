package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"golang.org/x/oauth2"
)

func TestHandleLogin_SetsStateAndNonce(t *testing.T) {
	orig := oauthConfig
	t.Cleanup(func() { oauthConfig = orig })
	oauthConfig = &oauth2.Config{
		ClientID:    "client-1",
		RedirectURL: "https://app.example.com/callback",
		Endpoint:    oauth2.Endpoint{AuthURL: "https://issuer.example.com/auth"},
	}

	cfg := &config.Config{ContextRoot: "/"}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()

	handleLogin(cfg, rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	stateParam := loc.Query().Get("state")
	nonceParam := loc.Query().Get("nonce")
	if stateParam == "" || nonceParam == "" {
		t.Fatalf("expected state and nonce in auth URL, got %q", loc.RawQuery)
	}

	cookies := map[string]string{}
	for _, c := range rr.Result().Cookies() {
		cookies[c.Name] = c.Value
	}
	if cookies["state"] != stateParam {
		t.Errorf("state cookie %q != state param %q", cookies["state"], stateParam)
	}
	if cookies["nonce"] != nonceParam {
		t.Errorf("nonce cookie %q != nonce param %q", cookies["nonce"], nonceParam)
	}
}

func TestHandleCallback_NonceValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid, issuer, client = "kid1", "https://issuer.example.com", "client-1"

	origKeys, origCfg := signingKeys, oauthConfig
	t.Cleanup(func() { signingKeys = origKeys; oauthConfig = origCfg })
	signingKeys = &keyStore{
		keys:        map[string]*rsa.PublicKey{kid: &key.PublicKey},
		lastRefresh: time.Now(),
	}

	signIDToken := func(nonce string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss":   issuer,
			"aud":   client,
			"sub":   "user-1",
			"exp":   time.Now().Add(time.Hour).Unix(),
			"iat":   time.Now().Unix(),
			"nonce": nonce,
		})
		tok.Header["kid"] = kid
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatalf("sign id_token: %v", err)
		}
		return s
	}

	cfg := &config.Config{ContextRoot: "/", OAuthConfig: config.OAuthConfig{ClientID: client, Issuer: issuer}}

	doCallback := func(t *testing.T, tokenNonce, cookieNonce string) *httptest.ResponseRecorder {
		idToken := signIDToken(tokenNonce)
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","id_token":"` + idToken + `"}`))
		}))
		defer tokenSrv.Close()

		oauthConfig = &oauth2.Config{
			ClientID: client,
			Endpoint: oauth2.Endpoint{TokenURL: tokenSrv.URL, AuthURL: "https://issuer.example.com/auth"},
		}

		req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=xyz", nil)
		req.AddCookie(&http.Cookie{Name: "state", Value: "xyz"})
		req.AddCookie(&http.Cookie{Name: "nonce", Value: cookieNonce})
		rr := httptest.NewRecorder()
		handleCallback(cfg, rr, req)
		return rr
	}

	idTokenCookie := func(rr *httptest.ResponseRecorder) string {
		for _, c := range rr.Result().Cookies() {
			if c.Name == "id_token" {
				return c.Value
			}
		}
		return ""
	}

	t.Run("matching nonce establishes the session", func(t *testing.T) {
		rr := doCallback(t, "nonce-abc", "nonce-abc")
		if rr.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want redirect; body=%s", rr.Code, rr.Body.String())
		}
		if idTokenCookie(rr) == "" {
			t.Fatal("expected id_token cookie to be set")
		}
	})

	t.Run("mismatched nonce is rejected", func(t *testing.T) {
		rr := doCallback(t, "nonce-abc", "nonce-different")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		if v := idTokenCookie(rr); v != "" {
			t.Fatalf("id_token must not be set on nonce mismatch, got %q", v)
		}
	})
}

func TestHandleLogout_ClearsSession(t *testing.T) {
	cfg := &config.Config{ContextRoot: "/"}
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	rr := httptest.NewRecorder()

	handleLogout(cfg, rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "id_token" && c.Value == "" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected id_token cookie to be cleared")
	}
}
