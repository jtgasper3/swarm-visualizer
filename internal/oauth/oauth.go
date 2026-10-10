package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

// Authenticator holds the OIDC configuration, JWKS signing keys, and per-IP
// rate limiters for the auth endpoints. One instance backs the running server;
// tests construct their own.
type Authenticator struct {
	cfg         *config.Config
	oauthConfig *oauth2.Config
	keys        *keyStore
	// sessionKey signs the session cookies issued after login.
	sessionKey []byte

	limitersMu sync.Mutex
	limiters   map[string]*ipLimiter
}

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewAuthenticator discovers the OIDC endpoints and JWKS, then builds the
// oauth2 config. It performs network I/O.
func NewAuthenticator(cfg *config.Config) (*Authenticator, error) {
	if err := checkConfig(&cfg.OAuthConfig); err != nil {
		return nil, err
	}
	cfg.OAuthConfig.Scopes = withOpenID(cfg.OAuthConfig.Scopes)

	a := &Authenticator{cfg: cfg, limiters: make(map[string]*ipLimiter)}
	if err := a.fetchWellKnownOIDCConfig(); err != nil {
		return nil, err
	}
	a.oauthConfig = setupOAuthConfig(&cfg.OAuthConfig)

	sessionKey, err := newSessionKey(&cfg.OAuthConfig)
	if err != nil {
		return nil, err
	}
	a.sessionKey = sessionKey
	return a, nil
}

// checkConfig reports every OIDC setting that is missing or unusable, so that
// a misconfiguration stops the server with one message that names them all
// instead of surfacing later as a login that cannot complete.
func checkConfig(cfg *config.OAuthConfig) error {
	var problems []string
	if cfg.ClientID == "" {
		problems = append(problems, "OIDC_CLIENT_ID is not set")
	}
	if cfg.OIDCWellKnownURL == "" {
		problems = append(problems, "OIDC_WELL_KNOWN_URL is not set")
	}
	if cfg.RedirectURL == "" {
		problems = append(problems, "OIDC_REDIRECT_URL is not set")
	} else if u, err := url.Parse(cfg.RedirectURL); err != nil || !u.IsAbs() || u.Host == "" {
		problems = append(problems, fmt.Sprintf("OIDC_REDIRECT_URL %q is not an absolute URL", cfg.RedirectURL))
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// withOpenID makes sure the openid scope is requested. Without it the identity
// provider treats the login as plain OAuth and returns no ID token, which
// leaves the callback with nothing to establish a session from.
func withOpenID(scopes []string) []string {
	if slices.Contains(scopes, "openid") {
		return scopes
	}
	log.Printf("OIDC_SCOPES does not include openid; adding it")
	return append([]string{"openid"}, scopes...)
}

// clientIP returns the address a request is rate-limited by. The forwarding
// headers are consulted only when the direct connection comes from a trusted
// proxy.
//
// X-Forwarded-For is read from the right. Each proxy appends the address it
// received the request from, so the right-most entry that is not itself a
// trusted proxy was written by a proxy we trust, while everything to its left
// arrived from the client and can say anything. X-Real-IP is used only when
// X-Forwarded-For has no entries, for a proxy configured to send it alone.
func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	peer := hostOnly(r.RemoteAddr)
	if !isTrustedProxy(peer, trustedProxies) {
		return peer
	}

	// A request can carry the header more than once; the lines read as one list.
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	client := ""
	for i := len(forwarded) - 1; i >= 0; i-- {
		entry := hostOnly(strings.TrimSpace(forwarded[i]))
		if entry == "" {
			continue
		}
		// If every entry is a trusted proxy, this ends on the left-most one,
		// which is where the request started.
		client = entry
		if !isTrustedProxy(entry, trustedProxies) {
			break
		}
	}
	if client != "" {
		return client
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	return peer
}

// hostOnly strips the port from an address that has one.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func isTrustedProxy(addr string, trustedProxies []*net.IPNet) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, cidr := range trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// authLimiter returns a rate limiter for the given IP, creating one if needed.
// Allows 5 requests per minute with a burst of 5.
func (a *Authenticator) authLimiter(ip string) *rate.Limiter {
	a.limitersMu.Lock()
	defer a.limitersMu.Unlock()

	l, ok := a.limiters[ip]
	if !ok {
		l = &ipLimiter{limiter: rate.NewLimiter(rate.Every(time.Minute/5), 5)}
		a.limiters[ip] = l
	}
	l.lastSeen = time.Now()
	return l.limiter
}

// cleanupLimiters removes entries not seen in the last 10 minutes.
func (a *Authenticator) cleanupLimiters() {
	for {
		time.Sleep(5 * time.Minute)
		a.limitersMu.Lock()
		for ip, l := range a.limiters {
			if time.Since(l.lastSeen) > 10*time.Minute {
				delete(a.limiters, ip)
			}
		}
		a.limitersMu.Unlock()
	}
}

// RegisterOAuthHandlers builds the Authenticator and wires its endpoints onto
// mux when authentication is enabled. It returns the Authenticator (whose
// ValidateToken the WebSocket handler uses), or nil when auth is disabled.
func RegisterOAuthHandlers(mux *http.ServeMux, cfg *config.Config) *Authenticator {
	if !cfg.AuthEnabled {
		return nil
	}

	auth, err := NewAuthenticator(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize authentication: %v", err)
	}
	go auth.cleanupLimiters()
	auth.register(mux)
	return auth
}

// register wires the auth endpoints onto mux.
func (a *Authenticator) register(mux *http.ServeMux) {
	mux.HandleFunc(a.cfg.ContextRoot+"login", func(w http.ResponseWriter, r *http.Request) {
		if !a.authLimiter(clientIP(r, a.cfg.TrustedProxies)).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		a.handleLogin(w, r)
	})
	mux.HandleFunc(a.cfg.ContextRoot+"callback", func(w http.ResponseWriter, r *http.Request) {
		if !a.authLimiter(clientIP(r, a.cfg.TrustedProxies)).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		a.handleCallback(w, r)
	})
	mux.HandleFunc(a.cfg.ContextRoot+"logout", func(w http.ResponseWriter, r *http.Request) {
		a.handleLogout(w, r)
	})
}

func setupOAuthConfig(cfg *config.OAuthConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		},
	}
}

func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateSecureRandomString(32)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate state: %v", err), http.StatusInternalServerError)
		return
	}
	nonce, err := generateSecureRandomString(32)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate nonce: %v", err), http.StatusInternalServerError)
		return
	}

	// state binds the callback to this browser (CSRF); nonce binds the issued
	// ID token to this login flow (replay protection) and is validated against
	// the token's nonce claim in the callback. The PKCE verifier binds the
	// authorization code to this browser as well: the identity provider only
	// redeems the code for whoever presents the verifier, so a code read off
	// the redirect is of no use without this cookie.
	verifier := oauth2.GenerateVerifier()
	url := a.oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier))
	setFlowCookie(a.cfg, w, "state", state)
	setFlowCookie(a.cfg, w, "nonce", nonce)
	setFlowCookie(a.cfg, w, verifierCookie, verifier)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (a *Authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("state")
	if err != nil || subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		clearFlowCookies(a.cfg, w)
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	// Bound the token exchange so a hung or slow IdP token endpoint cannot tie
	// up the request indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	verifier, err := r.Cookie(verifierCookie)
	if err != nil {
		clearFlowCookies(a.cfg, w)
		http.Error(w, "Missing PKCE verifier", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := a.oauthConfig.Exchange(ctx, code, oauth2.VerifierOption(verifier.Value))
	if err != nil {
		clearFlowCookies(a.cfg, w)
		// The error can quote the identity provider's response, so it goes to
		// the log and not to the browser.
		log.Printf("Callback token exchange failed: %s %v", r.RemoteAddr, err)
		http.Error(w, "Sign-in failed: the identity provider did not issue a token", http.StatusBadGateway)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		clearFlowCookies(a.cfg, w)
		http.Error(w, "No id_token found", http.StatusInternalServerError)
		return
	}

	// Validate the ID token and bind it to this login flow via the nonce, so a
	// token captured or replayed from a different flow cannot be used here.
	claims, err := a.validateRawToken(rawIDToken)
	if err != nil {
		clearFlowCookies(a.cfg, w)
		log.Printf("Callback ID token validation failed: %s %v", r.RemoteAddr, err)
		http.Error(w, "Invalid ID token", http.StatusUnauthorized)
		return
	}
	nonceCookie, err := r.Cookie("nonce")
	if err != nil {
		clearFlowCookies(a.cfg, w)
		http.Error(w, "Missing nonce", http.StatusBadRequest)
		return
	}
	tokenNonce, _ := claims["nonce"].(string)
	if tokenNonce == "" || subtle.ConstantTimeCompare([]byte(tokenNonce), []byte(nonceCookie.Value)) != 1 {
		clearFlowCookies(a.cfg, w)
		log.Printf("Callback nonce mismatch: %s", r.RemoteAddr)
		http.Error(w, "Invalid nonce", http.StatusBadRequest)
		return
	}

	clearFlowCookies(a.cfg, w)

	now := time.Now()
	session, expires, err := a.newSession(claims, now)
	if err != nil {
		log.Printf("Callback could not issue a session: %s %v", r.RemoteAddr, err)
		http.Error(w, "Sign-in failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session,
		MaxAge:   int(expires.Sub(now).Seconds()),
		Path:     a.cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, a.cfg.ContextRoot, http.StatusTemporaryRedirect)
}

func (a *Authenticator) fetchWellKnownOIDCConfig() error {
	cfg := a.cfg
	httpClient := &http.Client{Timeout: 10 * time.Second}

	wellKnownURL := cfg.OAuthConfig.OIDCWellKnownURL
	resp, err := httpClient.Get(wellKnownURL)
	if err != nil {
		return fmt.Errorf("failed to fetch well-known configuration: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch well-known configuration: %s returned %s", wellKnownURL, resp.Status)
	}

	var discovery struct {
		Issuer   string `json:"issuer"`
		TokenUrl string `json:"token_endpoint"`
		AuthUrl  string `json:"authorization_endpoint"`
		JWKSURI  string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadataBytes)).Decode(&discovery); err != nil {
		return fmt.Errorf("failed to decode well-known configuration: %v", err)
	}

	// Every ID token is checked against the issuer, so there has to be one.
	if discovery.Issuer == "" {
		return fmt.Errorf("well-known configuration provided no issuer")
	}
	cfg.OAuthConfig.Issuer = discovery.Issuer

	if cfg.OAuthConfig.AuthURL == "" {
		log.Printf("Using Authorization Endpoint from well-known config %s", discovery.AuthUrl)
		cfg.OAuthConfig.AuthURL = discovery.AuthUrl
	}
	if cfg.OAuthConfig.TokenURL == "" {
		log.Printf("Using Token Endpoint from well-known config %s", discovery.TokenUrl)
		cfg.OAuthConfig.TokenURL = discovery.TokenUrl
	}

	if cfg.OAuthConfig.AuthURL == "" {
		return fmt.Errorf("well-known configuration provided no authorization_endpoint and OIDC_AUTH_URL is not set")
	}
	if cfg.OAuthConfig.TokenURL == "" {
		return fmt.Errorf("well-known configuration provided no token_endpoint and OIDC_TOKEN_URL is not set")
	}

	if discovery.JWKSURI == "" {
		return fmt.Errorf("well-known configuration provided no jwks_uri")
	}

	a.keys = newKeyStore(discovery.JWKSURI, httpClient)
	if err := a.keys.refresh(); err != nil {
		return err
	}
	go a.keys.refreshLoop()

	return nil
}

// handleLogout clears the session cookie and returns the user to the app,
// which will then prompt for login again. This is a local logout; it does not
// terminate the session at the identity provider.
func (a *Authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     a.cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
	http.Redirect(w, r, a.cfg.ContextRoot, http.StatusTemporaryRedirect)
}

// setFlowCookie sets a short-lived cookie used during the OAuth redirect flow.
// SameSite=Lax so it survives the top-level redirect back from the identity
// provider.
func setFlowCookie(cfg *config.Config, w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// verifierCookie holds the PKCE code verifier between login and callback.
const verifierCookie = "pkce_verifier"

// clearFlowCookies expires the cookies set during login.
func clearFlowCookies(cfg *config.Config, w http.ResponseWriter) {
	for _, name := range []string{"state", "nonce", verifierCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     cfg.ContextRoot,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

func generateSecureRandomString(length int) (string, error) {
	// Calculate the number of random bytes needed (base64 encoding expands data by ~33%)
	numBytes := (length*6 + 7) / 8 // Approximation for Base64 encoding
	bytes := make([]byte, numBytes)

	// Read cryptographically secure random bytes
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Encode to Base64 and truncate to the desired length
	randomString := base64.RawURLEncoding.EncodeToString(bytes)
	if len(randomString) > length {
		randomString = randomString[:length]
	}
	return randomString, nil
}
