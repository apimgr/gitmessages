package mode

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Mode represents the application execution mode
type Mode string

const (
	// Production mode - optimized for production use
	Production Mode = "production"
	// Development mode enables development-oriented logging and reload behavior.
	Development Mode = "development"
	// Debug mode explicitly opts in to maximum diagnostics.
	Debug Mode = "debug"
)

var (
	currentMode          Mode = Production
	debugEnabled         bool
	blockProfileRate     = 1
	mutexProfileFraction = 1
	mu                   sync.RWMutex
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

// SetDebugEnabled sets the independent debug diagnostics switch.
func SetDebugEnabled(enabled bool) {
	mu.Lock()
	debugEnabled = enabled
	blockRate := blockProfileRate
	mutexFraction := mutexProfileFraction
	mu.Unlock()
	applyProfiling(enabled, blockRate, mutexFraction)
}

// SetProfilingRates configures runtime profiling rates used while debug diagnostics are enabled.
func SetProfilingRates(blockRate, mutexFraction int) {
	if blockRate < 0 {
		blockRate = 0
	}
	if mutexFraction < 0 {
		mutexFraction = 0
	}
	mu.Lock()
	blockProfileRate = blockRate
	mutexProfileFraction = mutexFraction
	enabled := debugEnabled
	mu.Unlock()
	applyProfiling(enabled, blockRate, mutexFraction)
}

func applyProfiling(enabled bool, blockRate, mutexFraction int) {
	if !enabled {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
		return
	}
	runtime.SetBlockProfileRate(blockRate)
	runtime.SetMutexProfileFraction(mutexFraction)
}

// IsDebugEnabled reports whether debug diagnostics were explicitly enabled.
func IsDebugEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return debugEnabled
}

// ParseMode converts a string to a Mode constant
// Accepts: "dev", "development", "prod", "production"
// Returns Production mode for unrecognized values
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dev", "devel", "development":
		return Development
	case "debug":
		return Debug
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
	if value, exists := os.LookupEnv("DEBUG"); exists && value != "" {
		SetDebugEnabled(isTruthy(value))
	} else {
		SetDebugEnabled(m == Debug)
	}
}

// InitializeWithConfig applies the documented CLI, environment, config, default priority.
func InitializeWithConfig(flagMode, configMode string, debugFlag *bool) {
	selected := flagMode
	if selected == "" {
		selected = os.Getenv("MODE")
	}
	if selected == "" {
		selected = configMode
	}
	if selected == "" {
		selected = string(Production)
	}
	m := ParseMode(selected)
	Set(m)
	if debugFlag != nil {
		SetDebugEnabled(*debugFlag)
		return
	}
	if value, exists := os.LookupEnv("DEBUG"); exists && value != "" {
		SetDebugEnabled(isTruthy(value))
		return
	}
	SetDebugEnabled(m == Debug)
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

// GetErrorDetail returns error details based on the current mode
// Development: full error details with stack traces
// Production: generic error message without internal details
func GetErrorDetail(err error) string {
	if err == nil {
		return ""
	}

	if IsDevelopment() || Get() == Debug {
		// In development, return full error details
		return err.Error()
	}

	// In production, return generic error message
	return "An internal error occurred"
}

// ShouldShowDebugEndpoints returns whether the explicit debug flag is enabled.
func ShouldShowDebugEndpoints() bool {
	return IsDebugEnabled()
}

// GetCacheHeaders returns appropriate cache control headers based on mode
// Development: no-cache headers to ensure fresh content
// Production: cache headers for optimal performance
func GetCacheHeaders() map[string]string {
	if IsDevelopment() || Get() == Debug {
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
	if IsDevelopment() || Get() == Debug {
		return "debug"
	}
	return "info"
}

// ShouldEnableAutoReload returns whether auto-reload should be enabled
// Development: true (watch for file changes)
// Production: false (no file watching)
func ShouldEnableAutoReload() bool {
	return IsDevelopment() || Get() == Debug
}

// ShouldEnableProfiling returns whether profiling is explicitly enabled.
func ShouldEnableProfiling() bool {
	return IsDebugEnabled()
}

// GetPanicRecoveryMode returns how panics should be handled
// Development: "verbose" (full stack trace in response)
// Production: "graceful" (log error, return 500, continue)
func GetPanicRecoveryMode() string {
	if IsDevelopment() || Get() == Debug {
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
