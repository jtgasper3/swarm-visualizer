package oauth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustParseCIDR(s string) *net.IPNet {
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return cidr
}

func TestClientIP(t *testing.T) {
	trustedProxy := mustParseCIDR("10.0.0.0/8")

	tests := []struct {
		name           string
		remoteAddr     string
		xRealIP        string
		xForwardedFor  string
		trustedProxies []*net.IPNet
		want           string
	}{
		{
			name:       "no trusted proxies uses RemoteAddr",
			remoteAddr: "1.2.3.4:5678",
			xRealIP:    "9.9.9.9",
			want:       "1.2.3.4",
		},
		{
			name:           "trusted proxy with X-Real-IP",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "203.0.113.5",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.5",
		},
		{
			name:           "trusted proxy with X-Forwarded-For single entry",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.10",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.10",
		},
		{
			name:           "trusted proxy with X-Forwarded-For multiple entries returns first",
			remoteAddr:     "10.1.2.3:5678",
			xForwardedFor:  "203.0.113.10, 10.5.6.7",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.10",
		},
		{
			name:           "X-Real-IP preferred over X-Forwarded-For",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "203.0.113.5",
			xForwardedFor:  "203.0.113.10",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.5",
		},
		{
			name:           "untrusted remote ignores headers",
			remoteAddr:     "5.5.5.5:1234",
			xRealIP:        "9.9.9.9",
			xForwardedFor:  "8.8.8.8",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "5.5.5.5",
		},
		{
			name:           "trusted proxy with no headers returns RemoteAddr host",
			remoteAddr:     "10.1.2.3:5678",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "10.1.2.3",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:           "X-Real-IP header with whitespace is trimmed",
			remoteAddr:     "10.1.2.3:5678",
			xRealIP:        "  203.0.113.5  ",
			trustedProxies: []*net.IPNet{trustedProxy},
			want:           "203.0.113.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xRealIP != "" {
				req.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			got := clientIP(req, tc.trustedProxies)
			if got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
