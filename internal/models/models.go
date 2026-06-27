package models

import "time"

// Session represents a guest visitor's temporary session.
// No registration required — just nickname + table number.
type Session struct {
	ID           string    `json:"id"`
	Nickname     string    `json:"nickname"`
	TableNumber  int       `json:"table_number"`
	Occupation   string    `json:"occupation"`
	Purpose      string    `json:"purpose"`
	SessionToken string    `json:"session_token"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Message represents a single chat message in the public room.
// Each message expires after 24 hours and is auto-deleted.
type Message struct {
	ID          int64     `json:"id"`
	SessionID   string    `json:"session_id"`
	Nickname    string    `json:"nickname"`
	TableNumber int       `json:"table_number"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// AIChat represents a single message in a private AI conversation.
// Limited to 7 user messages per session to conserve API tokens.
type AIChat struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"` // "user" or "assistant"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// WSMessage is the WebSocket message envelope for real-time communication.
// Type determines how the frontend should handle the payload.
type WSMessage struct {
	Type    string      `json:"type"`    // "chat_message", "user_joined", "user_left", "history", "online_count"
	Payload interface{} `json:"payload"` // The actual data (Message, []Message, count, etc.)
}

// SessionRequest is the request body for creating a new session.
type SessionRequest struct {
	Nickname    string `json:"nickname"`
	TableNumber int    `json:"table_number"`
	Occupation  string `json:"occupation"`
	Purpose     string `json:"purpose"`
}

// SessionResponse is returned after successfully creating a session.
type SessionResponse struct {
	SessionToken string  `json:"session_token"`
	Session      Session `json:"session"`
}

// AIChatRequest is the request body for sending a message to the AI.
type AIChatRequest struct {
	Message string `json:"message"`
}

// AIChatResponse is returned after the AI processes a message.
type AIChatResponse struct {
	Reply     string `json:"reply"`
	Remaining int    `json:"remaining"` // How many AI messages the user has left (out of 7)
}

// AIRemainingResponse shows how many AI conversations are left.
type AIRemainingResponse struct {
	Used      int `json:"used"`
	Limit     int `json:"limit"`
	Remaining int `json:"remaining"`
}

// OnlineCountPayload is broadcast to all clients when the online count changes.
type OnlineCountPayload struct {
	Count int `json:"count"`
}

// UserEventPayload is broadcast when a user joins or leaves the room.
type UserEventPayload struct {
	Nickname    string `json:"nickname"`
	TableNumber int    `json:"table_number"`
}
