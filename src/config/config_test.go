package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultConfig verifies the documented defaults so a regression in the
// zero-config first-run experience is caught immediately.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Address != "[::]" {
		t.Errorf("Server.Address = %q, want [::]", cfg.Server.Address)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.Update.Branch != "stable" {
		t.Errorf("Server.Update.Branch = %q, want stable", cfg.Server.Update.Branch)
	}
	if cfg.Web.UI.Theme != "dark" {
		t.Errorf("Web.UI.Theme = %q, want dark", cfg.Web.UI.Theme)
	}
	if len(cfg.Web.Robots.Allow) == 0 {
		t.Error("Web.Robots.Allow should not be empty")
	}
	if len(cfg.Web.Robots.Deny) == 0 {
		t.Error("Web.Robots.Deny should not be empty")
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %04o, want 0600", info.Mode().Perm())
	}
	if cfg.Web.UI.Theme != "dark" {
		t.Errorf("created config Theme = %q, want dark", cfg.Web.UI.Theme)
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
	if cfg.Web.UI.Theme != "dark" {
		t.Errorf("Web.UI.Theme = %q, want dark (default)", cfg.Web.UI.Theme)
	}
}

// TestLoad_InvalidYAML verifies malformed YAML is ignored and defaults remain usable.
func TestLoad_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yml")

	if err := os.WriteFile(path, []byte("server: [this is not valid: yaml"), 0644); err != nil {
		t.Fatalf("failed to seed config file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() should keep the server available: %v", err)
	}
	if cfg.Server.Mode != "production" {
		t.Fatalf("invalid file changed server mode to %q", cfg.Server.Mode)
	}
}

func TestLoad_UnknownSetting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  misspelled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("unknown configuration keys must not prevent startup: %v", err)
	}
}

func TestSaveSMTPDetectionPersistsRelayToNotificationsArea(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := SaveSMTPDetection("mail.example.test", 587); err != nil {
		t.Fatal(err)
	}
	if got := Get().Server.Notifications.Email.SMTP.Host; got != "mail.example.test" {
		t.Fatalf("current SMTP host = %q", got)
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(path), "yaml.d", "notifications.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(contents, &values); err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "mail.example.test") || !strings.Contains(string(encoded), "587") {
		t.Fatalf("notifications area did not persist SMTP relay: %s", contents)
	}
}

func TestLoad_InvalidSetting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"70000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("invalid setting should use its default: %v", err)
	}
	if cfg.Server.Port == "70000" {
		t.Fatal("invalid server.port was not replaced with the default")
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

func TestLoadGeneratesPrivateMetricsTokens(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"8080\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	tokens := cfg.Server.Metrics.Auth.Tokens
	if tokens.Prometheus == "" || tokens.Grafana == "" || tokens.Loki == "" {
		t.Fatalf("metrics service tokens must be generated: %#v", tokens)
	}
	info, err := os.Stat(filepath.Join(tmp, "secrets.yml"))
	if err != nil {
		t.Fatalf("stat secrets.yml: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("secrets.yml mode = %04o, want 0600", info.Mode().Perm())
	}
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
	loaded.Server.Mode = "development"

	if err := Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got := Get()
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Server.Mode != "development" {
		t.Errorf("Get().Server.Mode = %q, want development", got.Server.Mode)
	}

	// Node settings live in server.yml, while area settings are saved separately.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-Load() error: %v", err)
	}
	if reloaded.Server.Mode != "development" {
		t.Errorf("reloaded Server.Mode = %q, want development", reloaded.Server.Mode)
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
	if got.Web.UI.Theme != "dark" {
		t.Errorf("Get() fallback Theme = %q, want dark", got.Web.UI.Theme)
	}
}

// TestGetTheme verifies the theme accessor reflects the current config.
func TestGetTheme(t *testing.T) {
	savedCurrent := current
	t.Cleanup(func() { current = savedCurrent })

	current = DefaultConfig()
	current.Web.UI.Theme = "light"
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
	current.Server.CORS.AllowedOrigins = []string{"https://example.com"}
	if got := GetCORS(); got != "https://example.com" {
		t.Errorf("GetCORS() = %q, want https://example.com", got)
	}

	current.Server.CORS.AllowedOrigins = nil
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
	cfg.Web.Robots.Allow = []string{"/", "/api"}

	text := generateConfigYAML(cfg)

	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("generateConfigYAML() produced invalid YAML: %v", err)
	}
	if _, ok := out["server"]; !ok {
		t.Error("generated YAML missing top-level 'server' key")
	}
}

func TestLoadMigratesAreaSettings(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	legacy := "server:\n  port: \"8080\"\n  update:\n    branch: beta\nweb:\n  ui:\n    theme: light\n  robots:\n    allow: [\"/\"]\n"
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != "8080" || cfg.Server.Update.Branch != "beta" || cfg.Web.UI.Theme != "light" {
		t.Fatalf("merged config lost migrated settings: %#v", cfg)
	}
	for _, file := range []string{"maintenance.yml", "branding.yml", "web.yml"} {
		if _, err := os.Stat(filepath.Join(tmp, "yaml.d", file)); err != nil {
			t.Errorf("expected migrated area file %s: %v", file, err)
		}
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("migration did not create backup: %v", err)
	}
	var node map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatal(err)
	}
	if len(node) != 1 || node["web"] != nil {
		t.Fatalf("server.yml still contains area settings: %#v", node)
	}
}

func TestLoadMovesSecretsOutOfAreaFiles(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  token: xxxxx\n  mode: production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(tmp, "secrets.yml")
	secretData, err := os.ReadFile(secretPath)
	if err != nil || !strings.Contains(string(secretData), "xxxxx") {
		t.Fatalf("secret was not migrated: %s, %v", secretData, err)
	}
	for _, file := range []string{path, filepath.Join(tmp, "yaml.d", "app.yml")} {
		data, err := os.ReadFile(file)
		if err == nil && strings.Contains(string(data), "xxxxx") {
			t.Errorf("secret remains in non-secret file %s", file)
		}
	}
	if info, err := os.Stat(secretPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("secrets.yml mode/stat = %v, %v", info, err)
	}
}

func TestSaveUpdateWritesMaintenanceArea(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.yml")
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	current.Server.Update.Branch = "beta"
	if err := SaveUpdate(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "yaml.d", "maintenance.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "branch: beta") {
		t.Fatalf("maintenance area does not contain update branch: %s", data)
	}
}

func TestParseBool(t *testing.T) {
	for _, value := range []string{"1", "YES", "True", "enable", "enabled", "on", "yep", "yup", "yeah", "affirmative", "aye", "si", "oui", "da", "hai", "totally", "sure", "ok", "accept", "allow", "grant", "y", "t"} {
		got, err := ParseBool(value, false)
		if err != nil || !got {
			t.Errorf("ParseBool(%q) = %v, %v; want true", value, got, err)
		}
	}
	for _, value := range []string{"0", "NO", "False", "disable", "disabled", "off", "nope", "nah", "nay", "negative", "nein", "non", "niet", "iie", "lie", "noway", "never", "deny", "reject", "block", "revoke", "n", "f"} {
		got, err := ParseBool(value, true)
		if err != nil || got {
			t.Errorf("ParseBool(%q) = %v, %v; want false", value, got, err)
		}
	}
	if got, err := ParseBool(" ", true); err != nil || !got {
		t.Fatalf("empty ParseBool() = %v, %v; want default true", got, err)
	}
	if _, err := ParseBool("maybe", false); err == nil {
		t.Fatal("invalid boolean value was accepted")
	}
	if !IsTruthy("oui") || IsTruthy("invalid") || !MustParseBool("yes", false) {
		t.Fatal("truthy helpers returned incorrect values")
	}
}

func TestValidationFallsBackForInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Config)
		check func(*testing.T, *Config)
	}{
		{"port", func(cfg *Config) { cfg.Server.Port = "70000" }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Port == "70000" {
				t.Fatal("invalid port was retained")
			}
		}},
		{"address", func(cfg *Config) { cfg.Server.Address = "invalid" }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Address != DefaultConfig().Server.Address {
				t.Fatal("invalid address did not reset")
			}
		}},
		{"mode", func(cfg *Config) { cfg.Server.Mode = "invalid" }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Mode != "production" {
				t.Fatal("invalid mode did not reset")
			}
		}},
		{"branch", func(cfg *Config) { cfg.Server.Update.Branch = "invalid" }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Update.Branch != "stable" {
				t.Fatal("invalid branch did not reset")
			}
		}},
		{"defer", func(cfg *Config) { cfg.Server.Update.DeferDays = 999 }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Update.DeferDays != 0 {
				t.Fatal("invalid defer window did not reset")
			}
		}},
		{"theme", func(cfg *Config) { cfg.Web.UI.Theme = "invalid" }, func(t *testing.T, cfg *Config) {
			if cfg.Web.UI.Theme != "dark" {
				t.Fatal("invalid theme did not reset")
			}
		}},
		{"cors", func(cfg *Config) { cfg.Server.CORS.AllowedOrigins = []string{"javascript:alert(1)"} }, func(t *testing.T, cfg *Config) {
			if len(cfg.Server.CORS.AllowedOrigins) != 1 || cfg.Server.CORS.AllowedOrigins[0] != "*" {
				t.Fatal("invalid CORS origins did not reset")
			}
		}},
		{"tor", func(cfg *Config) { cfg.Server.Tor.MaxCircuits = 0 }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Tor.MaxCircuits != 32 {
				t.Fatal("invalid Tor config did not reset")
			}
		}},
		{"i2p", func(cfg *Config) { cfg.Server.I2P.SignatureType = 0 }, func(t *testing.T, cfg *Config) {
			if cfg.Server.I2P.SignatureType != 7 {
				t.Fatal("invalid I2P config did not reset")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			test.edit(cfg)
			if err := validate(cfg); err != nil {
				t.Fatal(err)
			}
			test.check(t, cfg)
		})
	}
}
