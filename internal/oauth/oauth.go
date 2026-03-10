package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
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

var oauthConfig *oauth2.Config

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	authLimiters   = make(map[string]*ipLimiter)
	authLimitersMu sync.Mutex
)

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

// getAuthLimiter returns a rate limiter for the given IP, creating one if needed.
// Allows 5 requests per minute with a burst of 5.
func getAuthLimiter(ip string) *rate.Limiter {
	authLimitersMu.Lock()
	defer authLimitersMu.Unlock()

	l, ok := authLimiters[ip]
	if !ok {
		l = &ipLimiter{limiter: rate.NewLimiter(rate.Every(time.Minute/5), 5)}
		authLimiters[ip] = l
	}
	l.lastSeen = time.Now()
	return l.limiter
}

// cleanupAuthLimiters removes entries not seen in the last 10 minutes.
func cleanupAuthLimiters() {
	for {
		time.Sleep(5 * time.Minute)
		authLimitersMu.Lock()
		for ip, l := range authLimiters {
			if time.Since(l.lastSeen) > 10*time.Minute {
				delete(authLimiters, ip)
			}
		}
		authLimitersMu.Unlock()
	}
}

func RegisterOAuthHandlers(cfg *config.Config) {
	if cfg.AuthEnabled {
		oauthConfig = setupOAuthConfig(&cfg.OAuthConfig)

		// Fetch and parse the well-known oidc config
		err := fetchWellKnownOIDCConfig(cfg)
		if err != nil {
			log.Fatalf("Failed to fetch JWKS: %v", err)
		}

		go cleanupAuthLimiters()

		http.HandleFunc(cfg.ContextRoot+"login", func(w http.ResponseWriter, r *http.Request) {
			if !getAuthLimiter(clientIP(r, cfg.TrustedProxies)).Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			handleLogin(cfg, w, r)
		})
		http.HandleFunc(cfg.ContextRoot+"callback", func(w http.ResponseWriter, r *http.Request) {
			if !getAuthLimiter(clientIP(r, cfg.TrustedProxies)).Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			handleCallback(cfg, w, r)
		})
	}
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

func handleLogin(cfg *config.Config, w http.ResponseWriter, r *http.Request) {
	state, err := generateSecureRandomString(32)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate state: %v", err), http.StatusInternalServerError)
		return
	}

	url := oauthConfig.AuthCodeURL(state)
	http.SetCookie(w, &http.Cookie{
		Name:     "nonce",
		Value:    state,
		Path:     cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleCallback(cfg *config.Config, w http.ResponseWriter, r *http.Request) {
	nonceCookie, err := r.Cookie("nonce")
	if err != nil || nonceCookie.Value != r.URL.Query().Get("state") {
		clearNonceCookie(cfg, w)
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to exchange token: %v", err), http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token found", http.StatusInternalServerError)
		return
	}

	clearNonceCookie(cfg, w)

	http.SetCookie(w, &http.Cookie{
		Name:     "id_token",
		Value:    rawIDToken,
		MaxAge:   cfg.OAuthConfig.SessionMaxAge,
		Path:     cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, cfg.ContextRoot, http.StatusTemporaryRedirect)
}

func fetchWellKnownOIDCConfig(cfg *config.Config) error {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	wellKnownURL := cfg.OAuthConfig.OIDCWellKnownURL
	resp, err := httpClient.Get(wellKnownURL)
	if err != nil {
		return fmt.Errorf("failed to fetch well-known configuration: %v", err)
	}
	defer resp.Body.Close()

	var config struct {
		TokenUrl string `json:"token_endpoint"`
		AuthUrl  string `json:"authorization_endpoint"`
		JWKSURI  string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return fmt.Errorf("failed to decode well-known configuration: %v", err)
	}

	if cfg.OAuthConfig.AuthURL == "" {
		log.Printf("Using Authorization Endpoint from well-known config %s", config.AuthUrl)
		cfg.OAuthConfig.AuthURL = config.AuthUrl
	}
	if cfg.OAuthConfig.TokenURL == "" {
		log.Printf("Using Token Endpoint from well-known config %s", config.TokenUrl)
		cfg.OAuthConfig.TokenURL = config.TokenUrl
	}

	jwksResp, err := httpClient.Get(config.JWKSURI)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %v", err)
	}
	defer jwksResp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kty string   `json:"kty"`
			Kid string   `json:"kid"`
			Use string   `json:"use"`
			N   string   `json:"n"`
			E   string   `json:"e"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %v", err)
	}

	for _, key := range jwks.Keys {
		if len(key.X5c) == 0 {
			log.Printf("Skipping JWKS key %s: no x5c certificate data", key.Kid)
			continue
		}
		certData, err := base64.StdEncoding.DecodeString(key.X5c[0])
		if err != nil {
			return fmt.Errorf("failed to decode certificate: %v", err)
		}

		cert, err := x509.ParseCertificate(certData)
		if err != nil {
			return fmt.Errorf("failed to parse certificate: %v", err)
		}

		cfg.OAuthConfig.RsaPublicKeyMap[key.Kid] = cert.PublicKey.(*rsa.PublicKey)
	}
	return nil
}

func clearNonceCookie(cfg *config.Config, w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "nonce",
		Value:    "",
		Path:     cfg.ContextRoot,
		HttpOnly: true,
		Secure:   true,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
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
