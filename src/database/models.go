package database

import "time"

// Message represents a git commit message
type Message struct {
	ID        int64     `json:"id" db:"id"`
	Content   string    `json:"content" db:"content"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// MessageUsage tracks when messages are used
type MessageUsage struct {
	ID         int64     `json:"id" db:"id"`
	MessageID  int64     `json:"message_id" db:"message_id"`
	UsedAt     time.Time `json:"used_at" db:"used_at"`
	ResetCycle int64     `json:"reset_cycle" db:"reset_cycle"`
}

// UsageMetadata stores cycle information
type UsageMetadata struct {
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// RandomMessageResponse is the API response for random message
type RandomMessageResponse struct {
	Success   bool      `json:"success"`
	Data      *Message  `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Meta      *struct {
		Cycle         int64 `json:"cycle"`
		TotalMessages int64 `json:"total_messages"`
		UsedInCycle   int64 `json:"used_in_cycle"`
		RemainingInCycle int64 `json:"remaining_in_cycle"`
	} `json:"meta,omitempty"`
}

// ErrorResponse is the standard error response
type ErrorResponse struct {
	Success   bool      `json:"success"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"error"`
	Timestamp time.Time `json:"timestamp"`
}
