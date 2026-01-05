package config

import (
	"crypto/rsa"
	"log"
	"os"
	"strings"
)

type Config struct {
	ClusterName        string
	ContextRoot        string
	ListenerPort       string
	AuthEnabled        bool
	OAuthConfig        OAuthConfig
	SensitiveDataPaths []string
	HideAllConfigs     bool
	HideAllEnvs        bool
	HideAllMounts      bool
	HideAllSecrets     bool
	HideLabels         []string
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
	UsernameClaim    string
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

	clientSecret := ""
	clientSecretFile := os.Getenv("OIDC_CLIENT_SECRET_FILE")
	if clientSecretFile != "" {
		clientSecretBytes, err := os.ReadFile(clientSecretFile)
		if err != nil {
			log.Fatal(err)
		}
		clientSecret = string(clientSecretBytes)
	}

	sensitiveDataPaths := []string{
		"nodes.*.Description.Engine.Plugins",
		"nodes.*.Description.TLSInfo",
		"services.*.Spec.TaskTemplate.Placement.Platforms", // Although not sensitive, this can be very verbose
		"services.*.Spec.TaskTemplate.ContainerSpec.Mounts.*.Source",
		"tasks.*.Spec.Placement.Platforms", // Although not sensitive, this can be very verbose
		"tasks.*.Spec.ContainerSpec.Mounts.*.Source",
	}

	if sensitiveDataPathsEnv := os.Getenv("SENSITIVE_DATA_PATHS"); sensitiveDataPathsEnv != "" {
		sensitiveDataPaths = append(sensitiveDataPaths, strings.Split(sensitiveDataPathsEnv, ",")...)
	}

	return &Config{
		ClusterName:  os.Getenv("CLUSTER_NAME"),
		ContextRoot:  contextRoot,
		ListenerPort: getEnv("LISTENER_PORT", defaultListenerPort),
		AuthEnabled:  authEnabled,
		OAuthConfig: OAuthConfig{
			ClientID:         os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret:     strings.TrimSpace(clientSecret),
			RedirectURL:      os.Getenv("OIDC_REDIRECT_URL"),
			Scopes:           strings.Split(os.Getenv("OIDC_SCOPES"), ","),
			AuthURL:          os.Getenv("OIDC_AUTH_URL"),
			TokenURL:         os.Getenv("OIDC_TOKEN_URL"),
			OIDCWellKnownURL: os.Getenv("OIDC_WELL_KNOWN_URL"),
			RsaPublicKeyMap:  make(map[string]*rsa.PublicKey),
			UsernameClaim:    getEnv("OIDC_USERNAME_CLAIM", "preferred_username"),
		},
		HideAllConfigs:     os.Getenv("HIDE_ALL_CONFIGS") == "true",
		HideAllEnvs:        os.Getenv("HIDE_ALL_ENVS") == "true",
		HideAllMounts:      os.Getenv("HIDE_ALL_MOUNTS") == "true",
		HideAllSecrets:     os.Getenv("HIDE_ALL_SECRETS") == "true",
		HideLabels:         strings.Split(os.Getenv("HIDE_ALL_LABELS"), ","),
		SensitiveDataPaths: sensitiveDataPaths,
	}
}

// Create function to print out config for debugging purposes
func (c *Config) PrintConfig() {
	log.Printf("Cluster Name: %s", c.ClusterName)
	log.Printf("Context Root: %s", c.ContextRoot)
	log.Printf("Listener Port: %s", c.ListenerPort)
	log.Printf("Auth Enabled: %t", c.AuthEnabled)
	log.Printf("Sensitive Data Paths: %v", c.SensitiveDataPaths)
	log.Printf("OAuth Client ID: %s", c.OAuthConfig.ClientID)
	log.Printf("OAuth Redirect URL: %s", c.OAuthConfig.RedirectURL)
	log.Printf("OAuth Scopes: %v", c.OAuthConfig.Scopes)
	log.Printf("OAuth Auth URL: %s", c.OAuthConfig.AuthURL)
	log.Printf("OAuth Token URL: %s", c.OAuthConfig.TokenURL)
	log.Printf("OAuth OIDC Well Known URL: %s", c.OAuthConfig.OIDCWellKnownURL)
	log.Printf("OAuth Username Claim: %s", c.OAuthConfig.UsernameClaim)
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
