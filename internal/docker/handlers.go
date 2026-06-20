package docker

import (
	"log"
	"net/http"
	"net/url"
	"sync"
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
	// maxClients caps the number of concurrent WebSocket connections to bound
	// resource use. 0 means unlimited; it is set from config in
	// RegisterDockerHandlers.
	maxClients int
)

const wsWriteTimeout = 5 * time.Second

// wsClient is a single WebSocket connection. All writes to conn happen on its
// writePump goroutine. send is a depth-1 buffer holding the latest pending
// snapshot: the broadcaster never blocks on a slow client, and a client that
// falls behind receives the most recent state rather than a backlog of stale
// frames.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

func RegisterDockerHandlers(cfg *config.Config) {
	maxClients = cfg.MaxWSConnections

	go inspectSwarmServices(cfg)
	go handleBroadcasts()

	http.HandleFunc(cfg.ContextRoot+"ws", func(w http.ResponseWriter, r *http.Request) {
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

	// Read loop: we don't expect inbound messages, but reading is how a
	// disconnect (or close frame) is detected. When it returns, the client
	// is gone.
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			log.Printf("Client disconnected: %s, %v", r.RemoteAddr, err)
			break
		}
	}
	unregisterClient(c)
}

// writePump owns every write to the connection. It drains the send channel
// until the client is unregistered (channel closed) or a write fails, then
// closes the connection.
func (c *wsClient) writePump() {
	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("Write error; closing: %s, %v", c.conn.RemoteAddr(), err)
			break
		}
	}
	c.conn.Close()
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

	// Seed the latest snapshot, if one exists, under the same lock that guards
	// broadcasts. This keeps registration and seeding atomic with respect to a
	// concurrent broadcast: the client cannot miss an in-flight frame, and it is
	// never sent a "null" frame before the first poll has produced data. The
	// send buffer was just created with cap 1, so this never blocks.
	if b := lastBroadcastedJSON.Load(); b != nil {
		c.send <- *b
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
