package config

import (
	"crypto/rsa"
	"os"
	"strings"
)

type Config struct {
	ClusterName  string
	ContextRoot  string
	ListenerPort string
	AuthEnabled  bool
	OAuthConfig  OAuthConfig
}

type OAuthConfig struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	Scopes           []string
	AuthURL          string
	TokenURL         string
	OIDCWellKnownURL string
	RsaPublicKeyMap  map[string]*rsa.PublicKey
}

const (
	defaultContextRoot  = "/"
	defaultListenerPort = "8080"
)

func LoadConfig() *Config {
	authEnabled := os.Getenv("ENABLE_AUTHN") == "true"

	contextRoot := getEnv("CONTEXT_ROOT", defaultContextRoot)
	if !strings.HasSuffix(contextRoot, "/") {
		contextRoot += "/"
	}

	return &Config{
		ClusterName:  os.Getenv("CLUSTER_NAME"),
		ContextRoot:  contextRoot,
		ListenerPort: getEnv("LISTENER_PORT", defaultListenerPort),
		AuthEnabled:  authEnabled,
		OAuthConfig: OAuthConfig{
			ClientID:         os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret:     os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURL:      os.Getenv("OIDC_REDIRECT_URL"),
			Scopes:           strings.Split(os.Getenv("OIDC_SCOPES"), ","),
			AuthURL:          os.Getenv("OIDC_AUTH_URL"),
			TokenURL:         os.Getenv("OIDC_TOKEN_URL"),
			OIDCWellKnownURL: os.Getenv("OIDC_WELL_KNOWN_URL"),
			RsaPublicKeyMap:  make(map[string]*rsa.PublicKey),
		},
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
