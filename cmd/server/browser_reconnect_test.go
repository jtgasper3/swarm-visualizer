package main

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// relay passes connections on to a server until it is told to cut them, which
// is what a reverse proxy restarting, or a network dropping out, looks like
// from a browser.
type relay struct {
	URL string

	listener net.Listener
	backend  string

	mu    sync.Mutex
	cut   bool
	conns map[net.Conn]struct{}
}

func startRelay(t *testing.T, serverURL string) *relay {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	r := &relay{
		URL:      "http://" + listener.Addr().String(),
		listener: listener,
		backend:  strings.TrimPrefix(serverURL, "http://"),
		conns:    map[net.Conn]struct{}{},
	}
	go r.accept()
	t.Cleanup(func() {
		_ = listener.Close()
		r.Cut()
	})
	return r
}

func (r *relay) accept() {
	for {
		from, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.pass(from)
	}
}

// pass copies between a client and the server until either side stops.
func (r *relay) pass(from net.Conn) {
	defer from.Close()
	if !r.track(from) {
		return
	}
	defer r.forget(from)

	to, err := net.Dial("tcp", r.backend)
	if err != nil {
		return
	}
	defer to.Close()
	if !r.track(to) {
		return
	}
	defer r.forget(to)

	done := make(chan struct{}, 2)
	for _, way := range []struct{ dst, src net.Conn }{{to, from}, {from, to}} {
		go func() {
			_, _ = io.Copy(way.dst, way.src)
			done <- struct{}{}
		}()
	}
	// When one direction ends, the deferred closes end the other.
	<-done
}

// track notes a connection so that Cut can close it. It reports false, having
// noted nothing, while the relay is cut.
func (r *relay) track(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cut {
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

func (r *relay) forget(conn net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, conn)
}

// Cut drops every connection and refuses new ones until Restore.
func (r *relay) Cut() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cut = true
	for conn := range r.conns {
		_ = conn.Close()
	}
}

// Restore lets connections through again.
func (r *relay) Restore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cut = false
}

// The connection to the server drops, and the swarm changes while the page
// cannot hear about it. The page has to say that it has lost touch, get back
// in touch by itself, and then show the swarm as it is now, not as it was.
func TestDashboardInABrowser_Reconnects(t *testing.T) {
	p := startBrowser(t)
	server := startDashboard(t, "swarm")
	link := startRelay(t, server.URL)

	// The page says how its connection is in two places: the dot beside the
	// cluster's name, and a notice while the connection is lost.
	const (
		dot    = `document.querySelector('.v-app-bar [role="status"]')?.getAttribute('aria-label')`
		notice = `[...document.querySelectorAll('.v-snackbar--active')].some(bar => bar.innerText.includes('Connection lost'))`
	)

	p.open(t, link.URL+"/")
	p.waitForCards(t, 4)
	p.waitFor(t, dot+` === 'Connection: connected'`)
	var shown bool
	if p.eval(t, notice, &shown); shown {
		t.Fatal("the page says the connection is lost before it has been")
	}

	link.Cut()
	p.waitFor(t, notice)
	p.waitFor(t, dot+` === 'Connection: reconnecting'`)
	// What it last knew stays on screen.
	p.waitForCards(t, 4)

	server.ShowSwarm("empty")
	link.Restore()

	// The page tries again after two seconds. The server may take a few more
	// to read the new swarm, and the page is sent it when it does.
	p.waitFor(t, dot+` === 'Connection: connected'`)
	p.waitFor(t, `!`+notice)
	p.waitFor(t, `document.querySelectorAll('.server-card').length === 1 && document.querySelector('.server-card').innerText.includes('solo-01')`)

	// A dropped connection is an error and the browser says so. Nothing else
	// should have gone wrong.
	for _, problem := range p.reported() {
		if !strings.Contains(problem, "WebSocket") {
			t.Errorf("the page reported: %s", problem)
		}
	}
	if banner := p.text(t, ".v-alert"); banner != "" {
		t.Errorf("the page shows an error banner: %s", banner)
	}
}
