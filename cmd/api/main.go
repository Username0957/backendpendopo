package main

import (
	"log"
	"net/http"
	"strings"

	"pendopo-chat-app/backend/internal/config"
	"pendopo-chat-app/backend/internal/database"
	"pendopo-chat-app/backend/internal/handlers"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
)

func main() {
	log.Println("🏠 Starting Pendopo Chat Server...")

	// ── Step 1: Load configuration ──────────────────────────────────────
	cfg := config.Load()
	log.Printf("📋 Config loaded: Port=%s, Model=%s", cfg.Port, cfg.GeminiModel)

	// ── Step 2: Connect to database ─────────────────────────────────────
	if err := database.Connect(cfg.DatabaseURL); err != nil {
		log.Fatalf("❌ Failed to connect to database: %v", err)
	}
	defer database.Close()

	// ── Step 3: Initialize database schema ──────────────────────────────
	if err := database.InitSchema(); err != nil {
		log.Fatalf("❌ Failed to initialize schema: %v", err)
	}

	// ── Step 4: Start cleanup worker (hourly) ───────────────────────────
	database.StartCleanupWorker()

	// ── Step 5: Create WebSocket Hub ────────────────────────────────────
	hub := handlers.NewHub()
	go hub.Run()
	log.Println("🔌 WebSocket Hub started")

	// ── Step 6: Setup AI configuration ──────────────────────────────────
	aiCfg := &handlers.AIConfig{
		APIKey: cfg.GeminiAPIKey,
		Model:  cfg.GeminiModel,
	}

	// ── Step 7: Setup HTTP routes ───────────────────────────────────────
	router := mux.NewRouter()

	// Health check
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"pendopo-chat-backend"}`))
	}).Methods("GET")

	// WebSocket endpoint
	router.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handlers.ServeWS(hub, w, r)
	})

	// Session endpoints
	router.HandleFunc("/api/session", handlers.CreateSession).Methods("POST")
	router.HandleFunc("/api/session/validate", handlers.ValidateSession).Methods("GET")

	// Chat endpoints
	router.HandleFunc("/api/messages", handlers.GetMessages).Methods("GET")
	router.HandleFunc("/api/online", func(w http.ResponseWriter, r *http.Request) {
		handlers.GetOnlineCount(hub, w, r)
	}).Methods("GET")

	// AI endpoints
	router.HandleFunc("/api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		handlers.HandleAIChat(aiCfg, w, r)
	}).Methods("POST")
	router.HandleFunc("/api/ai/remaining", handlers.HandleAIRemaining).Methods("GET")
	router.HandleFunc("/api/ai/history", handlers.HandleAIHistory).Methods("GET")

	// ── Step 8: Setup CORS ──────────────────────────────────────────────
	origins := strings.Split(cfg.AllowedOrigins, ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})
	handler := c.Handler(router)

	// ── Step 9: Start HTTP Server ───────────────────────────────────────
	log.Printf("🚀 Pendopo Chat Server running on :%s", cfg.Port)
	log.Printf("📡 WebSocket: ws://localhost:%s/ws", cfg.Port)
	log.Printf("🌐 API: http://localhost:%s/api", cfg.Port)

	if err := http.ListenAndServe(":"+cfg.Port, handler); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
