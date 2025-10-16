package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

// Handler holds dependencies for API handlers
type Handler struct {
	db *database.DB
}

// NewHandler creates a new API handler
func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db}
}

// GetRandomMessage returns a random unused message
func (h *Handler) GetRandomMessage(w http.ResponseWriter, r *http.Request) {
	// Get random message
	msg, err := h.db.GetRandomMessage()
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "FETCH_ERROR", err.Error(), "")
		return
	}

	// Get stats for metadata
	stats, err := h.db.GetMessageStats()
	if err != nil {
		// Continue without stats
		stats = nil
	}

	// Build response
	response := database.RandomMessageResponse{
		Success:   true,
		Data:      msg,
		Timestamp: time.Now(),
	}

	// Add metadata if stats available
	if stats != nil {
		response.Meta = &struct {
			Cycle            int64 `json:"cycle"`
			TotalMessages    int64 `json:"total_messages"`
			UsedInCycle      int64 `json:"used_in_cycle"`
			RemainingInCycle int64 `json:"remaining_in_cycle"`
		}{
			Cycle:            stats["cycle"].(int64),
			TotalMessages:    stats["total_messages"].(int64),
			UsedInCycle:      stats["used_in_cycle"].(int64),
			RemainingInCycle: stats["remaining_in_cycle"].(int64),
		}
	}

	h.sendJSON(w, http.StatusOK, response)
}

// GetRandomMessageText returns a random message as plain text
func (h *Handler) GetRandomMessageText(w http.ResponseWriter, r *http.Request) {
	msg, err := h.db.GetRandomMessage()
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error: " + err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(msg.Content))
}

// GetStats returns message usage statistics
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.db.GetMessageStats()
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "STATS_ERROR", err.Error(), "")
		return
	}

	response := struct {
		Success   bool                   `json:"success"`
		Data      map[string]interface{} `json:"data"`
		Timestamp time.Time              `json:"timestamp"`
	}{
		Success:   true,
		Data:      stats,
		Timestamp: time.Now(),
	}

	h.sendJSON(w, http.StatusOK, response)
}

// GetAllMessages returns all messages as JSON array (directly from embedded file)
func (h *Handler) GetAllMessages(w http.ResponseWriter, r *http.Request) {
	// Read directly from embedded messages.json file
	data, err := database.MessagesJSON.ReadFile("data/messages.json")
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "FILE_READ_ERROR", err.Error(), "")
		return
	}

	// Return raw JSON file
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ResetCycle manually resets to a new cycle
func (h *Handler) ResetCycle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST allowed", "")
		return
	}

	err := h.db.ResetCycle()
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "RESET_ERROR", err.Error(), "")
		return
	}

	response := struct {
		Success   bool      `json:"success"`
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
	}{
		Success:   true,
		Message:   "Cycle reset successfully",
		Timestamp: time.Now(),
	}

	h.sendJSON(w, http.StatusOK, response)
}

// sendJSON sends a JSON response
func (h *Handler) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// sendError sends an error response
func (h *Handler) sendError(w http.ResponseWriter, status int, code, message, field string) {
	response := database.ErrorResponse{
		Success: false,
		Error: &struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field,omitempty"`
		}{
			Code:    code,
			Message: message,
			Field:   field,
		},
		Timestamp: time.Now(),
	}

	h.sendJSON(w, status, response)
}
