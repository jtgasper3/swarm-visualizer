package config

import (
	"net"
	"testing"
)

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
}

// TestLoadConfig_TrustedProxies verifies TRUSTED_PROXIES parsing.
func TestLoadConfig_TrustedProxies(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		wantLen     int
		wantContain []string // IPs that should be contained
		wantExclude []string // IPs that should NOT be contained
	}{
		{
			name:     "empty env produces no proxies",
			envValue: "",
			wantLen:  0,
		},
		{
			name:        "plain IPv4 accepted",
			envValue:    "10.0.0.1",
			wantLen:     1,
			wantContain: []string{"10.0.0.1"},
			wantExclude: []string{"10.0.0.2"},
		},
		{
			name:        "CIDR range accepted",
			envValue:    "10.0.0.0/8",
			wantLen:     1,
			wantContain: []string{"10.1.2.3", "10.255.255.255"},
			wantExclude: []string{"11.0.0.1"},
		},
		{
			name:        "multiple entries comma separated",
			envValue:    "192.168.1.1,10.0.0.0/24",
			wantLen:     2,
			wantContain: []string{"192.168.1.1", "10.0.0.50"},
			wantExclude: []string{"172.16.0.1"},
		},
		{
			name:        "entries with whitespace are trimmed",
			envValue:    " 10.0.0.1 , 192.168.1.0/24 ",
			wantLen:     2,
			wantContain: []string{"10.0.0.1", "192.168.1.100"},
		},
		{
			name:     "invalid entry is skipped",
			envValue: "not-an-ip,10.0.0.1",
			wantLen:  1,
		},
		{
			name:        "plain IPv6 accepted",
			envValue:    "::1",
			wantLen:     1,
			wantContain: []string{"::1"},
			wantExclude: []string{"::2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "TRUSTED_PROXIES", tc.envValue)

			cfg := LoadConfig()

			if len(cfg.TrustedProxies) != tc.wantLen {
				t.Errorf("TrustedProxies length = %d, want %d", len(cfg.TrustedProxies), tc.wantLen)
			}

			contains := func(ip string) bool {
				parsed := net.ParseIP(ip)
				for _, cidr := range cfg.TrustedProxies {
					if cidr.Contains(parsed) {
						return true
					}
				}
				return false
			}

			for _, ip := range tc.wantContain {
				if !contains(ip) {
					t.Errorf("expected TrustedProxies to contain %s", ip)
				}
			}
			for _, ip := range tc.wantExclude {
				if contains(ip) {
					t.Errorf("expected TrustedProxies to NOT contain %s", ip)
				}
			}
		})
	}
}

// TestSplitList verifies comma-list parsing trims entries and drops empties.
func TestSplitList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty yields nil", input: "", want: nil},
		{name: "whitespace only yields nil", input: "  ,  ,", want: nil},
		{name: "single value", input: "node", want: []string{"node"}},
		{name: "trims and drops empties", input: " node , , service ", want: []string{"node", "service"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitList(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("splitList(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitList(%q) = %#v, want %#v", tc.input, got, tc.want)
				}
			}
		})
	}
}

// TestLoadConfig_SessionMaxAge verifies OIDC_SESSION_MAX_AGE parsing.
func TestLoadConfig_SessionMaxAge(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     int
	}{
		{
			name:     "unset uses default",
			envValue: "",
			want:     defaultSessionMaxAge,
		},
		{
			name:     "valid positive integer",
			envValue: "7200",
			want:     7200,
		},
		{
			name:     "invalid string falls back to default",
			envValue: "notanumber",
			want:     defaultSessionMaxAge,
		},
		{
			name:     "zero falls back to default",
			envValue: "0",
			want:     defaultSessionMaxAge,
		},
		{
			name:     "negative falls back to default",
			envValue: "-60",
			want:     defaultSessionMaxAge,
		},
		{
			name:     "value of 1 is accepted",
			envValue: "1",
			want:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "OIDC_SESSION_MAX_AGE", tc.envValue)

			cfg := LoadConfig()

			if cfg.OAuthConfig.SessionMaxAge != tc.want {
				t.Errorf("SessionMaxAge = %d, want %d", cfg.OAuthConfig.SessionMaxAge, tc.want)
			}
		})
	}
}

// TestLoadConfig_MaxWSConnections verifies MAX_WS_CONNECTIONS parsing.
func TestLoadConfig_MaxWSConnections(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     int
	}{
		{name: "unset uses default", envValue: "", want: defaultMaxWSConnections},
		{name: "valid positive integer", envValue: "50", want: 50},
		{name: "invalid string falls back to default", envValue: "lots", want: defaultMaxWSConnections},
		{name: "zero falls back to default", envValue: "0", want: defaultMaxWSConnections},
		{name: "negative falls back to default", envValue: "-1", want: defaultMaxWSConnections},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "MAX_WS_CONNECTIONS", tc.envValue)

			cfg := LoadConfig()

			if cfg.MaxWSConnections != tc.want {
				t.Errorf("MaxWSConnections = %d, want %d", cfg.MaxWSConnections, tc.want)
			}
		})
	}
}

// TestParseBool verifies which values the boolean settings accept.
func TestParseBool(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{value: "", want: false},
		{value: "   ", want: false},
		{value: "true", want: true},
		{value: "TRUE", want: true},
		{value: "True", want: true},
		{value: "tRuE", want: true},
		{value: " true ", want: true},
		{value: "1", want: true},
		{value: "false", want: false},
		{value: "FALSE", want: false},
		{value: "False", want: false},
		{value: "0", want: false},
		{value: "yes", wantErr: true},
		{value: "on", wantErr: true},
		{value: "enabled", wantErr: true},
		{value: "ture", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parseBool(tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseBool(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("parseBool(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestLoadConfig_Booleans verifies that every boolean setting is read the same
// way, so that none is left off by a value another one would accept.
func TestLoadConfig_Booleans(t *testing.T) {
	flags := map[string]func(*Config) bool{
		"ENABLE_AUTHN":     func(c *Config) bool { return c.AuthEnabled },
		"HIDE_ALL_CONFIGS": func(c *Config) bool { return c.HideAllConfigs },
		"HIDE_ALL_ENVS":    func(c *Config) bool { return c.HideAllEnvs },
		"HIDE_ALL_MOUNTS":  func(c *Config) bool { return c.HideAllMounts },
		"HIDE_ALL_SECRETS": func(c *Config) bool { return c.HideAllSecrets },
	}

	for key, get := range flags {
		for value, want := range map[string]bool{"": false, "false": false, "true": true, "TRUE": true, "1": true} {
			t.Run(key+"="+value, func(t *testing.T) {
				setEnv(t, key, value)

				if got := get(LoadConfig()); got != want {
					t.Errorf("%s=%q read as %v, want %v", key, value, got, want)
				}
			})
		}
	}
}

// TestLoadConfig_ContextRoot verifies CONTEXT_ROOT is normalized to a path
// that starts and ends with a slash.
func TestLoadConfig_ContextRoot(t *testing.T) {
	tests := []struct {
		envValue string
		want     string
	}{
		{envValue: "", want: "/"},
		{envValue: "/", want: "/"},
		{envValue: "/visualizer/", want: "/visualizer/"},
		{envValue: "/visualizer", want: "/visualizer/"},
		{envValue: "visualizer/", want: "/visualizer/"},
		{envValue: "visualizer", want: "/visualizer/"},
	}

	for _, tc := range tests {
		t.Run(tc.envValue, func(t *testing.T) {
			setEnv(t, "CONTEXT_ROOT", tc.envValue)

			if got := LoadConfig().ContextRoot; got != tc.want {
				t.Errorf("ContextRoot = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeContextRoot verifies that a root which a browser would read as
// another host is refused: it is used as a redirect target after login.
func TestNormalizeContextRoot(t *testing.T) {
	for _, root := range []string{"", "/", "visualizer", "/visualizer", "/a/b/"} {
		if _, err := normalizeContextRoot(root); err != nil {
			t.Errorf("normalizeContextRoot(%q): unexpected error: %v", root, err)
		}
	}

	for _, root := range []string{"//", "//example.com", "//example.com/", `/\example.com`, `\example.com`, `/\`} {
		if got, err := normalizeContextRoot(root); err == nil {
			t.Errorf("normalizeContextRoot(%q) = %q, want an error", root, got)
		}
	}
}
