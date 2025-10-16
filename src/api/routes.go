package api

import (
	"net/http"
)

// SetupRoutes configures all API routes
func (h *Handler) SetupRoutes(mux *http.ServeMux) {
	// API v1 routes
	mux.HandleFunc("/api/v1/random", h.GetRandomMessage)
	mux.HandleFunc("/api/v1/random.txt", h.GetRandomMessageText)
	mux.HandleFunc("/api/v1/messages.json", h.GetAllMessages)
	mux.HandleFunc("/api/v1/stats", h.GetStats)
	mux.HandleFunc("/api/v1/reset", h.ResetCycle)

	// Health check endpoint
	mux.HandleFunc("/healthz", h.HealthCheck)
	mux.HandleFunc("/api/v1/health", h.HealthCheck)
	mux.HandleFunc("/api/v1/health.txt", h.HealthCheckText)
}

// HealthCheck returns server health status
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	stats, _ := h.db.GetMessageStats()

	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": "2024-01-01T12:00:00Z",
		"checks": map[string]interface{}{
			"database": map[string]interface{}{
				"status": "connected",
				"type":   "sqlite",
			},
			"messages": stats,
		},
	}

	h.sendJSON(w, http.StatusOK, health)
}

// HealthCheckText returns health status as plain text
func (h *Handler) HealthCheckText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Status: healthy\nDatabase: connected\n"))
}
