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
