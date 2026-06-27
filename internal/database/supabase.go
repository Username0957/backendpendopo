package database

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// DB is the global database connection pool.
var DB *sql.DB

// Connect establishes a connection to the Supabase PostgreSQL database.
// It configures the connection pool for optimal performance on the free tier.
func Connect(databaseURL string) error {
	var err error
	DB, err = sql.Open("postgres", databaseURL)
	if err != nil {
		return err
	}

	// Connection pool settings optimized for Supabase free tier
	DB.SetMaxOpenConns(10)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(30 * time.Minute)
	DB.SetConnMaxIdleTime(5 * time.Minute)

	// Verify the connection is alive
	if err = DB.Ping(); err != nil {
		return err
	}

	log.Println("✅ Database connected successfully")
	return nil
}

// InitSchema creates the required tables if they don't already exist.
// This is safe to call on every startup.
func InitSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		nickname VARCHAR(50) NOT NULL,
		table_number INT NOT NULL,
		session_token VARCHAR(255) UNIQUE NOT NULL,
		is_active BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		expires_at TIMESTAMPTZ DEFAULT (NOW() + INTERVAL '15 hours')
	);

	CREATE TABLE IF NOT EXISTS messages (
		id BIGSERIAL PRIMARY KEY,
		session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
		nickname VARCHAR(50) NOT NULL,
		table_number INT NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		expires_at TIMESTAMPTZ DEFAULT (NOW() + INTERVAL '24 hours')
	);

	CREATE TABLE IF NOT EXISTS ai_chats (
		id BIGSERIAL PRIMARY KEY,
		session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
		role VARCHAR(10) NOT NULL CHECK (role IN ('user', 'assistant')),
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_expires_at ON messages(expires_at);
	CREATE INDEX IF NOT EXISTS idx_ai_chats_session_id ON ai_chats(session_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(session_token);
	CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(is_active) WHERE is_active = TRUE;
	`

	_, err := DB.Exec(schema)
	if err != nil {
		return err
	}

	log.Println("✅ Database schema initialized")
	return nil
}

// CleanupExpiredMessages deletes room chat messages that have passed their 24-hour expiry.
// Returns the number of rows deleted.
func CleanupExpiredMessages() (int64, error) {
	result, err := DB.Exec("DELETE FROM messages WHERE expires_at <= NOW()")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CleanupExpiredSessions deactivates sessions that have passed their 15-hour expiry.
// Returns the number of rows updated.
func CleanupExpiredSessions() (int64, error) {
	result, err := DB.Exec("UPDATE sessions SET is_active = FALSE WHERE is_active = TRUE AND expires_at <= NOW()")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// StartCleanupWorker runs a goroutine that periodically cleans up expired data.
// It runs every hour and logs the results.
func StartCleanupWorker() {
	go func() {
		log.Println("🧹 Cleanup worker started (runs every 1 hour)")
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			// Clean up expired messages (24-hour expiry)
			msgCount, err := CleanupExpiredMessages()
			if err != nil {
				log.Printf("❌ Error cleaning up expired messages: %v", err)
			} else if msgCount > 0 {
				log.Printf("🧹 Cleaned up %d expired messages", msgCount)
			}

			// Clean up expired sessions (15-hour expiry)
			sessCount, err := CleanupExpiredSessions()
			if err != nil {
				log.Printf("❌ Error cleaning up expired sessions: %v", err)
			} else if sessCount > 0 {
				log.Printf("🧹 Deactivated %d expired sessions", sessCount)
			}
		}
	}()
}

// Close gracefully closes the database connection pool.
func Close() {
	if DB != nil {
		DB.Close()
		log.Println("Database connection closed")
	}
}
