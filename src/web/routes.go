package web

import (
	"net/http"

	"github.com/apimgr/gitmessages/src/static"
)

// SetupRoutes configures all web routes
func (h *Handler) SetupRoutes(mux *http.ServeMux) {
	// Static files from src/static
	staticFS := http.FS(static.Content)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticFS)))
	mux.HandleFunc("/manifest.json", h.ServeManifest)
	mux.HandleFunc("/robots.txt", h.ServeRobotsTxt)
	mux.HandleFunc("/.well-known/security.txt", h.ServeSecurityTxt)
	mux.HandleFunc("/security.txt", h.RedirectToWellKnown)

	// Public routes
	mux.HandleFunc("/", h.HomePage)

	// Setup routes (first run)
	mux.HandleFunc("/user/setup", h.SetupWelcome)
	mux.HandleFunc("/user/setup/register", h.SetupRegister)
	mux.HandleFunc("/user/setup/admin", h.SetupAdmin)
	mux.HandleFunc("/user/setup/complete", h.SetupComplete)

	// Auth routes
	mux.HandleFunc("/auth/login", h.Login)
	mux.HandleFunc("/auth/logout", h.Logout)
	mux.HandleFunc("/auth/register", h.Register)

	// User routes (require authentication)
	mux.HandleFunc("/user", h.UserDashboard)
	mux.HandleFunc("/user/profile", h.UserProfile)
	mux.HandleFunc("/user/settings", h.UserSettings)
	mux.HandleFunc("/user/tokens", h.UserTokens)
	mux.HandleFunc("/user/sessions", h.UserSessions)

	// Admin routes (require admin role)
	mux.HandleFunc("/admin", h.AdminDashboard)
	mux.HandleFunc("/admin/users", h.AdminUsers)
	mux.HandleFunc("/admin/settings", h.AdminSettings)
	mux.HandleFunc("/admin/database", h.AdminDatabase)
}
