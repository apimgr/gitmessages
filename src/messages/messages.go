package messages

import (
	"embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

//go:embed data/messages.json
var messagesFS embed.FS

// Manager manages git commit messages
type Manager struct {
	messages    []string
	usedIndexes map[int]bool
	cycle       int
	mu          sync.RWMutex
}

// New creates a new message manager and loads all messages
func New() (*Manager, error) {
	m := &Manager{
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}

	if err := m.loadMessages(); err != nil {
		return nil, err
	}

	// Seed random
	rand.Seed(time.Now().UnixNano())

	return m, nil
}

// loadMessages loads all messages from embedded JSON
func (m *Manager) loadMessages() error {
	data, err := messagesFS.ReadFile("data/messages.json")
	if err != nil {
		return fmt.Errorf("failed to read messages.json: %w", err)
	}

	if err := json.Unmarshal(data, &m.messages); err != nil {
		return fmt.Errorf("failed to parse messages.json: %w", err)
	}

	return nil
}

// GetRandom returns a random message that hasn't been used in current cycle
func (m *Manager) GetRandom() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return "", fmt.Errorf("no messages available")
	}

	// Check if all messages have been used
	if len(m.usedIndexes) >= len(m.messages) {
		// Start new cycle
		m.cycle++
		m.usedIndexes = make(map[int]bool)
	}

	// Find an unused message
	maxAttempts := len(m.messages) * 2
	for i := 0; i < maxAttempts; i++ {
		idx := rand.Intn(len(m.messages))
		if !m.usedIndexes[idx] {
			m.usedIndexes[idx] = true
			return m.messages[idx], nil
		}
	}

	// Fallback: find first unused
	for idx := range m.messages {
		if !m.usedIndexes[idx] {
			m.usedIndexes[idx] = true
			return m.messages[idx], nil
		}
	}

	// Should never happen, but just in case
	return m.messages[rand.Intn(len(m.messages))], nil
}

// GetAll returns all messages
func (m *Manager) GetAll() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.messages
}

// GetAllJSON returns the raw JSON data
func (m *Manager) GetAllJSON() ([]byte, error) {
	return messagesFS.ReadFile("data/messages.json")
}

// Count returns the total number of messages
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages)
}

// Stats returns usage statistics
func (m *Manager) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"cycle":              m.cycle,
		"total_messages":     len(m.messages),
		"used_in_cycle":      len(m.usedIndexes),
		"remaining_in_cycle": len(m.messages) - len(m.usedIndexes),
	}
}

// ResetCycle manually resets to a new cycle
func (m *Manager) ResetCycle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cycle++
	m.usedIndexes = make(map[int]bool)
}
