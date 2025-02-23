package oauth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"golang.org/x/oauth2"
)

var oauthConfig *oauth2.Config

func RegisterOAuthHandlers(cfg *config.Config) {
	if cfg.AuthEnabled {
		oauthConfig = setupOAuthConfig(&cfg.OAuthConfig)

		// Fetch and parse the well-known oidc config
		err := fetchWellKnownOIDCConfig(cfg)
		if err != nil {
			log.Fatalf("Failed to fetch JWKS: %v", err)
		}

		http.HandleFunc("/login", handleLogin)
		http.HandleFunc("/callback", handleCallback)
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

func handleLogin(w http.ResponseWriter, r *http.Request) {
	url := oauthConfig.AuthCodeURL("randomstate")
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
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

	http.SetCookie(w, &http.Cookie{
		Name:     "id_token",
		Value:    rawIDToken,
		MaxAge:   3600,
		Path:     "/",
		HttpOnly: true,
	})
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func fetchWellKnownOIDCConfig(cfg *config.Config) error {
	wellKnownURL := cfg.OAuthConfig.OIDCWellKnownURL
	resp, err := http.Get(wellKnownURL)
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

	jwksResp, err := http.Get(config.JWKSURI)
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
