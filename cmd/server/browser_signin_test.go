package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

// identityProvider is a stand-in OpenID Connect provider. It signs in whoever
// asks without asking them anything, which is why it only exists in a test and
// only listens on loopback. It does hold the server to the protocol: a code is
// good once, and only for the client and redirect URI it was issued to; the
// PKCE verifier has to match the challenge; and the client has to present its
// secret.
//
// It and the server are both plain HTTP on 127.0.0.1. The session cookie is
// marked Secure all the same, and the browser accepts it: Chrome counts
// loopback addresses as secure for that purpose.
type identityProvider struct {
	URL string

	key    *rsa.PrivateKey
	secret string

	mu             sync.Mutex
	grants         map[string]grant
	authorizations []url.Values
	tokens         int
}

// grant is what a code stands for until it is exchanged.
type grant struct{ clientID, redirectURI, nonce, challenge string }

func startIdentityProvider(t *testing.T, clientSecret string) *identityProvider {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	idp := &identityProvider{key: key, secret: clientSecret, grants: map[string]grant{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("/jwks", idp.jwks)
	mux.HandleFunc("/auth", idp.authorize)
	mux.HandleFunc("/token", idp.token)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	idp.URL = srv.URL
	return idp
}

func (idp *identityProvider) discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"issuer":                 idp.URL,
		"authorization_endpoint": idp.URL + "/auth",
		"token_endpoint":         idp.URL + "/token",
		"jwks_uri":               idp.URL + "/jwks",
	})
}

func (idp *identityProvider) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key",
		"n": base64.RawURLEncoding.EncodeToString(idp.key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(idp.key.E)).Bytes()),
	}}})
}

// authorize sends the browser straight back with a code.
func (idp *identityProvider) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	back, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || back.Hostname() != "127.0.0.1" {
		http.Error(w, "redirect_uri must be on loopback", http.StatusBadRequest)
		return
	}

	code := rand.Text()
	idp.mu.Lock()
	idp.authorizations = append(idp.authorizations, query)
	idp.grants[code] = grant{
		clientID: query.Get("client_id"), redirectURI: query.Get("redirect_uri"),
		nonce: query.Get("nonce"), challenge: query.Get("code_challenge"),
	}
	idp.mu.Unlock()

	answer := back.Query()
	answer.Set("code", code)
	answer.Set("state", query.Get("state"))
	back.RawQuery = answer.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// token exchanges a code for an ID token.
func (idp *identityProvider) token(w http.ResponseWriter, r *http.Request) {
	refuse := func(why string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": why})
	}
	if err := r.ParseForm(); err != nil {
		refuse("unreadable request")
		return
	}

	// The client may identify itself in the Authorization header, where the
	// values are form-encoded, or in the form.
	clientID, secret, inHeader := r.BasicAuth()
	if inHeader {
		clientID, _ = url.QueryUnescape(clientID)
		secret, _ = url.QueryUnescape(secret)
	} else {
		clientID, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(idp.secret)) != 1 {
		refuse("wrong client secret")
		return
	}

	idp.mu.Lock()
	granted, known := idp.grants[r.PostForm.Get("code")]
	delete(idp.grants, r.PostForm.Get("code"))
	idp.mu.Unlock()
	if !known {
		refuse("unknown or used code")
		return
	}
	if clientID != granted.clientID || r.PostForm.Get("redirect_uri") != granted.redirectURI {
		refuse("the code was issued to another client or redirect URI")
		return
	}

	verifier := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if granted.challenge == "" || base64.RawURLEncoding.EncodeToString(verifier[:]) != granted.challenge {
		refuse("the PKCE verifier does not match the challenge")
		return
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": idp.URL, "aud": granted.clientID, "sub": "user-1", "preferred_username": "alice",
		"nonce": granted.nonce, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(idp.key)
	if err != nil {
		refuse("could not sign")
		return
	}

	idp.mu.Lock()
	idp.tokens++
	idp.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "unused", "token_type": "Bearer", "id_token": signed})
}

// seen returns the authorization requests so far and how many tokens were
// issued for them.
func (idp *identityProvider) seen() (authorizations []url.Values, tokens int) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return append([]url.Values(nil), idp.authorizations...), idp.tokens
}

// signedIn configures the server to require sign-in through idp.
func signedIn(t *testing.T, idp *identityProvider) settings {
	t.Helper()

	secretFile := filepath.Join(t.TempDir(), "client_secret")
	if err := os.WriteFile(secretFile, []byte(idp.secret), 0o600); err != nil {
		t.Fatalf("write the client secret: %v", err)
	}
	return func(url string) map[string]string {
		return map[string]string{
			"ENABLE_AUTHN":            "true",
			"OIDC_CLIENT_ID":          "swarm-visualizer-test",
			"OIDC_CLIENT_SECRET_FILE": secretFile,
			"OIDC_REDIRECT_URL":       url + "/callback",
			"OIDC_WELL_KNOWN_URL":     idp.URL + "/.well-known/openid-configuration",
		}
	}
}

// Somebody who is not signed in follows a link to a service. The page sends
// them to the identity provider and they come back to the dashboard's bare
// address; the link has to survive that, or it opens the dashboard and nothing
// else.
func TestDashboardInABrowser_SignIn(t *testing.T) {
	p := startBrowser(t)
	idp := startIdentityProvider(t, rand.Text())
	server := startDashboard(t, "swarm", signedIn(t, idp))

	t.Run("the server does require it", func(t *testing.T) {
		// Otherwise everything below would pass with sign-in turned off.
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
		if err != nil {
			t.Fatalf("connect without a session: %v", err)
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, first, err := conn.ReadMessage()
		if err != nil || !strings.HasPrefix(string(first), "401-Unauthorized") {
			t.Fatalf("without a session the server sent %q, %v", first, err)
		}
	})

	const dialog = `(document.querySelector('.v-overlay--active .v-card')?.innerText ?? '').replace(/\s+/g, ' ')`

	t.Run("a link survives signing in", func(t *testing.T) {
		p.open(t, server.URL+"/#/service/svc-web")
		p.waitFor(t, dialog+`.includes('Service: svc-web ')`)
		p.waitForCards(t, 4)

		var at struct{ Origin, Hash, Cookies, Remembered string }
		p.eval(t, `({
			origin: location.origin,
			hash: location.hash,
			cookies: document.cookie,
			remembered: Object.keys(sessionStorage).join(','),
		})`, &at)
		if at.Origin != server.URL || at.Hash != "#/service/svc-web" {
			t.Errorf("ended up at %s%s", at.Origin, at.Hash)
		}
		// The session is not for scripts to read.
		if at.Cookies != "" {
			t.Errorf("the page can read cookies: %s", at.Cookies)
		}
		// The saved link is used once.
		if at.Remembered != "" {
			t.Errorf("left in session storage: %s", at.Remembered)
		}

		authorizations, tokens := idp.seen()
		if len(authorizations) != 1 || tokens != 1 {
			t.Fatalf("the provider saw %d authorization requests and issued %d tokens, want one of each", len(authorizations), tokens)
		}
		request := authorizations[0]
		if request.Get("code_challenge_method") != "S256" || request.Get("code_challenge") == "" {
			t.Errorf("no PKCE challenge in %v", request)
		}
		if !strings.Contains(" "+request.Get("scope")+" ", " openid ") {
			t.Errorf("scope = %q, without openid", request.Get("scope"))
		}
		if request.Get("state") == "" || request.Get("nonce") == "" {
			t.Errorf("no state or nonce in %v", request)
		}
		if got, want := request.Get("redirect_uri"), server.URL+"/callback"; got != want {
			t.Errorf("redirect_uri = %q, want %q", got, want)
		}
	})

	t.Run("signing out ends the session", func(t *testing.T) {
		p.eval(t, `document.querySelector('[aria-label="Log out"]').click()`, nil)

		// The page comes back without a session and is sent to sign in again,
		// which this provider grants at once. That it was asked a second time
		// is what shows the first session had ended.
		deadline := time.Now().Add(15 * time.Second)
		for {
			if authorizations, tokens := idp.seen(); len(authorizations) == 2 && tokens == 2 {
				break
			}
			if time.Now().After(deadline) {
				authorizations, tokens := idp.seen()
				t.Fatalf("after signing out the provider had seen %d authorization requests and issued %d tokens, want two of each", len(authorizations), tokens)
			}
			time.Sleep(50 * time.Millisecond)
		}
		p.waitForCards(t, 4)

		var hash string
		p.eval(t, `location.hash`, &hash)
		if hash != "" {
			t.Errorf("the old link came back after signing out: %s", hash)
		}
	})

	assertNothingWentWrong(t, p)
}
