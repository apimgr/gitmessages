package database

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL embed.FS

// DB wraps the database connection
type DB struct {
	conn *sql.DB
}

// New creates a new database connection
func New(dataDir string) (*DB, error) {
	dbPath := dataDir + "/gitmessages.db"

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Load messages from JSON
	if err := LoadMessages(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates all tables
func (db *DB) initSchema() error {
	schema, err := schemaSQL.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("failed to read schema.sql: %w", err)
	}

	_, err = db.conn.Exec(string(schema))
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	log.Println("Database schema initialized")
	return nil
}

// GetRandomMessage returns a random message that hasn't been used in current cycle
func (db *DB) GetRandomMessage() (*Message, error) {
	// Get current cycle
	var cycleStr string
	err := db.conn.QueryRow("SELECT value FROM usage_metadata WHERE key = 'current_cycle'").Scan(&cycleStr)
	if err != nil {
		return nil, fmt.Errorf("failed to get current cycle: %w", err)
	}

	cycle, err := strconv.ParseInt(cycleStr, 10, 64)
	if err != nil {
		cycle = 0
	}

	// Get total message count
	var totalCount int64
	err = db.conn.QueryRow("SELECT COUNT(*) FROM messages").Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count messages: %w", err)
	}

	if totalCount == 0 {
		return nil, fmt.Errorf("no messages available")
	}

	// Get used count in current cycle
	var usedCount int64
	err = db.conn.QueryRow("SELECT COUNT(*) FROM message_usage WHERE reset_cycle = ?", cycle).Scan(&usedCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count used messages: %w", err)
	}

	// Check if all messages used in current cycle
	if usedCount >= totalCount {
		// Reset to next cycle
		cycle++
		_, err = db.conn.Exec("UPDATE usage_metadata SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = 'current_cycle'",
			strconv.FormatInt(cycle, 10))
		if err != nil {
			return nil, fmt.Errorf("failed to update cycle: %w", err)
		}
		log.Printf("All messages used. Starting new cycle: %d", cycle)
		usedCount = 0
	}

	// Get random unused message in current cycle
	query := `
		SELECT m.id, m.content, m.created_at
		FROM messages m
		WHERE m.id NOT IN (
			SELECT message_id
			FROM message_usage
			WHERE reset_cycle = ?
		)
		ORDER BY RANDOM()
		LIMIT 1
	`

	var msg Message
	err = db.conn.QueryRow(query, cycle).Scan(&msg.ID, &msg.Content, &msg.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get random message: %w", err)
	}

	// Mark message as used
	_, err = db.conn.Exec(
		"INSERT INTO message_usage (message_id, reset_cycle) VALUES (?, ?)",
		msg.ID, cycle,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to mark message as used: %w", err)
	}

	return &msg, nil
}

// GetAllMessages returns all messages from database
func (db *DB) GetAllMessages() ([]string, error) {
	rows, err := db.conn.Query("SELECT content FROM messages ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, content)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating messages: %w", err)
	}

	return messages, nil
}

// GetMessageStats returns statistics about message usage
func (db *DB) GetMessageStats() (map[string]interface{}, error) {
	var cycleStr string
	err := db.conn.QueryRow("SELECT value FROM usage_metadata WHERE key = 'current_cycle'").Scan(&cycleStr)
	if err != nil {
		return nil, err
	}

	cycle, _ := strconv.ParseInt(cycleStr, 10, 64)

	var totalCount, usedCount int64
	db.conn.QueryRow("SELECT COUNT(*) FROM messages").Scan(&totalCount)
	db.conn.QueryRow("SELECT COUNT(*) FROM message_usage WHERE reset_cycle = ?", cycle).Scan(&usedCount)

	return map[string]interface{}{
		"cycle":                cycle,
		"total_messages":       totalCount,
		"used_in_cycle":        usedCount,
		"remaining_in_cycle":   totalCount - usedCount,
	}, nil
}

// ResetCycle manually resets to a new cycle
func (db *DB) ResetCycle() error {
	var cycleStr string
	err := db.conn.QueryRow("SELECT value FROM usage_metadata WHERE key = 'current_cycle'").Scan(&cycleStr)
	if err != nil {
		return err
	}

	cycle, _ := strconv.ParseInt(cycleStr, 10, 64)
	cycle++

	_, err = db.conn.Exec(
		"UPDATE usage_metadata SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = 'current_cycle'",
		strconv.FormatInt(cycle, 10),
	)
	if err != nil {
		return err
	}

	log.Printf("Cycle manually reset to: %d", cycle)
	return nil
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
