package docker

import (
	"testing"
	"time"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
)

// TestRegisterClient_EnforcesCap verifies the concurrent connection cap.
func TestRegisterClient_EnforcesCap(t *testing.T) {
	h := newHub(&config.Config{MaxWSConnections: 2}, nil)

	c1 := &wsClient{send: make(chan []byte, 1)}
	c2 := &wsClient{send: make(chan []byte, 1)}
	c3 := &wsClient{send: make(chan []byte, 1)}

	if !h.register(c1) || !h.register(c2) {
		t.Fatal("expected the first two clients to register")
	}
	if h.register(c3) {
		t.Fatal("expected the third client to be rejected at capacity")
	}
	if !h.atCapacity() {
		t.Fatal("expected atCapacity to report full")
	}

	// Freeing a slot allows a new client in.
	h.unregister(c1)
	if h.atCapacity() {
		t.Fatal("expected capacity after unregister")
	}
	if !h.register(c3) {
		t.Fatal("expected registration to succeed after a slot freed")
	}
}

// TestRegisterClient_SeedsLatestSnapshot verifies a newly registered client is
// seeded with the most recent snapshot.
func TestRegisterClient_SeedsLatestSnapshot(t *testing.T) {
	h := newHub(&config.Config{}, nil)

	payload := []byte(`{"clusterName":"test"}`)
	h.lastFanned = payload

	c := &wsClient{send: make(chan []byte, 1)}
	h.register(c)

	select {
	case got := <-c.send:
		if string(got) != string(payload) {
			t.Fatalf("got %q, want %q", got, payload)
		}
	default:
		t.Fatal("expected the latest snapshot to be seeded, got none")
	}
}

// TestRegisterClient_NoSeedBeforeFirstSnapshot verifies that a client that
// connects before any data has been produced is not sent a "null" frame.
func TestRegisterClient_NoSeedBeforeFirstSnapshot(t *testing.T) {
	h := newHub(&config.Config{}, nil)

	c := &wsClient{send: make(chan []byte, 1)}
	h.register(c)

	select {
	case got := <-c.send:
		t.Fatalf("expected no seed frame before first snapshot, got %q", got)
	default:
	}
}

// TestEnqueue_EmptyBuffer verifies a message lands in an empty send buffer.
func TestEnqueue_EmptyBuffer(t *testing.T) {
	c := &wsClient{send: make(chan []byte, 1)}
	enqueue(c, []byte("first"))

	select {
	case got := <-c.send:
		if string(got) != "first" {
			t.Fatalf("got %q, want %q", got, "first")
		}
	default:
		t.Fatal("expected a buffered message, got none")
	}
}

// TestEnqueue_LatestWins verifies that enqueuing onto a full buffer discards
// the stale pending frame in favor of the newest one.
func TestEnqueue_LatestWins(t *testing.T) {
	c := &wsClient{send: make(chan []byte, 1)}
	enqueue(c, []byte("stale"))
	enqueue(c, []byte("fresh")) // buffer already full; should replace "stale"

	got := <-c.send
	if string(got) != "fresh" {
		t.Fatalf("got %q, want %q", got, "fresh")
	}

	// Only the latest frame is retained; nothing else is queued.
	select {
	case extra := <-c.send:
		t.Fatalf("expected empty buffer, got %q", extra)
	default:
	}
}

// TestEnqueue_NonBlocking verifies enqueue never blocks, even when the buffer
// is full and no consumer is draining it.
func TestEnqueue_NonBlocking(t *testing.T) {
	c := &wsClient{send: make(chan []byte, 1)}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			enqueue(c, []byte("x"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked with a full buffer and no consumer")
	}
}
