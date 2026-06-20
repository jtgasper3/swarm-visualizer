package docker

import (
	"encoding/json"
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
)

const wsWriteTimeout = 5 * time.Second

// client is a single WebSocket connection. All writes to conn happen on its
// writePump goroutine. send is a depth-1 buffer holding the latest pending
// snapshot: the broadcaster never blocks on a slow client, and a client that
// falls behind receives the most recent state rather than a backlog of stale
// frames.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

func RegisterDockerHandlers(cfg *config.Config) {
	go inspectSwarmServices(cfg)
	go handleBroadcasts()

	http.HandleFunc(cfg.ContextRoot+"ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnections(cfg, w, r)
	})

}

func handleConnections(cfg *config.Config, w http.ResponseWriter, r *http.Request) {
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

	// Seed the connection with the latest known state so it renders
	// immediately rather than waiting for the next change.
	if jsonBytes, err := json.Marshal(lastBroadcastedData.Load()); err != nil {
		log.Println("Error marshalling combined data:", err)
	} else {
		c.send <- jsonBytes
	}

	registerClient(c)
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

func registerClient(c *wsClient) {
	clientsMu.Lock()
	clients[c] = struct{}{}
	clientsMu.Unlock()
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
