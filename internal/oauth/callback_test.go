package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"golang.org/x/oauth2"
)

func cookieValues(rr *httptest.ResponseRecorder) map[string]string {
	cookies := map[string]string{}
	for _, c := range rr.Result().Cookies() {
		cookies[c.Name] = c.Value
	}
	return cookies
}

// assertFlowCookiesCleared requires a deletion for each cookie the login set.
// Looking only at the cookies a response happens to carry would pass just as
// well for a response that cleared nothing.
func assertFlowCookiesCleared(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	deleted := map[string]bool{}
	for _, c := range rr.Result().Cookies() {
		if c.MaxAge < 0 && c.Value == "" && c.Path == "/" {
			deleted[c.Name] = true
		}
	}
	for _, name := range []string{"state", "nonce", verifierCookie} {
		if !deleted[name] {
			t.Errorf("flow cookie %q was not cleared", name)
		}
	}
}

func TestHandleLogin_SetsFlowCookies(t *testing.T) {
	a := &Authenticator{
		cfg: &config.Config{ContextRoot: "/"},
		oauthConfig: &oauth2.Config{
			ClientID:    "client-1",
			RedirectURL: "https://app.example.com/callback",
			Endpoint:    oauth2.Endpoint{AuthURL: "https://issuer.example.com/auth"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()

	a.handleLogin(rr, req)

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

	cookies := cookieValues(rr)
	if cookies["state"] != stateParam {
		t.Errorf("state cookie %q != state param %q", cookies["state"], stateParam)
	}
	if cookies["nonce"] != nonceParam {
		t.Errorf("nonce cookie %q != nonce param %q", cookies["nonce"], nonceParam)
	}

	// PKCE: the URL carries the S256 challenge of the verifier in the cookie,
	// and never the verifier itself.
	verifier := cookies[verifierCookie]
	if verifier == "" {
		t.Fatal("expected a PKCE verifier cookie")
	}
	if got := loc.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	sum := sha256.Sum256([]byte(verifier))
	if got, want := loc.Query().Get("code_challenge"), base64.RawURLEncoding.EncodeToString(sum[:]); got != want {
		t.Errorf("code_challenge = %q, want the S256 hash of the verifier %q", got, want)
	}
	if strings.Contains(loc.RawQuery, verifier) {
		t.Error("the verifier must not appear in the authorization URL")
	}
}

// callbackFixture is an Authenticator wired to a stand-in token endpoint.
type callbackFixture struct {
	a   *Authenticator
	key *rsa.PrivateKey

	mu sync.Mutex
	// tokenForm is the form the token endpoint last received.
	tokenForm url.Values
	// respond overrides the token endpoint's response when set.
	respond http.HandlerFunc
	// extraClaims are added to the ID token the endpoint issues.
	extraClaims jwt.MapClaims
	// tokenLifetime is how long the issued ID token is valid for.
	tokenLifetime time.Duration
}

const (
	testKid    = "kid1"
	testIssuer = "https://issuer.example.com"
	testClient = "client-1"
)

func newCallbackFixture(t *testing.T) *callbackFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	f := &callbackFixture{key: key, tokenLifetime: time.Hour}

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.tokenForm = r.PostForm
		respond := f.respond
		f.mu.Unlock()
		if respond != nil {
			respond(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","id_token":"` + f.idToken(t, "nonce-abc") + `"}`))
	}))
	t.Cleanup(tokenSrv.Close)

	f.a = &Authenticator{
		cfg: &config.Config{ContextRoot: "/", OAuthConfig: config.OAuthConfig{
			ClientID: testClient, Issuer: testIssuer, UsernameClaim: "preferred_username", SessionMaxAge: 3600,
		}},
		keys:       &keyStore{keys: map[string]*rsa.PublicKey{testKid: &key.PublicKey}, lastRefresh: time.Now()},
		sessionKey: []byte("test-session-key"),
		oauthConfig: &oauth2.Config{
			ClientID: testClient,
			Endpoint: oauth2.Endpoint{TokenURL: tokenSrv.URL, AuthURL: testIssuer + "/auth"},
		},
	}
	return f
}

func (f *callbackFixture) idToken(t *testing.T, nonce string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":                testIssuer,
		"aud":                testClient,
		"sub":                "user-1",
		"preferred_username": "alice",
		"exp":                time.Now().Add(f.tokenLifetime).Unix(),
		"iat":                time.Now().Unix(),
		"nonce":              nonce,
	}
	for k, v := range f.extraClaims {
		claims[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKid
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	return s
}

// callback runs the callback as the browser would after a login that set the
// given nonce cookie. An empty verifier leaves the PKCE cookie out.
func (f *callbackFixture) callback(cookieNonce, verifier string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "state", Value: "xyz"})
	req.AddCookie(&http.Cookie{Name: "nonce", Value: cookieNonce})
	if verifier != "" {
		req.AddCookie(&http.Cookie{Name: verifierCookie, Value: verifier})
	}
	rr := httptest.NewRecorder()
	f.a.handleCallback(rr, req)
	return rr
}

func TestHandleCallback_NonceValidation(t *testing.T) {
	f := newCallbackFixture(t)

	t.Run("matching nonce establishes the session", func(t *testing.T) {
		rr := f.callback("nonce-abc", "verifier-1")
		if rr.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want redirect; body=%s", rr.Code, rr.Body.String())
		}
		session := cookieValues(rr)[sessionCookie]
		if session == "" {
			t.Fatal("expected a session cookie to be set")
		}

		// The session authenticates the next request.
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		claims, err := f.a.ValidateToken(req)
		if err != nil {
			t.Fatalf("session was not accepted: %v", err)
		}
		if claims["preferred_username"] != "alice" {
			t.Errorf("username = %v, want alice", claims["preferred_username"])
		}

		// The flow cookies are spent.
		assertFlowCookiesCleared(t, rr)
	})

	t.Run("mismatched nonce is rejected", func(t *testing.T) {
		rr := f.callback("nonce-different", "verifier-1")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		if v := cookieValues(rr)[sessionCookie]; v != "" {
			t.Fatalf("a session must not be set on nonce mismatch, got %q", v)
		}
		assertFlowCookiesCleared(t, rr)
	})

	t.Run("mismatched state is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=from-somewhere-else", nil)
		req.AddCookie(&http.Cookie{Name: "state", Value: "xyz"})
		req.AddCookie(&http.Cookie{Name: "nonce", Value: "nonce-abc"})
		req.AddCookie(&http.Cookie{Name: verifierCookie, Value: "verifier-1"})
		rr := httptest.NewRecorder()
		f.a.handleCallback(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		assertFlowCookiesCleared(t, rr)
	})
}

func TestHandleCallback_PKCE(t *testing.T) {
	f := newCallbackFixture(t)

	t.Run("the verifier from the cookie is sent to the token endpoint", func(t *testing.T) {
		if rr := f.callback("nonce-abc", "verifier-from-login"); rr.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want redirect; body=%s", rr.Code, rr.Body.String())
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if got := f.tokenForm.Get("code_verifier"); got != "verifier-from-login" {
			t.Errorf("code_verifier = %q, want the cookie's value", got)
		}
		if got := f.tokenForm.Get("code"); got != "abc" {
			t.Errorf("code = %q, want abc", got)
		}
	})

	t.Run("a callback without the verifier cookie is refused", func(t *testing.T) {
		f.mu.Lock()
		f.tokenForm = nil
		f.mu.Unlock()

		rr := f.callback("nonce-abc", "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		assertFlowCookiesCleared(t, rr)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.tokenForm != nil {
			t.Error("the code must not be redeemed without a verifier")
		}
	})
}

// The identity provider's error response can describe its configuration; it
// belongs in the log, not on the page.
func TestHandleCallback_ExchangeErrorIsNotEchoed(t *testing.T) {
	f := newCallbackFixture(t)
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"secret for client-1 at realm internal-prod is wrong"}`))
	}

	rr := f.callback("nonce-abc", "verifier-1")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	body := rr.Body.String()
	for _, leaked := range []string{"invalid_client", "internal-prod", "secret for"} {
		if strings.Contains(body, leaked) {
			t.Errorf("response body %q repeats the identity provider's error (%q)", body, leaked)
		}
	}
	if v := cookieValues(rr)[sessionCookie]; v != "" {
		t.Errorf("a session must not be set when the exchange fails, got %q", v)
	}
	assertFlowCookiesCleared(t, rr)
}

// An ID token with many group claims is larger than a browser will store in
// one cookie. The session set in its place has to stay small whatever the
// token holds.
func TestHandleCallback_SessionCookieStaysSmall(t *testing.T) {
	f := newCallbackFixture(t)
	groups := make([]string, 400)
	for i := range groups {
		groups[i] = "cn=some-rather-long-group-name-" + strings.Repeat("x", 12) + ",ou=groups,dc=example,dc=com"
	}
	f.extraClaims = jwt.MapClaims{"groups": groups}

	if size := len(f.idToken(t, "nonce-abc")); size < 8192 {
		t.Fatalf("the ID token in this test is only %d bytes; it should be well over a cookie's limit", size)
	}

	rr := f.callback("nonce-abc", "verifier-1")
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want redirect; body=%s", rr.Code, rr.Body.String())
	}
	for _, header := range rr.Result().Header.Values("Set-Cookie") {
		if len(header) > 1024 {
			t.Errorf("a Set-Cookie header is %d bytes: %.80s...", len(header), header)
		}
	}
	if cookieValues(rr)[sessionCookie] == "" {
		t.Fatal("expected a session cookie to be set")
	}
}

// An identity provider that issued an ID token with no expiry would be handing
// out something valid for good. The login is refused rather than given a
// session.
func TestHandleCallback_RejectsTokenWithoutExpiry(t *testing.T) {
	f := newCallbackFixture(t)
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": testIssuer, "aud": testClient, "sub": "user-1", "nonce": "nonce-abc", "iat": time.Now().Unix(),
		})
		tok.Header["kid"] = testKid
		idToken, err := tok.SignedString(f.key)
		if err != nil {
			t.Errorf("sign id_token: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","id_token":"` + idToken + `"}`))
	}

	rr := f.callback("nonce-abc", "verifier-1")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if v := cookieValues(rr)[sessionCookie]; v != "" {
		t.Errorf("a session was issued for a token with no expiry: %q", v)
	}
	assertFlowCookiesCleared(t, rr)
}

// A session lasts until the ID token would have expired or for SessionMaxAge,
// whichever is shorter, as it did when the cookie held the token itself.
func TestHandleCallback_SessionLifetime(t *testing.T) {
	tests := []struct {
		name          string
		tokenLifetime time.Duration
		maxAge        int
		want          time.Duration
	}{
		{"token outlives the session limit", 8 * time.Hour, 3600, time.Hour},
		{"token expires first", 5 * time.Minute, 3600, 5 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCallbackFixture(t)
			f.tokenLifetime = tc.tokenLifetime
			f.a.cfg.OAuthConfig.SessionMaxAge = tc.maxAge

			rr := f.callback("nonce-abc", "verifier-1")
			var session *http.Cookie
			for _, c := range rr.Result().Cookies() {
				if c.Name == sessionCookie {
					session = c
				}
			}
			if session == nil {
				t.Fatalf("expected a session cookie; status=%d body=%s", rr.Code, rr.Body.String())
			}

			if got := time.Duration(session.MaxAge) * time.Second; got < tc.want-5*time.Second || got > tc.want {
				t.Errorf("cookie Max-Age = %v, want about %v", got, tc.want)
			}
			claims, err := f.a.validateSession(session.Value)
			if err != nil {
				t.Fatalf("validate session: %v", err)
			}
			expires, _ := claims.GetExpirationTime()
			if got := time.Until(expires.Time); got < tc.want-5*time.Second || got > tc.want {
				t.Errorf("session expires in %v, want about %v", got, tc.want)
			}
			if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode {
				t.Errorf("session cookie attributes: HttpOnly=%v Secure=%v SameSite=%v", session.HttpOnly, session.Secure, session.SameSite)
			}
		})
	}
}

func TestHandleLogout_ClearsSession(t *testing.T) {
	a := &Authenticator{cfg: &config.Config{ContextRoot: "/"}}
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	rr := httptest.NewRecorder()

	a.handleLogout(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.Value == "" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected the session cookie to be cleared")
	}
}
