package web

import (
	"embed"
	"html/template"
	"net/http"

	"github.com/apimgr/gitmessages/src/database"
)

//go:embed templates
var content embed.FS

// Handler holds web dependencies
type Handler struct {
	db        *database.DB
	templates *template.Template
}

// NewHandler creates a new web handler
func NewHandler(db *database.DB) (*Handler, error) {
	// Parse templates with proper pattern matching
	tmpl, err := template.ParseFS(content,
		"templates/layouts/*.html",
		"templates/pages/*.html")
	if err != nil {
		return nil, err
	}

	return &Handler{
		db:        db,
		templates: tmpl,
	}, nil
}

// TemplateData holds common template data
type TemplateData struct {
	Title               string
	Description         string
	ServerTitle         string
	ServerTagline       string
	ServerDescription   string
	IsAuthenticated     bool
	IsAdmin             bool
	Username            string
	DisplayName         string
	Email               string
	Role                string
	ShowHeader          bool
	RegistrationEnabled bool
	Error               string
	Success             string
	Banner              *BannerData
}

// BannerData holds banner notification data
type BannerData struct {
	Type      string // error, warning, info, success
	Icon      string
	Message   string
	Action    string
	ActionURL string
}

// renderTemplate renders a template with data
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	err := h.templates.ExecuteTemplate(w, name, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetDefaultTemplateData returns default template data
func (h *Handler) GetDefaultTemplateData() TemplateData {
	return TemplateData{
		ServerTitle:         "gitmessages",
		ServerTagline:       "Random Git Commit Messages",
		ServerDescription:   "Get funny and sarcastic git commit messages for your projects. Over 5000+ unique messages with smart cycle tracking to avoid duplicates.",
		ShowHeader:          true,
		RegistrationEnabled: true, // TODO: Make configurable
	}
}
