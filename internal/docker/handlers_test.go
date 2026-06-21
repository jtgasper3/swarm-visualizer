package docker

import (
	"testing"
	"time"
)

// resetClientState clears the package-level client registry and seed snapshot
// so a test starts from a known state.
func resetClientState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		clientsMu.Lock()
		clients = make(map[*wsClient]struct{})
		lastFanned = nil
		clientsMu.Unlock()
		maxClients = 0
	})
}

// TestRegisterClient_EnforcesCap verifies the concurrent connection cap.
func TestRegisterClient_EnforcesCap(t *testing.T) {
	resetClientState(t)
	maxClients = 2

	c1 := &wsClient{send: make(chan []byte, 1)}
	c2 := &wsClient{send: make(chan []byte, 1)}
	c3 := &wsClient{send: make(chan []byte, 1)}

	if !registerClient(c1) || !registerClient(c2) {
		t.Fatal("expected the first two clients to register")
	}
	if registerClient(c3) {
		t.Fatal("expected the third client to be rejected at capacity")
	}
	if atClientCapacity() != true {
		t.Fatal("expected atClientCapacity to report full")
	}

	// Freeing a slot allows a new client in.
	unregisterClient(c1)
	if atClientCapacity() {
		t.Fatal("expected capacity after unregister")
	}
	if !registerClient(c3) {
		t.Fatal("expected registration to succeed after a slot freed")
	}
}

// TestRegisterClient_SeedsLatestSnapshot verifies a newly registered client is
// seeded with the most recent snapshot.
func TestRegisterClient_SeedsLatestSnapshot(t *testing.T) {
	resetClientState(t)

	payload := []byte(`{"clusterName":"test"}`)
	lastFanned = payload

	c := &wsClient{send: make(chan []byte, 1)}
	registerClient(c)

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
	resetClientState(t)
	lastFanned = nil

	c := &wsClient{send: make(chan []byte, 1)}
	registerClient(c)

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
