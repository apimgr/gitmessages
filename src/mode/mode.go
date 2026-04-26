package mode

import (
	"fmt"
	"os"
	"sync"
)

// Mode represents the application execution mode
type Mode string

const (
	// Production mode - optimized for production use
	Production Mode = "production"
	// Development mode - optimized for development with debugging features
	Development Mode = "development"

	appName = "gitmessages"
)

var (
	currentMode Mode = Production
	mu          sync.RWMutex
)

// Get returns the current application mode
func Get() Mode {
	mu.RLock()
	defer mu.RUnlock()
	return currentMode
}

// Set sets the application mode
func Set(m Mode) {
	mu.Lock()
	defer mu.Unlock()
	currentMode = m
}

// ParseMode converts a string to a Mode constant
// Accepts: "dev", "development", "prod", "production"
// Returns Production mode for unrecognized values
func ParseMode(s string) Mode {
	switch s {
	case "dev", "development":
		return Development
	case "prod", "production":
		return Production
	default:
		return Production
	}
}

// IsDevelopment returns true if running in development mode
func IsDevelopment() bool {
	return Get() == Development
}

// IsProduction returns true if running in production mode
func IsProduction() bool {
	return Get() == Production
}

// Initialize sets up the mode based on priority:
// 1. Explicitly set via Set() (e.g., from --mode flag)
// 2. MODE environment variable
// 3. Default to Production
func Initialize(flagMode string) {
	var m Mode

	// Priority 1: CLI flag (if provided)
	if flagMode != "" {
		m = ParseMode(flagMode)
	} else if envMode := os.Getenv("MODE"); envMode != "" {
		// Priority 2: Environment variable
		m = ParseMode(envMode)
	} else {
		// Priority 3: Default
		m = Production
	}

	Set(m)
}

// GetErrorDetail returns error details based on the current mode
// Development: full error details with stack traces
// Production: generic error message without internal details
func GetErrorDetail(err error) string {
	if err == nil {
		return ""
	}

	if IsDevelopment() {
		// In development, return full error details
		return err.Error()
	}

	// In production, return generic error message
	return "An internal error occurred"
}

// ShouldShowDebugEndpoints returns whether debug endpoints should be enabled
// Development: true (enables /debug/pprof/*, /debug/vars)
// Production: false (returns 404 for /debug/* routes)
func ShouldShowDebugEndpoints() bool {
	return IsDevelopment()
}

// GetCacheHeaders returns appropriate cache control headers based on mode
// Development: no-cache headers to ensure fresh content
// Production: cache headers for optimal performance
func GetCacheHeaders() map[string]string {
	if IsDevelopment() {
		return map[string]string{
			"Cache-Control": "no-cache, no-store, must-revalidate",
			"Pragma":        "no-cache",
			"Expires":       "0",
		}
	}

	// Production: cache static files for 1 year
	return map[string]string{
		"Cache-Control": "public, max-age=31536000, immutable",
	}
}

// ShouldCacheTemplates returns whether templates should be cached
// Development: false (reload templates on each request)
// Production: true (cache parsed templates)
func ShouldCacheTemplates() bool {
	return IsProduction()
}

// ShouldCacheStaticFiles returns whether static files should be cached
// Development: false (always serve fresh files)
// Production: true (cache static file content)
func ShouldCacheStaticFiles() bool {
	return IsProduction()
}

// GetLogLevel returns the appropriate log level for the current mode
// Development: "debug"
// Production: "info"
func GetLogLevel() string {
	if IsDevelopment() {
		return "debug"
	}
	return "info"
}

// ShouldEnableAutoReload returns whether auto-reload should be enabled
// Development: true (watch for file changes)
// Production: false (no file watching)
func ShouldEnableAutoReload() bool {
	return IsDevelopment()
}

// ShouldEnableProfiling returns whether profiling endpoints should be available
// Development: true (profiling available at /debug/pprof/*)
// Production: false (profiling disabled)
func ShouldEnableProfiling() bool {
	return IsDevelopment()
}

// GetPanicRecoveryMode returns how panics should be handled
// Development: "verbose" (full stack trace in response)
// Production: "graceful" (log error, return 500, continue)
func GetPanicRecoveryMode() string {
	if IsDevelopment() {
		return "verbose"
	}
	return "graceful"
}

// String returns the string representation of the mode
func (m Mode) String() string {
	return string(m)
}

// MarshalText implements encoding.TextMarshaler
func (m Mode) MarshalText() ([]byte, error) {
	return []byte(m.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler
func (m *Mode) UnmarshalText(text []byte) error {
	parsed := ParseMode(string(text))
	*m = parsed
	return nil
}

// GetModeInfo returns detailed information about the current mode configuration
func GetModeInfo() string {
	mode := Get()
	return fmt.Sprintf(`Application Mode: %s
Logging Level: %s
Debug Endpoints: %v
Template Caching: %v
Static File Caching: %v
Auto-reload: %v
Profiling: %v
Panic Recovery: %s`,
		mode,
		GetLogLevel(),
		ShouldShowDebugEndpoints(),
		ShouldCacheTemplates(),
		ShouldCacheStaticFiles(),
		ShouldEnableAutoReload(),
		ShouldEnableProfiling(),
		GetPanicRecoveryMode(),
	)
}
