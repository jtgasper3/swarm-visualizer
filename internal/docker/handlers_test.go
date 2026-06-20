package docker

import (
	"testing"
	"time"
)

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
