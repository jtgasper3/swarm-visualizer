package config

import (
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ClusterName        string
	ContextRoot        string
	ListenerPort       string
	AuthEnabled        bool
	OAuthConfig        OAuthConfig
	TrustedProxies     []*net.IPNet
	SensitiveDataPaths []string
	HideAllConfigs     bool
	HideAllEnvs        bool
	HideAllMounts      bool
	HideAllSecrets     bool
	HideLabels         []string
	MaxWSConnections   int
}

type OAuthConfig struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	Scopes           []string
	AuthURL          string
	TokenURL         string
	OIDCWellKnownURL string
	Issuer           string
	UsernameClaim    string
	SessionMaxAge    int
}

const (
	defaultContextRoot      = "/"
	defaultListenerPort     = "8080"
	defaultSessionMaxAge    = 3600
	defaultMaxWSConnections = 256
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

	sensitiveDataPaths = append(sensitiveDataPaths, splitList(os.Getenv("SENSITIVE_DATA_PATHS"))...)

	sessionMaxAge := defaultSessionMaxAge
	if s := os.Getenv("OIDC_SESSION_MAX_AGE"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			sessionMaxAge = v
		} else {
			log.Printf("Warning: invalid OIDC_SESSION_MAX_AGE %q, using default %d", s, defaultSessionMaxAge)
		}
	}

	maxWSConnections := defaultMaxWSConnections
	if s := os.Getenv("MAX_WS_CONNECTIONS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			maxWSConnections = v
		} else {
			log.Printf("Warning: invalid MAX_WS_CONNECTIONS %q, using default %d", s, defaultMaxWSConnections)
		}
	}

	var trustedProxies []*net.IPNet
	if tp := os.Getenv("TRUSTED_PROXIES"); tp != "" {
		for _, entry := range strings.Split(tp, ",") {
			entry = strings.TrimSpace(entry)
			if !strings.Contains(entry, "/") {
				ip := net.ParseIP(entry)
				if ip == nil {
					log.Printf("Warning: invalid trusted proxy address %q, skipping", entry)
					continue
				}
				if ip.To4() != nil {
					entry += "/32"
				} else {
					entry += "/128"
				}
			}
			_, cidr, err := net.ParseCIDR(entry)
			if err != nil {
				log.Printf("Warning: invalid trusted proxy CIDR %q: %v, skipping", entry, err)
				continue
			}
			trustedProxies = append(trustedProxies, cidr)
		}
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
			Scopes:           splitList(os.Getenv("OIDC_SCOPES")),
			AuthURL:          os.Getenv("OIDC_AUTH_URL"),
			TokenURL:         os.Getenv("OIDC_TOKEN_URL"),
			OIDCWellKnownURL: os.Getenv("OIDC_WELL_KNOWN_URL"),
			UsernameClaim:    getEnv("OIDC_USERNAME_CLAIM", "preferred_username"),
			SessionMaxAge:    sessionMaxAge,
		},
		TrustedProxies:     trustedProxies,
		HideAllConfigs:     os.Getenv("HIDE_ALL_CONFIGS") == "true",
		HideAllEnvs:        os.Getenv("HIDE_ALL_ENVS") == "true",
		HideAllMounts:      os.Getenv("HIDE_ALL_MOUNTS") == "true",
		HideAllSecrets:     os.Getenv("HIDE_ALL_SECRETS") == "true",
		HideLabels:         splitList(os.Getenv("HIDE_LABELS")),
		SensitiveDataPaths: sensitiveDataPaths,
		MaxWSConnections:   maxWSConnections,
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// splitList parses a comma-separated value into a slice, trimming whitespace
// and dropping empty entries. An empty or all-whitespace input yields nil
// rather than a one-element slice containing an empty string.
func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
