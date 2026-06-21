package docker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

func clientCount(h *Hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// setKeepalive overrides the ping/pong timings for the duration of a test.
func setKeepalive(t *testing.T, ping, pong time.Duration) {
	t.Helper()
	origPing, origPong := pingPeriodNanos.Load(), pongWaitNanos.Load()
	pingPeriodNanos.Store(int64(ping))
	pongWaitNanos.Store(int64(pong))
	t.Cleanup(func() {
		pingPeriodNanos.Store(origPing)
		pongWaitNanos.Store(origPong)
	})
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestKeepalive_ReapsUnresponsivePeer verifies that a client which never answers
// pings trips the read deadline and is unregistered, freeing its slot. A client
// that does not read also never auto-replies to pings, which simulates a peer
// that has silently vanished.
func TestKeepalive_ReapsUnresponsivePeer(t *testing.T) {
	setKeepalive(t, 50*time.Millisecond, 250*time.Millisecond)

	h := newHub(&config.Config{ContextRoot: "/"})
	srv := httptest.NewServer(http.HandlerFunc(h.handleConnections))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// Deliberately never read from conn: the client never auto-replies to the
	// server's pings, so the server should reap it after pongWait.

	if !waitFor(t, func() bool { return clientCount(h) == 1 }, 2*time.Second) {
		t.Fatalf("client never registered (count=%d)", clientCount(h))
	}
	if !waitFor(t, func() bool { return clientCount(h) == 0 }, 3*time.Second) {
		t.Fatalf("unresponsive client was not reaped (count=%d)", clientCount(h))
	}
}

// TestKeepalive_ResponsivePeerStaysConnected verifies that a client which reads
// (and therefore auto-replies to pings) is not reaped.
func TestKeepalive_ResponsivePeerStaysConnected(t *testing.T) {
	// Keep pongWait generously larger than pingPeriod so frequent pongs hold the
	// deadline open even under the scheduling jitter of the race detector.
	setKeepalive(t, 50*time.Millisecond, 600*time.Millisecond)

	h := newHub(&config.Config{ContextRoot: "/"})
	srv := httptest.NewServer(http.HandlerFunc(h.handleConnections))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read continuously so gorilla auto-responds to pings with pongs.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	if !waitFor(t, func() bool { return clientCount(h) == 1 }, 2*time.Second) {
		t.Fatalf("client never registered (count=%d)", clientCount(h))
	}
	// Past a full pongWait window (many ping/pong cycles), a responsive client
	// must still be connected.
	time.Sleep(pongWait() + 200*time.Millisecond)
	if got := clientCount(h); got != 1 {
		t.Fatalf("responsive client should stay connected, count=%d", got)
	}

	conn.Close()
	<-done
}
