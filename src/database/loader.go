package database

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
)

//go:embed data/messages.json
var messagesData embed.FS

// MessagesJSON exposes the embedded messages.json for direct access
var MessagesJSON = messagesData

// LoadMessages reads messages.json and loads into database
func LoadMessages(db *sql.DB) error {
	// Read embedded JSON file
	data, err := messagesData.ReadFile("data/messages.json")
	if err != nil {
		return fmt.Errorf("failed to read messages.json: %w", err)
	}

	// Parse JSON
	var messages []string
	if err := json.Unmarshal(data, &messages); err != nil {
		return fmt.Errorf("failed to parse messages.json: %w", err)
	}

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check if messages already loaded
	var count int64
	err = tx.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check existing messages: %w", err)
	}

	if count > 0 {
		log.Printf("Messages already loaded (%d messages in database)", count)
		return nil
	}

	// Prepare insert statement
	stmt, err := tx.Prepare("INSERT INTO messages (content) VALUES (?)")
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer stmt.Close()

	// Insert all messages
	for _, msg := range messages {
		if msg == "" {
			continue // Skip empty messages
		}
		_, err := stmt.Exec(msg)
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Successfully loaded %d messages into database", len(messages))
	return nil
}
