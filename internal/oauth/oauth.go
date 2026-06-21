package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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
	a := &Authenticator{cfg: cfg, limiters: make(map[string]*ipLimiter)}
	if err := a.fetchWellKnownOIDCConfig(); err != nil {
		return nil, err
	}
	a.oauthConfig = setupOAuthConfig(&cfg.OAuthConfig)
	return a, nil
}

// clientIP returns the real client IP. If the direct connection is from a
// trusted proxy, X-Real-IP and X-Forwarded-For headers are consulted instead.
func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if len(trustedProxies) > 0 {
		if remoteIP := net.ParseIP(host); remoteIP != nil {
			for _, cidr := range trustedProxies {
				if cidr.Contains(remoteIP) {
					if ip := r.Header.Get("X-Real-IP"); ip != "" {
						return strings.TrimSpace(ip)
					}
					if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
						return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
					}
					break
				}
			}
		}
	}

	return host
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
	auth.register(mux)
	return auth
}

// register wires the auth endpoints onto mux and starts the limiter cleanup.
func (a *Authenticator) register(mux *http.ServeMux) {
	go a.cleanupLimiters()

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
	// the token's nonce claim in the callback.
	url := a.oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce))
	setFlowCookie(a.cfg, w, "state", state)
	setFlowCookie(a.cfg, w, "nonce", nonce)
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

	code := r.URL.Query().Get("code")
	token, err := a.oauthConfig.Exchange(ctx, code)
	if err != nil {
		clearFlowCookies(a.cfg, w)
		http.Error(w, fmt.Sprintf("Failed to exchange token: %v", err), http.StatusInternalServerError)
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

	http.SetCookie(w, &http.Cookie{
		Name:     "id_token",
		Value:    rawIDToken,
		MaxAge:   a.cfg.OAuthConfig.SessionMaxAge,
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

	var discovery struct {
		Issuer   string `json:"issuer"`
		TokenUrl string `json:"token_endpoint"`
		AuthUrl  string `json:"authorization_endpoint"`
		JWKSURI  string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return fmt.Errorf("failed to decode well-known configuration: %v", err)
	}

	if discovery.Issuer != "" {
		cfg.OAuthConfig.Issuer = discovery.Issuer
	} else {
		log.Printf("Warning: well-known configuration provided no issuer; ID token issuer validation is disabled")
	}

	if cfg.OAuthConfig.AuthURL == "" {
		log.Printf("Using Authorization Endpoint from well-known config %s", discovery.AuthUrl)
		cfg.OAuthConfig.AuthURL = discovery.AuthUrl
	}
	if cfg.OAuthConfig.TokenURL == "" {
		log.Printf("Using Token Endpoint from well-known config %s", discovery.TokenUrl)
		cfg.OAuthConfig.TokenURL = discovery.TokenUrl
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
		Name:     "id_token",
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

// clearFlowCookies expires the state and nonce cookies set during login.
func clearFlowCookies(cfg *config.Config, w http.ResponseWriter) {
	for _, name := range []string{"state", "nonce"} {
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
