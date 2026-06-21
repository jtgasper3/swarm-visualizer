package docker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

// bearerValidator accepts only "Bearer good".
func bearerValidator(r *http.Request) (jwt.MapClaims, error) {
	if r.Header.Get("Authorization") == "Bearer good" {
		return jwt.MapClaims{"sub": "alice"}, nil
	}
	return nil, fmt.Errorf("no valid token")
}

func wsServer(t *testing.T, h *Hub) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.handleConnections))
	t.Cleanup(srv.Close)
	return srv, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

// TestWS_AuthRejectsUnauthorized verifies that, with auth enabled, a connection
// without a valid token receives the in-band 401 message and is not registered.
func TestWS_AuthRejectsUnauthorized(t *testing.T) {
	cfg := &config.Config{ContextRoot: "/", AuthEnabled: true, OAuthConfig: config.OAuthConfig{UsernameClaim: "sub"}}
	h := newHub(cfg, bearerValidator)
	_, wsURL := wsServer(t, h)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil) // no Authorization header
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected a 401 message, got read error: %v", err)
	}
	if string(msg) != "401-Unauthorized" {
		t.Fatalf("got %q, want 401-Unauthorized", msg)
	}
	if c := clientCount(h); c != 0 {
		t.Fatalf("unauthorized client must not be registered, count=%d", c)
	}
}

// TestWS_AuthAcceptsAndDelivers verifies that an authorized connection is
// registered and receives the current snapshot.
func TestWS_AuthAcceptsAndDelivers(t *testing.T) {
	cfg := &config.Config{ContextRoot: "/", AuthEnabled: true, OAuthConfig: config.OAuthConfig{UsernameClaim: "sub"}}
	h := newHub(cfg, bearerValidator)
	go h.runBroadcasts()
	_, wsURL := wsServer(t, h)

	// Publish a frame and wait until it has been fanned out, so a newly
	// connecting client is seeded with it.
	frame := []byte(`{"clusterName":"x"}`)
	h.Publish(frame)
	if !waitFor(t, h.Ready, time.Second) {
		t.Fatal("frame was never fanned out")
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer good")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("authorized client got no frame: %v", err)
	}
	if string(msg) != string(frame) {
		t.Fatalf("got %q, want %q", msg, frame)
	}
}
