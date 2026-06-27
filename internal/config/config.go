package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all configuration values for the application.
type Config struct {
	Port           string
	DatabaseURL    string
	GeminiAPIKey   string
	GeminiModel    string
	AllowedOrigins string
}

// Load reads environment variables and returns a Config struct.
// It attempts to load from .env file first (for local development),
// then falls back to actual environment variables (for production).
func Load() *Config {
	// Attempt to load .env file; ignore error if not found (production)
	err := godotenv.Load()
	if err != nil {
		log.Println("INFO: .env file not found, using system environment variables")
	}

	cfg := &Config{
		Port:           getEnv("PORT", "8080"),
		DatabaseURL:    getEnv("DATABASE_URL", ""),
		GeminiAPIKey:   getEnv("GEMINI_API_KEY", ""),
		GeminiModel:    getEnv("GEMINI_MODEL", "gemini-2.5-flash"),
		AllowedOrigins: getEnv("ALLOWED_ORIGINS", "http://localhost:3000"),
	}

	// Validate required fields
	if cfg.DatabaseURL == "" {
		log.Fatal("FATAL: DATABASE_URL environment variable is required")
	}
	if cfg.GeminiAPIKey == "" {
		log.Fatal("FATAL: GEMINI_API_KEY environment variable is required")
	}

	return cfg
}

// getEnv reads an environment variable with a fallback default value.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
