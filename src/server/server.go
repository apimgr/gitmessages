package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/apimgr/gitmessages/src/api"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/web"
)

// Server represents the HTTP server
type Server struct {
	db      *database.DB
	handler *api.Handler
	mux     *http.ServeMux
	srv     *http.Server
}

// New creates a new server instance
func New(db *database.DB, address string, port string) (*Server, error) {
	mux := http.NewServeMux()

	// Setup API routes
	apiHandler := api.NewHandler(db)
	apiHandler.SetupRoutes(mux)

	// Setup web routes
	webHandler, err := web.NewHandler(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create web handler: %w", err)
	}
	webHandler.SetupRoutes(mux)

	// Add CORS middleware
	wrappedMux := corsMiddleware(mux)

	// Create HTTP server
	addr := fmt.Sprintf("%s:%s", address, port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      wrappedMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		db:      db,
		handler: apiHandler,
		mux:     mux,
		srv:     srv,
	}, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log.Printf("Server starting on %s", s.srv.Addr)
	return s.srv.ListenAndServe()
}

// corsMiddleware adds CORS headers to all responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add security headers
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle preflight
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
