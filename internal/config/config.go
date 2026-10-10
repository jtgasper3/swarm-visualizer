package config

import (
	"fmt"
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
	authEnabled := getEnvBool("ENABLE_AUTHN")

	// ServeMux reads a pattern with no leading slash as a host name, which
	// would leave every route unreachable, so normalize both ends.
	contextRoot := getEnv("CONTEXT_ROOT", defaultContextRoot)
	if !strings.HasPrefix(contextRoot, "/") {
		contextRoot = "/" + contextRoot
	}
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
		HideAllConfigs:     getEnvBool("HIDE_ALL_CONFIGS"),
		HideAllEnvs:        getEnvBool("HIDE_ALL_ENVS"),
		HideAllMounts:      getEnvBool("HIDE_ALL_MOUNTS"),
		HideAllSecrets:     getEnvBool("HIDE_ALL_SECRETS"),
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

// getEnvBool reads a boolean setting; unset or empty means false. A value that
// is not a recognizable boolean stops the server instead of counting as false:
// these settings turn on authentication and redaction, and a slip such as
// ENABLE_AUTHN=yes must not quietly leave them off.
func getEnvBool(key string) bool {
	value, err := parseBool(os.Getenv(key))
	if err != nil {
		log.Fatalf("Invalid %s: %v", key, err)
	}
	return value
}

func parseBool(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return false, fmt.Errorf("%q is not a boolean; use true or false", value)
	}
	return parsed, nil
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
