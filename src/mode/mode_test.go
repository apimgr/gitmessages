package mode

import (
	"errors"
	"os"
	"testing"
)

// resetMode restores the package-level mode to Production after a test, so
// tests do not leak state into each other via the shared global.
func resetMode(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		Set(Production)
	})
}

// TestParseMode covers every recognized alias plus the fallback for
// unrecognized/empty input.
func TestParseMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Mode
	}{
		{"dev short", "dev", Development},
		{"dev long", "development", Development},
		{"prod short", "prod", Production},
		{"prod long", "production", Production},
		{"empty defaults to production", "", Production},
		{"unknown defaults to production", "bogus", Production},
		{"case sensitive - uppercase not matched", "DEV", Production},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseMode(tt.in); got != tt.want {
				t.Errorf("ParseMode(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestGetSet verifies the basic getter/setter round trip.
func TestGetSet(t *testing.T) {
	resetMode(t)

	Set(Development)
	if Get() != Development {
		t.Fatalf("Get() = %v, want %v", Get(), Development)
	}

	Set(Production)
	if Get() != Production {
		t.Fatalf("Get() = %v, want %v", Get(), Production)
	}
}

// TestIsDevelopmentIsProduction checks the two convenience predicates agree
// with Get() and are mutually exclusive.
func TestIsDevelopmentIsProduction(t *testing.T) {
	resetMode(t)

	Set(Development)
	if !IsDevelopment() {
		t.Error("IsDevelopment() = false, want true after Set(Development)")
	}
	if IsProduction() {
		t.Error("IsProduction() = true, want false after Set(Development)")
	}

	Set(Production)
	if IsDevelopment() {
		t.Error("IsDevelopment() = true, want false after Set(Production)")
	}
	if !IsProduction() {
		t.Error("IsProduction() = false, want true after Set(Production)")
	}
}

// TestInitialize_Priority verifies the documented precedence: explicit
// flag > MODE env var > default Production.
func TestInitialize_Priority(t *testing.T) {
	resetMode(t)

	t.Run("flag wins over env", func(t *testing.T) {
		t.Setenv("MODE", "production")
		Initialize("development")
		if Get() != Development {
			t.Fatalf("Get() = %v, want %v (flag should win)", Get(), Development)
		}
	})

	t.Run("env used when flag empty", func(t *testing.T) {
		t.Setenv("MODE", "development")
		Initialize("")
		if Get() != Development {
			t.Fatalf("Get() = %v, want %v (env should apply)", Get(), Development)
		}
	})

	t.Run("default production when neither set", func(t *testing.T) {
		os.Unsetenv("MODE")
		Initialize("")
		if Get() != Production {
			t.Fatalf("Get() = %v, want %v (default)", Get(), Production)
		}
	})
}

// TestGetErrorDetail verifies development mode leaks the raw error while
// production mode returns a generic message, and nil errors are handled.
func TestGetErrorDetail(t *testing.T) {
	resetMode(t)

	if got := GetErrorDetail(nil); got != "" {
		t.Errorf("GetErrorDetail(nil) = %q, want empty string", got)
	}

	err := errors.New("boom: disk full")

	Set(Development)
	if got := GetErrorDetail(err); got != "boom: disk full" {
		t.Errorf("GetErrorDetail() in dev = %q, want raw error text", got)
	}

	Set(Production)
	if got := GetErrorDetail(err); got != "An internal error occurred" {
		t.Errorf("GetErrorDetail() in prod = %q, want generic message", got)
	}
}

// TestModeGatedBehaviors is table-driven across every simple mode-gated
// function to catch any accidental inversion of dev/prod behavior.
func TestModeGatedBehaviors(t *testing.T) {
	resetMode(t)

	tests := []struct {
		name string
		fn   func() bool
		dev  bool
		prod bool
	}{
		{"ShouldShowDebugEndpoints", ShouldShowDebugEndpoints, true, false},
		{"ShouldCacheTemplates", ShouldCacheTemplates, false, true},
		{"ShouldCacheStaticFiles", ShouldCacheStaticFiles, false, true},
		{"ShouldEnableAutoReload", ShouldEnableAutoReload, true, false},
		{"ShouldEnableProfiling", ShouldEnableProfiling, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Set(Development)
			if got := tt.fn(); got != tt.dev {
				t.Errorf("%s() in dev = %v, want %v", tt.name, got, tt.dev)
			}
			Set(Production)
			if got := tt.fn(); got != tt.prod {
				t.Errorf("%s() in prod = %v, want %v", tt.name, got, tt.prod)
			}
		})
	}
}

// TestGetCacheHeaders checks the two distinct header sets returned per mode.
func TestGetCacheHeaders(t *testing.T) {
	resetMode(t)

	Set(Development)
	dev := GetCacheHeaders()
	if dev["Cache-Control"] != "no-cache, no-store, must-revalidate" {
		t.Errorf("dev Cache-Control = %q", dev["Cache-Control"])
	}
	if dev["Pragma"] != "no-cache" {
		t.Errorf("dev Pragma = %q", dev["Pragma"])
	}

	Set(Production)
	prod := GetCacheHeaders()
	if prod["Cache-Control"] != "public, max-age=31536000, immutable" {
		t.Errorf("prod Cache-Control = %q", prod["Cache-Control"])
	}
	if _, ok := prod["Pragma"]; ok {
		t.Error("prod headers should not include Pragma")
	}
}

// TestGetLogLevel and TestGetPanicRecoveryMode check the remaining
// mode-gated string getters.
func TestGetLogLevel(t *testing.T) {
	resetMode(t)

	Set(Development)
	if got := GetLogLevel(); got != "debug" {
		t.Errorf("GetLogLevel() dev = %q, want debug", got)
	}
	Set(Production)
	if got := GetLogLevel(); got != "info" {
		t.Errorf("GetLogLevel() prod = %q, want info", got)
	}
}

func TestGetPanicRecoveryMode(t *testing.T) {
	resetMode(t)

	Set(Development)
	if got := GetPanicRecoveryMode(); got != "verbose" {
		t.Errorf("GetPanicRecoveryMode() dev = %q, want verbose", got)
	}
	Set(Production)
	if got := GetPanicRecoveryMode(); got != "graceful" {
		t.Errorf("GetPanicRecoveryMode() prod = %q, want graceful", got)
	}
}

// TestModeString verifies String() and MarshalText/UnmarshalText round-trip.
func TestModeString(t *testing.T) {
	if Development.String() != "development" {
		t.Errorf("Development.String() = %q", Development.String())
	}
	if Production.String() != "production" {
		t.Errorf("Production.String() = %q", Production.String())
	}
}

func TestMarshalUnmarshalText(t *testing.T) {
	b, err := Development.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error: %v", err)
	}
	if string(b) != "development" {
		t.Fatalf("MarshalText() = %q, want development", string(b))
	}

	var m Mode
	if err := m.UnmarshalText([]byte("dev")); err != nil {
		t.Fatalf("UnmarshalText() error: %v", err)
	}
	if m != Development {
		t.Fatalf("UnmarshalText(\"dev\") = %v, want %v", m, Development)
	}

	// Unrecognized text falls back to Production per ParseMode's contract.
	var m2 Mode
	if err := m2.UnmarshalText([]byte("nonsense")); err != nil {
		t.Fatalf("UnmarshalText() error: %v", err)
	}
	if m2 != Production {
		t.Fatalf("UnmarshalText(\"nonsense\") = %v, want %v", m2, Production)
	}
}

// TestGetModeInfo is a light smoke test: it must include the current mode
// and not panic while formatting every field.
func TestGetModeInfo(t *testing.T) {
	resetMode(t)

	Set(Development)
	info := GetModeInfo()
	if info == "" {
		t.Fatal("GetModeInfo() returned empty string")
	}
	if !contains(info, "development") {
		t.Errorf("GetModeInfo() = %q, expected to mention development", info)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
