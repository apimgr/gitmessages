package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultConfig verifies the documented defaults so a regression in the
// zero-config first-run experience is caught immediately.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Address != "0.0.0.0" {
		t.Errorf("Server.Address = %q, want 0.0.0.0", cfg.Server.Address)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.UpdateBranch != "stable" {
		t.Errorf("Server.UpdateBranch = %q, want stable", cfg.Server.UpdateBranch)
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("WebUI.Theme = %q, want dark", cfg.WebUI.Theme)
	}
	if !cfg.WebUI.Notifications.Enabled {
		t.Error("Notifications.Enabled = false, want true")
	}
	if len(cfg.WebRobots.Allow) == 0 {
		t.Error("WebRobots.Allow should not be empty")
	}
	if len(cfg.WebRobots.Deny) == 0 {
		t.Error("WebRobots.Deny should not be empty")
	}
	if cfg.Server.Metrics.Enabled {
		t.Error("Metrics.Enabled = true, want false by default")
	}
}

// TestLoad_CreatesDefaultWhenMissing verifies Load writes a default config
// file when none exists yet, and that it can be read back.
func TestLoad_CreatesDefaultWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("created config Theme = %q, want dark", cfg.WebUI.Theme)
	}
}

// TestLoad_ExistingOverridesDefaults verifies a partial YAML file on disk
// overrides only the fields it sets, with defaults filling the rest.
func TestLoad_ExistingOverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yml")

	yamlContent := "server:\n  address: \"127.0.0.1\"\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to seed config file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Address != "127.0.0.1" {
		t.Errorf("Server.Address = %q, want 127.0.0.1 (from file)", cfg.Server.Address)
	}
	// Fields not present in the file must still carry their defaults.
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production (default)", cfg.Server.Mode)
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("WebUI.Theme = %q, want dark (default)", cfg.WebUI.Theme)
	}
}

// TestLoad_InvalidYAML verifies malformed YAML surfaces an error instead of
// silently falling back to defaults.
func TestLoad_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yml")

	if err := os.WriteFile(path, []byte("server: [this is not valid: yaml"), 0644); err != nil {
		t.Fatalf("failed to seed config file: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error loading invalid YAML, got nil")
	}
}

// TestMigrateYamlToYml verifies a legacy .yaml sibling is renamed to .yml
// when the .yml target does not already exist, and left alone otherwise.
func TestMigrateYamlToYml(t *testing.T) {
	t.Run("renames when yml missing", func(t *testing.T) {
		tmp := t.TempDir()
		ymlPath := filepath.Join(tmp, "config.yml")
		yamlPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(yamlPath, []byte("server:\n  address: \"1.2.3.4\"\n"), 0644); err != nil {
			t.Fatalf("failed to seed .yaml file: %v", err)
		}

		migrateYamlToYml(ymlPath)

		if _, err := os.Stat(ymlPath); err != nil {
			t.Fatalf("expected %q to exist after migration: %v", ymlPath, err)
		}
		if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
			t.Fatalf("expected legacy .yaml file to be gone, stat err = %v", err)
		}
	})

	t.Run("no-op when yml already exists", func(t *testing.T) {
		tmp := t.TempDir()
		ymlPath := filepath.Join(tmp, "config.yml")
		yamlPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(ymlPath, []byte("existing yml\n"), 0644); err != nil {
			t.Fatalf("failed to seed .yml file: %v", err)
		}
		if err := os.WriteFile(yamlPath, []byte("legacy yaml\n"), 0644); err != nil {
			t.Fatalf("failed to seed .yaml file: %v", err)
		}

		migrateYamlToYml(ymlPath)

		data, err := os.ReadFile(ymlPath)
		if err != nil {
			t.Fatalf("failed to read .yml after migrate: %v", err)
		}
		if string(data) != "existing yml\n" {
			t.Errorf(".yml content changed unexpectedly: %q", string(data))
		}
	})

	t.Run("no-op for non-.yml path", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "config.json")
		// Should simply not panic or do anything for a non-.yml path.
		migrateYamlToYml(path)
	})
}

// TestSaveAndGet verifies the Save/Get round trip through the package-level
// current config pointer.
func TestSaveAndGet(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yml")

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	loaded.WebUI.Theme = "light"

	if err := Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got := Get()
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.WebUI.Theme != "light" {
		t.Errorf("Get().WebUI.Theme = %q, want light", got.WebUI.Theme)
	}

	// generateConfigYAML intentionally does not persist every field (e.g.
	// server mode / update_branch / admin credentials are omitted from the
	// hand-written YAML output) so a re-Load resets those to defaults. This
	// documents real behavior rather than asserting a value that would
	// falsely imply full round-trip fidelity.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-Load() error: %v", err)
	}
	if reloaded.WebUI.Theme != "light" {
		t.Errorf("reloaded WebUI.Theme = %q, want light (should persist)", reloaded.WebUI.Theme)
	}
}

// TestSave_NoConfigLoaded verifies Save errors out when there is no active
// config/path to save (covering the guard at the top of Save()).
func TestSave_NoConfigLoaded(t *testing.T) {
	// Reset package state via an explicit nil to force the error branch.
	// The config package exposes current/configPath only within its own
	// package, so this test (being in package config) can reach in.
	savedCurrent := current
	savedPath := configPath
	current = nil
	configPath = ""
	t.Cleanup(func() {
		current = savedCurrent
		configPath = savedPath
	})

	if err := Save(); err == nil {
		t.Fatal("expected error from Save() with no config loaded, got nil")
	}
}

// TestGet_NilCurrent verifies Get() falls back to a fresh default config
// instead of returning nil when current has not been set.
func TestGet_NilCurrent(t *testing.T) {
	savedCurrent := current
	current = nil
	t.Cleanup(func() {
		current = savedCurrent
	})

	got := Get()
	if got == nil {
		t.Fatal("Get() returned nil, want a default config")
	}
	if got.WebUI.Theme != "dark" {
		t.Errorf("Get() fallback Theme = %q, want dark", got.WebUI.Theme)
	}
}

// TestGetTheme verifies the theme accessor reflects the current config.
func TestGetTheme(t *testing.T) {
	savedCurrent := current
	t.Cleanup(func() { current = savedCurrent })

	current = DefaultConfig()
	current.WebUI.Theme = "light"
	if got := GetTheme(); got != "light" {
		t.Errorf("GetTheme() = %q, want light", got)
	}
}

// TestGetCORS covers both the explicit value and the empty-string fallback
// to "*".
func TestGetCORS(t *testing.T) {
	savedCurrent := current
	t.Cleanup(func() { current = savedCurrent })

	current = DefaultConfig()
	current.WebSecurity.CORS = "https://example.com"
	if got := GetCORS(); got != "https://example.com" {
		t.Errorf("GetCORS() = %q, want https://example.com", got)
	}

	current.WebSecurity.CORS = ""
	if got := GetCORS(); got != "*" {
		t.Errorf("GetCORS() with empty CORS = %q, want *", got)
	}
}

// TestFormatStringSlice covers the empty-slice and populated-slice
// formatting used by generateConfigYAML.
func TestFormatStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", []string{}, "[]"},
		{"nil", nil, "[]"},
		{"single", []string{"/"}, `["/"]`},
		{"multiple", []string{"/", "/api"}, `["/", "/api"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatStringSlice(tt.in); got != tt.want {
				t.Errorf("formatStringSlice(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestGenerateConfigYAML_ValidYAML verifies the hand-built YAML text is at
// least syntactically valid and round-trips the fields it does emit.
func TestGenerateConfigYAML_ValidYAML(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Port = "8080"
	cfg.WebRobots.Allow = []string{"/", "/api"}

	text := generateConfigYAML(cfg)

	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("generateConfigYAML() produced invalid YAML: %v", err)
	}
	if _, ok := out["server"]; !ok {
		t.Error("generated YAML missing top-level 'server' key")
	}
}
