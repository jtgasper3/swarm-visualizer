package docker

import (
	"context"
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
	// Registered second, so it runs first: the hub lets go of its clients and
	// stops its fan-out, where the test started one.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		h.Close(ctx)
	})
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

// TestWS_FailedUpgradeWritesOneResponse verifies that a request which is not a
// WebSocket handshake gets the upgrader's error response and nothing after it.
func TestWS_FailedUpgradeWritesOneResponse(t *testing.T) {
	h := newHub(&config.Config{ContextRoot: "/"}, nil)

	rec := httptest.NewRecorder()
	h.handleConnections(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); body != "Bad Request\n" {
		t.Errorf("body = %q, want only the upgrader's message", body)
	}
}

// TestWS_OversizedMessageIsRefused verifies that the server does not buffer
// whatever a client chooses to send: a message over the limit ends the
// connection, and the client's slot is freed.
func TestWS_OversizedMessageIsRefused(t *testing.T) {
	h := newHub(&config.Config{ContextRoot: "/"}, nil)
	_, wsURL := wsServer(t, h)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if !waitFor(t, func() bool { return clientCount(h) == 1 }, 2*time.Second) {
		t.Fatal("client was not registered")
	}

	// A small message is read and discarded.
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write small message: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if c := clientCount(h); c != 1 {
		t.Fatalf("a small message must not end the connection, count=%d", c)
	}

	// The server hangs up as soon as it has read the frame's length, which can
	// be before this write has finished, so the write itself may fail.
	_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, 64*1024))
	if !waitFor(t, func() bool { return clientCount(h) == 0 }, 2*time.Second) {
		t.Fatal("expected the client to be dropped after an oversized message")
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("expected the connection to be closed after an oversized message")
	}
}

// TestWS_CloseTellsClientsTheServerIsGoingAway verifies the shutdown path: each
// client gets a close frame instead of a dropped connection, and nobody new is
// let in.
func TestWS_CloseTellsClientsTheServerIsGoingAway(t *testing.T) {
	h := newHub(&config.Config{ContextRoot: "/"}, nil)
	_, wsURL := wsServer(t, h)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if !waitFor(t, func() bool { return clientCount(h) == 1 }, 2*time.Second) {
		t.Fatal("client was not registered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h.Close(ctx)
	if ctx.Err() != nil {
		t.Fatal("Close did not finish writing the closing frames in time")
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Errorf("read after Close: %v, want a close with code %d", err, websocket.CloseGoingAway)
	}
	if c := clientCount(h); c != 0 {
		t.Errorf("clients after Close = %d, want 0", c)
	}

	// A connection attempted after Close is turned away.
	late, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		defer late.Close()
		late.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := late.ReadMessage(); err == nil {
			t.Error("a client connecting after Close was served")
		}
	}
	if c := clientCount(h); c != 0 {
		t.Errorf("clients after a late connection = %d, want 0", c)
	}
}
