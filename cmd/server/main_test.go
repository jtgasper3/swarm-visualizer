package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzHandler(t *testing.T) {
	tests := []struct {
		name  string
		ready bool
		want  int
	}{
		{name: "ready returns 200", ready: true, want: http.StatusOK},
		{name: "not ready returns 503", ready: false, want: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := healthzHandler(func() bool { return tc.ready })
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
			if tc.ready && rr.Body.String() != "ok" {
				t.Fatalf("body = %q, want %q", rr.Body.String(), "ok")
			}
		})
	}
}
