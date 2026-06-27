package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"pendopo-chat-app/backend/internal/database"
	"pendopo-chat-app/backend/internal/models"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ============================================================================
// WebSocket Hub — manages all active WebSocket connections
// ============================================================================

// Hub maintains the set of active clients and broadcasts messages to them.
type Hub struct {
	clients    map[*Client]bool // All registered clients
	broadcast  chan []byte       // Inbound messages to broadcast
	register   chan *Client      // Register requests from clients
	unregister chan *Client      // Unregister requests from clients
	mu         sync.RWMutex     // Protects clients map
}

// NewHub creates a new Hub instance.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the hub's main event loop. Must be called as a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

			log.Printf("👤 Client joined: %s (Meja %d) — Total online: %d",
				client.Session.Nickname, client.Session.TableNumber, h.OnlineCount())

			// Broadcast user joined event
			h.broadcastEvent("user_joined", models.UserEventPayload{
				Nickname:    client.Session.Nickname,
				TableNumber: client.Session.TableNumber,
			})

			// Broadcast updated online count
			h.broadcastOnlineCount()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

			log.Printf("👋 Client left: %s (Meja %d) — Total online: %d",
				client.Session.Nickname, client.Session.TableNumber, h.OnlineCount())

			// Broadcast user left event
			h.broadcastEvent("user_left", models.UserEventPayload{
				Nickname:    client.Session.Nickname,
				TableNumber: client.Session.TableNumber,
			})

			// Broadcast updated online count
			h.broadcastOnlineCount()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// OnlineCount returns the number of currently connected clients.
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// broadcastEvent sends a typed event to all connected clients.
func (h *Hub) broadcastEvent(eventType string, payload interface{}) {
	msg := models.WSMessage{
		Type:    eventType,
		Payload: payload,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling event: %v", err)
		return
	}
	h.broadcast <- data
}

// broadcastOnlineCount sends the current online user count to all clients.
func (h *Hub) broadcastOnlineCount() {
	h.broadcastEvent("online_count", models.OnlineCountPayload{
		Count: h.OnlineCount(),
	})
}

// ============================================================================
// WebSocket Client — represents a single WebSocket connection
// ============================================================================

const (
	writeWait      = 10 * time.Second    // Time allowed to write a message
	pongWait       = 60 * time.Second    // Time allowed to read the next pong
	pingPeriod     = (pongWait * 9) / 10 // Send pings with this period (must be < pongWait)
	maxMessageSize = 4096                // Maximum message size in bytes
)

// Upgrader configures the WebSocket upgrade from HTTP.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, validate against allowed origins
		return true
	},
}

// Client represents a single WebSocket connection with session info.
type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	send    chan []byte
	Session *models.Session
}

// readPump reads messages from the WebSocket connection.
// It runs in its own goroutine per client.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, rawMessage, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Parse incoming message
		var incoming struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(rawMessage, &incoming); err != nil {
			log.Printf("Error parsing message: %v", err)
			continue
		}

		// Validate message content
		if incoming.Content == "" || len(incoming.Content) > 1000 {
			continue
		}

		// Save message to database
		msg, err := saveMessage(c.Session, incoming.Content)
		if err != nil {
			log.Printf("Error saving message: %v", err)
			continue
		}

		// Broadcast the message to all clients
		wsMsg := models.WSMessage{
			Type:    "chat_message",
			Payload: msg,
		}
		data, err := json.Marshal(wsMsg)
		if err != nil {
			log.Printf("Error marshaling message: %v", err)
			continue
		}
		c.hub.broadcast <- data
	}
}

// writePump writes messages to the WebSocket connection.
// It runs in its own goroutine per client.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ============================================================================
// HTTP Handlers
// ============================================================================

// ServeWS handles WebSocket upgrade requests for the room chat.
// Query parameter: ?token=<session_token>
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing session token", http.StatusUnauthorized)
		return
	}

	// Validate the session token
	session, err := getSessionByToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Send chat history (last 40 messages) to the new client synchronously before starting pumps
	history, err := getRecentMessages(40)
	if err != nil {
		log.Printf("Error loading history: %v", err)
	} else {
		wsMsg := models.WSMessage{
			Type:    "history",
			Payload: history,
		}
		data, err := json.Marshal(wsMsg)
		if err == nil {
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("Error sending history: %v", err)
				conn.Close()
				return
			}
		}
	}

	client := &Client{
		hub:     hub,
		conn:    conn,
		send:    make(chan []byte, 256),
		Session: session,
	}

	hub.register <- client

	// Start read/write pumps in separate goroutines
	go client.writePump()
	go client.readPump()
}

// CreateSession handles POST /api/session — creates a new guest session.
func CreateSession(w http.ResponseWriter, r *http.Request) {
	var req models.SessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if len(req.Nickname) < 2 || len(req.Nickname) > 50 {
		http.Error(w, "Nickname must be between 2 and 50 characters", http.StatusBadRequest)
		return
	}
	if req.TableNumber < 1 || req.TableNumber > 50 {
		http.Error(w, "Table number must be between 1 and 50", http.StatusBadRequest)
		return
	}

	// Validate optional occupation (max 100 chars)
	if len(req.Occupation) > 100 {
		http.Error(w, "Occupation must be under 100 characters", http.StatusBadRequest)
		return
	}
	// Validate optional purpose (max 200 chars)
	if len(req.Purpose) > 200 {
		http.Error(w, "Purpose must be under 200 characters", http.StatusBadRequest)
		return
	}

	// Generate unique session token
	sessionToken := uuid.New().String()

	// Insert session into database
	var session models.Session
	err := database.DB.QueryRow(`
		INSERT INTO sessions (nickname, table_number, occupation, purpose, session_token)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, nickname, table_number, occupation, purpose, session_token, is_active, created_at, expires_at
	`, req.Nickname, req.TableNumber, req.Occupation, req.Purpose, sessionToken).Scan(
		&session.ID, &session.Nickname, &session.TableNumber,
		&session.Occupation, &session.Purpose,
		&session.SessionToken, &session.IsActive, &session.CreatedAt, &session.ExpiresAt,
	)
	if err != nil {
		log.Printf("Error creating session: %v", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ New session: %s (Meja %d) — %s", session.Nickname, session.TableNumber, session.Occupation)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.SessionResponse{
		SessionToken: sessionToken,
		Session:      session,
	})
}

// ValidateSession handles GET /api/session/validate?token=xxx
func ValidateSession(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing token parameter", http.StatusBadRequest)
		return
	}

	session, err := getSessionByToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

// GetMessages handles GET /api/messages — returns recent chat history.
func GetMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := getRecentMessages(40)
	if err != nil {
		log.Printf("Error fetching messages: %v", err)
		http.Error(w, "Failed to fetch messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// GetOnlineCount handles GET /api/online — returns current online user count.
func GetOnlineCount(hub *Hub, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.OnlineCountPayload{
		Count: hub.OnlineCount(),
	})
}

// ============================================================================
// Database helpers
// ============================================================================

// getSessionByToken retrieves an active, non-expired session by its token.
func getSessionByToken(token string) (*models.Session, error) {
	var session models.Session
	err := database.DB.QueryRow(`
		SELECT id, nickname, table_number, occupation, purpose, session_token, is_active, created_at, expires_at
		FROM sessions
		WHERE session_token = $1 AND is_active = TRUE AND expires_at > NOW()
	`, token).Scan(
		&session.ID, &session.Nickname, &session.TableNumber,
		&session.Occupation, &session.Purpose,
		&session.SessionToken, &session.IsActive, &session.CreatedAt, &session.ExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	return &session, nil
}

// saveMessage persists a chat message to the database and returns the saved message.
func saveMessage(session *models.Session, content string) (*models.Message, error) {
	var msg models.Message
	err := database.DB.QueryRow(`
		INSERT INTO messages (session_id, nickname, table_number, content)
		VALUES ($1, $2, $3, $4)
		RETURNING id, session_id, nickname, table_number, content, created_at, expires_at
	`, session.ID, session.Nickname, session.TableNumber, content).Scan(
		&msg.ID, &msg.SessionID, &msg.Nickname, &msg.TableNumber,
		&msg.Content, &msg.CreatedAt, &msg.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// getRecentMessages retrieves the most recent N messages that haven't expired.
func getRecentMessages(limit int) ([]models.Message, error) {
	rows, err := database.DB.Query(`
		SELECT id, session_id, nickname, table_number, content, created_at, expires_at
		FROM messages
		WHERE expires_at > NOW()
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Nickname, &msg.TableNumber,
			&msg.Content, &msg.CreatedAt, &msg.ExpiresAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	// Return empty array instead of null for JSON
	if messages == nil {
		messages = []models.Message{}
	}

	return messages, nil
}
