package docker

import (
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/jtgasper3/swarm-visualizer/internal/config"
	"github.com/jtgasper3/swarm-visualizer/internal/oauth"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser clients (curl, native apps)
			}
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}
	clientsMu sync.Mutex
	clients   = make(map[*wsClient]struct{})
	// lastFanned is the most recent frame handed to handleBroadcasts, guarded by
	// clientsMu. A newly registered client is seeded with it (under the same
	// lock) so its seed is never newer than a frame still queued for fan-out,
	// which would otherwise make the client briefly roll back to older state.
	lastFanned []byte
	// maxClients caps the number of concurrent WebSocket connections to bound
	// resource use. 0 means unlimited; it is set from config in
	// RegisterDockerHandlers.
	maxClients int
)

const wsWriteTimeout = 5 * time.Second

// Keepalive timings: a ping is sent every pingPeriod(), and the read side must
// see a pong (or any frame) within pongWait() or the peer is considered dead
// and reaped, freeing its slot against maxClients. pingPeriod must be less than
// pongWait. They are stored atomically (as nanoseconds) so tests can shorten
// them without racing the connection goroutines that read them.
var (
	pingPeriodNanos atomic.Int64
	pongWaitNanos   atomic.Int64
)

func init() {
	pingPeriodNanos.Store(int64(30 * time.Second))
	pongWaitNanos.Store(int64(60 * time.Second))
}

func pingPeriod() time.Duration { return time.Duration(pingPeriodNanos.Load()) }
func pongWait() time.Duration   { return time.Duration(pongWaitNanos.Load()) }

// wsClient is a single WebSocket connection. All writes to conn happen on its
// writePump goroutine. send is a depth-1 buffer holding the latest pending
// snapshot: the broadcaster never blocks on a slow client, and a client that
// falls behind receives the most recent state rather than a backlog of stale
// frames.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

func RegisterDockerHandlers(mux *http.ServeMux, cfg *config.Config) {
	maxClients = cfg.MaxWSConnections

	src, err := newMobySource()
	if err != nil {
		log.Fatal("Docker client error:", err)
	}

	go inspectSwarmServices(cfg, src)
	go handleBroadcasts()

	mux.HandleFunc(cfg.ContextRoot+"ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnections(cfg, w, r)
	})
}

func handleConnections(cfg *config.Config, w http.ResponseWriter, r *http.Request) {
	// Shed load before the WebSocket handshake when already at capacity. This
	// is best effort; registerClient performs the authoritative check.
	if atClientCapacity() {
		http.Error(w, "Too many connections", http.StatusServiceUnavailable)
		log.Printf("Connection rejected, server at capacity (%d): %s", maxClients, r.RemoteAddr)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		http.Error(w, "Could not upgrade to WebSocket", http.StatusInternalServerError)
		return
	}

	if cfg.AuthEnabled {
		claims, err := oauth.ValidateToken(cfg, r)
		if err != nil {
			ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			ws.WriteMessage(websocket.TextMessage, []byte("401-Unauthorized"))
			log.Printf("Client unauthorized: %s %v", r.RemoteAddr, err)
			ws.Close()
			return
		}
		log.Printf("Client connected: %s, %s", r.RemoteAddr, claims[cfg.OAuthConfig.UsernameClaim])
	} else {
		log.Printf("Client connected: %s", r.RemoteAddr)
	}

	c := &wsClient{conn: ws, send: make(chan []byte, 1)}

	if !registerClient(c) {
		// Capacity was reached between the pre-upgrade check and here.
		log.Printf("Connection rejected, server at capacity (%d): %s", maxClients, r.RemoteAddr)
		ws.Close()
		return
	}
	go c.writePump()

	// Detect dead peers: require a pong (or any frame) within pongWait and
	// extend the deadline whenever one arrives. writePump's pings keep a live
	// peer's deadline fresh; a peer that vanishes silently (laptop sleep, NAT
	// idle-drop) trips the deadline and is reaped, freeing its slot.
	readWait := pongWait()
	ws.SetReadDeadline(time.Now().Add(readWait))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(readWait))
		return nil
	})

	// Read loop: we don't expect inbound messages, but reading is how a
	// disconnect (or close frame) is detected, and it processes the pong frames
	// that drive the deadline above. When it returns, the client is gone.
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			log.Printf("Client disconnected: %s, %v", r.RemoteAddr, err)
			break
		}
	}
	unregisterClient(c)
}

// writePump owns every write to the connection: snapshot frames from send and
// periodic keepalive pings. It exits, closing the connection, when the client
// is unregistered (send closed) or a write fails.
func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingPeriod())
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if !ok {
				// Unregistered: send a close frame and stop.
				c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				c.conn.Close()
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("Write error; closing: %s, %v", c.conn.RemoteAddr(), err)
				c.conn.Close()
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Ping error; closing: %s, %v", c.conn.RemoteAddr(), err)
				c.conn.Close()
				return
			}
		}
	}
}

// atClientCapacity reports whether the concurrent connection limit is reached.
func atClientCapacity() bool {
	if maxClients <= 0 {
		return false
	}
	clientsMu.Lock()
	defer clientsMu.Unlock()
	return len(clients) >= maxClients
}

// registerClient adds the client to the registry and seeds it with the latest
// snapshot, all under the broadcast lock. It returns false (registering
// nothing) if the concurrent connection cap is reached.
func registerClient(c *wsClient) bool {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	if maxClients > 0 && len(clients) >= maxClients {
		return false
	}
	clients[c] = struct{}{}

	// Seed the most recently fanned-out frame, if any, under the same lock that
	// guards broadcasts. This keeps registration and seeding atomic with respect
	// to a broadcast: the client cannot miss an in-flight frame, is never sent a
	// "null" frame before the first poll, and is never seeded with a frame newer
	// than one still queued for fan-out (which would cause a visible rollback).
	// The send buffer was just created with cap 1, so this never blocks.
	if lastFanned != nil {
		c.send <- lastFanned
	}
	return true
}

func unregisterClient(c *wsClient) {
	clientsMu.Lock()
	if _, ok := clients[c]; ok {
		delete(clients, c)
		// Closing send terminates writePump's range. Safe against the
		// broadcaster because it only enqueues to clients still in the map
		// and holds the same lock.
		close(c.send)
	}
	clientsMu.Unlock()
}

func handleBroadcasts() {
	for msg := range broadcast {
		clientsMu.Lock()
		// Record the frame being fanned out so a client registering concurrently
		// is seeded with this frame (or a newer one), never a stale one.
		lastFanned = msg
		for c := range clients {
			enqueue(c, msg)
		}
		clientsMu.Unlock()
	}
}

// enqueue performs a non-blocking, latest-wins handoff to a client's send
// buffer. If the buffer already holds an undelivered frame, that stale frame
// is discarded in favor of msg so a lagging client always receives the most
// recent snapshot next. Callers must hold clientsMu.
func enqueue(c *wsClient, msg []byte) {
	select {
	case c.send <- msg:
	default:
		// Buffer full: drop the stale pending frame, then enqueue the latest.
		select {
		case <-c.send:
		default:
		}
		select {
		case c.send <- msg:
		default:
		}
	}
}
