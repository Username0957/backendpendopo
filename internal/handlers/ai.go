package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"pendopo-chat-app/backend/internal/database"
	"pendopo-chat-app/backend/internal/models"
)

// ============================================================================
// Constants
// ============================================================================

const (
	// MaxAIConversations is the maximum number of user messages allowed per session.
	// This limits API token usage. Each session gets 7 messages to the AI.
	MaxAIConversations = 7

	// GeminiAPIBaseURL is the base URL for the Google Gemini API.
	GeminiAPIBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
)

// systemPrompt defines the AI's personality, knowledge, and behavior.
// It includes the café menu and instructions to always respond in Indonesian.
var systemPrompt = `Kamu adalah "Pendopo AI Buddy", asisten virtual cerdas milik Kafe Pendopo.
Kamu ramah, santai, dan berbicara dengan gaya anak muda yang sopan.
SELALU jawab dalam Bahasa Indonesia. JANGAN gunakan markdown berlebihan.

ATURAN PENTING:
- Jawab SINGKAT, PADAT, dan JELAS. Maksimal 3-4 paragraf pendek.
- JANGAN berpikir terlalu panjang atau bertele-tele.
- Kamu BOLEH menjawab pertanyaan umum di luar topik kafe (misalnya tentang bisnis, teknologi, ide, motivasi, dll).
- TETAPI kamu TIDAK BOLEH menjawab pertanyaan yang bersifat: konten dewasa/NSFW, kekerasan, ujaran kebencian, politik sensitif, atau hal ilegal.
- Jika ditanya hal terlarang, tolak dengan sopan dan arahkan ke topik lain.
- Default persona-mu tetap sebagai asisten kafe yang ramah. Selalu selipkan referensi ke kafe jika relevan.

TUGASMU:
1. Membantu pengunjung memilih menu makanan dan minuman
2. Memberikan rekomendasi berdasarkan preferensi pengunjung
3. Menjawab pertanyaan seputar kafe (jam buka, lokasi, fasilitas)
4. Memberikan informasi promo yang sedang berlaku
5. Menjawab pertanyaan umum dengan ramah dan ringkas
6. Jika pengunjung memiliki tujuan khusus (networking, ide bisnis, dll), bantu dengan tips singkat

MENU KAFE PENDOPO:

☕ KOPI:
- Espresso ................. Rp 18.000
- Americano ................ Rp 22.000
- Café Latte ............... Rp 28.000
- Cappuccino ............... Rp 28.000
- V60 Pour Over ............ Rp 30.000
- Kopi Susu Gula Aren ...... Rp 25.000
- Affogato ................. Rp 32.000

🍵 NON-KOPI:
- Matcha Latte ............. Rp 30.000
- Coklat Panas ............. Rp 25.000
- Teh Tarik ................ Rp 20.000
- Jus Jeruk Segar .......... Rp 22.000
- Lemon Tea ................ Rp 20.000
- Milkshake Vanilla ........ Rp 28.000

🍳 MAKANAN:
- Nasi Goreng Pendopo ...... Rp 35.000
- Mie Goreng Spesial ....... Rp 32.000
- Chicken Wings (6 pcs) .... Rp 35.000
- Roti Bakar Coklat/Keju ... Rp 20.000
- French Fries ............. Rp 25.000
- Sandwich Tuna ............ Rp 30.000
- Pasta Aglio Olio ......... Rp 38.000

🍰 DESSERT:
- Cheesecake ............... Rp 30.000
- Brownies ................. Rp 25.000
- Pisang Goreng Crispy ..... Rp 18.000
- Pancake Stack ............ Rp 28.000

📍 INFO KAFE:
- Jam Buka: Senin-Jumat 08:00-22:00, Sabtu-Minggu 09:00-23:00
- Lokasi: Jl. Pendopo No. 42, Jakarta Selatan
- Fasilitas: WiFi Gratis, Colokan Listrik, Meeting Room, Smoking Area
- Kapasitas: 50 meja

🎉 PROMO SAAT INI:
- Beli 2 kopi, gratis 1 French Fries (Senin-Kamis)
- Happy Hour: Semua minuman diskon 20% (14:00-16:00)
- Paket Hemat: Nasi Goreng + Es Teh = Rp 40.000

Jawab dengan singkat, jelas, dan menarik. Gunakan emoji sesekali untuk kesan ramah.`

// ============================================================================
// Gemini API Types
// ============================================================================

// geminiRequest is the request body for the Gemini API.
type geminiRequest struct {
	Contents       []geminiContent       `json:"contents"`
	SystemInstruct *geminiSystemInstruct `json:"systemInstruction,omitempty"`
}

type geminiSystemInstruct struct {
	Parts []geminiPart `json:"parts"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

// geminiResponse is the response from the Gemini API.
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// ============================================================================
// AI Chat Handler
// ============================================================================

// AIConfig holds the API key and model for Gemini.
type AIConfig struct {
	APIKey string
	Model  string
}

// HandleAIChat handles POST /api/ai/chat — sends a message to Gemini AI.
// It enforces a 7-message limit per session and maintains conversation context.
func HandleAIChat(aiCfg *AIConfig, w http.ResponseWriter, r *http.Request) {
	// Extract session token from Authorization header
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
		return
	}

	// Validate session
	session, err := getSessionByToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req models.AIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Message == "" || len(req.Message) > 500 {
		http.Error(w, "Message must be between 1 and 500 characters", http.StatusBadRequest)
		return
	}

	// Check AI conversation limit (count only user messages)
	used, err := countUserAIMessages(session.ID)
	if err != nil {
		log.Printf("Error counting AI messages: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if used >= MaxAIConversations {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":     "Batas percakapan AI tercapai (7/7). Terima kasih sudah menggunakan Pendopo AI Buddy! 🙏",
			"remaining": 0,
			"used":      used,
			"limit":     MaxAIConversations,
		})
		return
	}

	// Get conversation history for context (all previous messages)
	history, err := getAIChatHistory(session.ID)
	if err != nil {
		log.Printf("Error fetching AI history: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Build user profile context for personalized AI responses
	profileContext := fmt.Sprintf("Pengunjung ini bernama %s, duduk di Meja %d.", session.Nickname, session.TableNumber)
	if session.Occupation != "" {
		profileContext += fmt.Sprintf(" Pekerjaan: %s.", session.Occupation)
	}
	if session.Purpose != "" {
		profileContext += fmt.Sprintf(" Tujuan ke kafe: %s.", session.Purpose)
	}

	// Call Gemini API with user profile context
	reply, err := callGeminiAPI(aiCfg, history, req.Message, profileContext)
	if err != nil {
		log.Printf("Error calling Gemini API: %v", err)
		http.Error(w, "AI service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	// Save user message and AI reply to database
	if err := saveAIChat(session.ID, "user", req.Message); err != nil {
		log.Printf("Error saving user AI message: %v", err)
	}
	if err := saveAIChat(session.ID, "assistant", reply); err != nil {
		log.Printf("Error saving AI reply: %v", err)
	}

	remaining := MaxAIConversations - used - 1
	if remaining < 0 {
		remaining = 0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AIChatResponse{
		Reply:     reply,
		Remaining: remaining,
	})
}

// HandleAIRemaining handles GET /api/ai/remaining — returns the remaining AI conversation count.
func HandleAIRemaining(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
		return
	}

	session, err := getSessionByToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	used, err := countUserAIMessages(session.ID)
	if err != nil {
		log.Printf("Error counting AI messages: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	remaining := MaxAIConversations - used
	if remaining < 0 {
		remaining = 0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AIRemainingResponse{
		Used:      used,
		Limit:     MaxAIConversations,
		Remaining: remaining,
	})
}

// HandleAIHistory handles GET /api/ai/history — returns AI conversation history for a session.
func HandleAIHistory(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
		return
	}

	session, err := getSessionByToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	history, err := getAIChatHistory(session.ID)
	if err != nil {
		log.Printf("Error fetching AI history: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// ============================================================================
// Gemini API Call
// ============================================================================

// callGeminiAPI sends a message to the Gemini API with conversation history and user profile context.
func callGeminiAPI(cfg *AIConfig, history []models.AIChat, userMessage string, profileContext string) (string, error) {
	// Build conversation contents from history
	var contents []geminiContent
	for _, chat := range history {
		role := chat.Role
		if role == "assistant" {
			role = "model" // Gemini uses "model" instead of "assistant"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: chat.Content}},
		})
	}

	// Add the current user message
	contents = append(contents, geminiContent{
		Role:  "user",
		Parts: []geminiPart{{Text: userMessage}},
	})

	// Build system instruction with profile context
	fullSystemPrompt := systemPrompt
	if profileContext != "" {
		fullSystemPrompt += "\n\nINFO PENGUNJUNG SAAT INI:\n" + profileContext
	}

	// Build request with system instruction
	reqBody := geminiRequest{
		Contents: contents,
		SystemInstruct: &geminiSystemInstruct{
			Parts: []geminiPart{{Text: fullSystemPrompt}},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %w", err)
	}

	// Build API URL
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", GeminiAPIBaseURL, cfg.Model, cfg.APIKey)

	// Make HTTP request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error calling Gemini API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %w", err)
	}

	// Parse response
	var geminiResp geminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return "", fmt.Errorf("error parsing response: %w", err)
	}

	// Check for API error
	if geminiResp.Error != nil {
		return "", fmt.Errorf("Gemini API error: %s", geminiResp.Error.Message)
	}

	// Extract the response text
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini API")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

// ============================================================================
// Database helpers for AI Chat
// ============================================================================

// countUserAIMessages counts the number of user messages sent to the AI for a session.
func countUserAIMessages(sessionID string) (int, error) {
	var count int
	err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM ai_chats
		WHERE session_id = $1 AND role = 'user'
	`, sessionID).Scan(&count)
	return count, err
}

// getAIChatHistory retrieves all AI chat messages for a session, ordered by creation time.
func getAIChatHistory(sessionID string) ([]models.AIChat, error) {
	rows, err := database.DB.Query(`
		SELECT id, session_id, role, content, created_at
		FROM ai_chats
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []models.AIChat
	for rows.Next() {
		var chat models.AIChat
		if err := rows.Scan(&chat.ID, &chat.SessionID, &chat.Role, &chat.Content, &chat.CreatedAt); err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}

	if chats == nil {
		chats = []models.AIChat{}
	}

	return chats, nil
}

// saveAIChat persists an AI chat message (user or assistant) to the database.
func saveAIChat(sessionID, role, content string) error {
	_, err := database.DB.Exec(`
		INSERT INTO ai_chats (session_id, role, content)
		VALUES ($1, $2, $3)
	`, sessionID, role, content)
	return err
}
