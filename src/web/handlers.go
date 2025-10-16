package web

import (
	"net/http"
)

// HomePage renders the homepage
func (h *Handler) HomePage(w http.ResponseWriter, r *http.Request) {
	data := h.GetDefaultTemplateData()
	data.Title = "Home"

	h.renderTemplate(w, "base.html", data)
}

// SetupWelcome shows the welcome screen for first-time setup
func (h *Handler) SetupWelcome(w http.ResponseWriter, r *http.Request) {
	// TODO: Check if setup is needed (no users in DB)
	data := h.GetDefaultTemplateData()
	data.Title = "Setup"
	data.ShowHeader = false

	h.renderTemplate(w, "setup-welcome.html", data)
}

// SetupRegister handles user registration during setup
func (h *Handler) SetupRegister(w http.ResponseWriter, r *http.Request) {
	data := h.GetDefaultTemplateData()
	data.Title = "Create Account"
	data.ShowHeader = false

	if r.Method == http.MethodPost {
		// TODO: Implement user creation
		// For now, redirect to admin setup
		http.Redirect(w, r, "/user/setup/admin", http.StatusSeeOther)
		return
	}

	h.renderTemplate(w, "setup-register.html", data)
}

// SetupAdmin handles administrator creation during setup
func (h *Handler) SetupAdmin(w http.ResponseWriter, r *http.Request) {
	data := h.GetDefaultTemplateData()
	data.Title = "Create Administrator"
	data.ShowHeader = false

	if r.Method == http.MethodPost {
		// TODO: Implement admin creation
		// For now, redirect to complete
		http.Redirect(w, r, "/user/setup/complete", http.StatusSeeOther)
		return
	}

	h.renderTemplate(w, "setup-admin.html", data)
}

// SetupComplete redirects based on authentication state
func (h *Handler) SetupComplete(w http.ResponseWriter, r *http.Request) {
	// TODO: Check authentication state
	// If admin: redirect to /admin
	// If user: redirect to /user
	// If not authenticated: redirect to /auth/login
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// Login handles user login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	data := h.GetDefaultTemplateData()
	data.Title = "Login"

	if r.Method == http.MethodPost {
		// TODO: Implement authentication
		http.Redirect(w, r, "/user", http.StatusSeeOther)
		return
	}

	h.renderTemplate(w, "login.html", data)
}

// Logout handles user logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement session destruction
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Register handles new user registration
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	data := h.GetDefaultTemplateData()
	data.Title = "Register"

	if r.Method == http.MethodPost {
		// TODO: Implement user registration
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.renderTemplate(w, "login.html", data) // Using login template for now
}

// UserDashboard shows the user dashboard
func (h *Handler) UserDashboard(w http.ResponseWriter, r *http.Request) {
	// TODO: Check authentication
	data := h.GetDefaultTemplateData()
	data.Title = "Dashboard"
	data.IsAuthenticated = true
	data.Username = "demo_user"
	data.DisplayName = "Demo User"
	data.Email = "demo@example.com"
	data.Role = "user"

	h.renderTemplate(w, "user-dashboard.html", data)
}

// UserProfile shows the user profile page
func (h *Handler) UserProfile(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement profile page
	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

// UserSettings shows the user settings page
func (h *Handler) UserSettings(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement settings page
	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

// UserTokens shows the API tokens page
func (h *Handler) UserTokens(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement tokens page
	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

// UserSessions shows active sessions page
func (h *Handler) UserSessions(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement sessions page
	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

// AdminDashboard shows the admin dashboard
func (h *Handler) AdminDashboard(w http.ResponseWriter, r *http.Request) {
	// TODO: Check admin authentication
	data := h.GetDefaultTemplateData()
	data.Title = "Admin Dashboard"
	data.IsAuthenticated = true
	data.IsAdmin = true
	data.Username = "administrator"

	// Mock stats
	stats := map[string]interface{}{
		"TotalUsers":     10,
		"ActiveSessions": 5,
		"RequestsToday":  1234,
		"Uptime":         "5 days",
	}

	health := map[string]interface{}{
		"Database":   "connected",
		"SSL":        "valid",
		"DiskUsed":   45,
		"MemoryUsed": 32,
	}

	type AdminData struct {
		TemplateData
		Stats          map[string]interface{}
		Health         map[string]interface{}
		RecentActivity []interface{}
	}

	adminData := AdminData{
		TemplateData:   data,
		Stats:          stats,
		Health:         health,
		RecentActivity: []interface{}{},
	}

	h.renderTemplate(w, "admin-dashboard.html", adminData)
}

// AdminUsers shows the users management page
func (h *Handler) AdminUsers(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement users management
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// AdminSettings shows the server settings page
func (h *Handler) AdminSettings(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement settings management
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// AdminDatabase shows the database management page
func (h *Handler) AdminDatabase(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement database management
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// ServeManifest serves the PWA manifest
func (h *Handler) ServeManifest(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/static/manifest.json", http.StatusMovedPermanently)
}

// ServeRobotsTxt serves robots.txt
func (h *Handler) ServeRobotsTxt(w http.ResponseWriter, r *http.Request) {
	// TODO: Load from database settings
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("User-agent: *\nAllow: /\n"))
}

// ServeSecurityTxt serves security.txt (RFC 9116)
func (h *Handler) ServeSecurityTxt(w http.ResponseWriter, r *http.Request) {
	// TODO: Load from database settings
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Contact: security@example.com\nExpires: 2025-12-31T23:59:59Z\n"))
}

// RedirectToWellKnown redirects /security.txt to /.well-known/security.txt
func (h *Handler) RedirectToWellKnown(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/.well-known/security.txt", http.StatusMovedPermanently)
}
