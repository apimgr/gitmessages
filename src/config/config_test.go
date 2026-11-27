package config

import (
	"encoding/base64"
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
	if cfg.Server.Compliance.AnyEnabled() {
		t.Fatal("compliance standards must be disabled by default")
	}

	if cfg.Server.Address != "[::]" {
		t.Errorf("Server.Address = %q, want [::]", cfg.Server.Address)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.APIVersion != "v1" {
		t.Errorf("Server.APIVersion = %q, want v1", cfg.Server.APIVersion)
	}
	if cfg.Server.Update.Branch != "stable" {
		t.Errorf("Server.Update.Branch = %q, want stable", cfg.Server.Update.Branch)
	}
	if cfg.Server.Security.Reports.RetentionDays != 365 {
		t.Errorf("Server.Security.Reports.RetentionDays = %d, want 365", cfg.Server.Security.Reports.RetentionDays)
	}
	if !cfg.Server.GeoIP.Enabled || cfg.Server.Scheduler.Timezone != "America/New_York" || cfg.Server.Scheduler.Tasks["backup_hourly"].Enabled || !cfg.Server.Scheduler.Tasks["database_check"].Enabled || cfg.Server.Scheduler.Tasks["database_check"].Schedule != "@every 1m" || !cfg.Server.Scheduler.Tasks["health_stats_flush"].Enabled || cfg.Server.Scheduler.Tasks["health_stats_flush"].Schedule != "@every 30s" {
		t.Errorf("security/scheduler defaults do not match the service defaults: GeoIP=%+v Scheduler=%+v", cfg.Server.GeoIP, cfg.Server.Scheduler)
	}
	if !cfg.Server.Notifications.DiskLow.Enabled || cfg.Server.Notifications.DiskLow.ThresholdPercent != 85 || cfg.Server.Notifications.DiskLow.Repeat != "24h" {
		t.Errorf("disk-low notification defaults do not match the service defaults: %+v", cfg.Server.Notifications.DiskLow)
	}
	if cfg.Server.Notifications.WebUI.Position != "top-right" || cfg.Server.Notifications.WebUI.Duration != 3 || cfg.Server.Notifications.WebUI.WarningDuration != 5 {
		t.Errorf("web notification defaults do not match the page contract: %+v", cfg.Server.Notifications.WebUI)
	}
	if cfg.Server.Maintenance.SelfHealing.RetryInterval != "30s" || cfg.Server.Maintenance.SelfHealing.MaxAttempts != 0 || cfg.Server.Maintenance.Cleanup.DiskThreshold != 90 || cfg.Server.Maintenance.Cleanup.LogRetentionDays != 7 || !cfg.Server.Maintenance.Notify.OnEnter || !cfg.Server.Maintenance.Notify.OnExit {
		t.Errorf("maintenance defaults do not match the server recovery contract: %+v", cfg.Server.Maintenance)
	}
	if cfg.Data.CVE.Source == "" || cfg.Data.Trivy.Source == "" || !cfg.Server.Security.Blocklists.Enabled {
		t.Errorf("security feed defaults are incomplete: data=%+v blocklists=%+v", cfg.Data, cfg.Server.Security.Blocklists)
	}
	if cfg.Web.UI.Theme != "dark" {
		t.Errorf("Web.UI.Theme = %q, want dark", cfg.Web.UI.Theme)
	}
	if !cfg.Server.CSRF.Enabled || cfg.Server.CSRF.TokenLength != 32 || cfg.Server.CSRF.CookieName != "csrf_token" || cfg.Server.CSRF.HeaderName != "X-CSRF-Token" || cfg.Server.CSRF.Secure != "auto" {
		t.Errorf("CSRF defaults do not match the frontend token contract: %+v", cfg.Server.CSRF)
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
	privacy := cfg.Server.Privacy
	if privacy.Data.Sold || !privacy.Data.StoredOnServer || privacy.Retention.Period != "" || !privacy.Retention.ExportAvailable || !privacy.Retention.DeletionAvailable {
		t.Error("privacy data and retention defaults are incomplete")
	}
	if got := privacy.Retention.PeriodForLanguage("en"); got == "privacy.retention.period" || strings.TrimSpace(got) == "" {
		t.Errorf("built-in privacy retention text is missing: %q", got)
	}
	localizedRetention := PrivacyRetentionConfig{Period: map[string]any{"fr": "Texte français", "default": "Default text"}}
	if got := localizedRetention.PeriodForLanguage("fr"); got != "Texte français" {
		t.Errorf("localized retention = %q, want French text", got)
	}
	if got := localizedRetention.PeriodForLanguage("de"); got != "Default text" {
		t.Errorf("default retention fallback = %q, want default text", got)
	}
	if !privacy.Cookies.Essential.Enabled || !privacy.Consent.ShowUntilAcknowledged || privacy.Consent.Policy.URL != "/server/privacy" {
		t.Error("privacy consent and cookie defaults are incomplete")
	}
	if privacy.GetConsentMessage() == "" || privacy.GetAnalyticsDescription() == "" || privacy.GetDataUsageContent() == "" || privacy.IsCCPAApplicable() {
		t.Error("privacy dynamic content does not match the default not-sold policy")
	}
}

func TestOutputPreferencesPreserveAutomaticDefaults(t *testing.T) {
	configuration := DefaultConfig()
	if configuration.Output.Color != nil || configuration.Output.Emoji != nil {
		t.Fatal("unset output preferences should remain automatic")
	}
	if err := yaml.Unmarshal([]byte("output:\n  color: false\n  emoji: true\n"), configuration); err != nil {
		t.Fatalf("decode output preferences: %v", err)
	}
	if configuration.Output.Color == nil || *configuration.Output.Color || configuration.Output.Emoji == nil || !*configuration.Output.Emoji {
		t.Fatalf("decoded output preferences = %#v", configuration.Output)
	}
}

func TestI18NAndEmailEventSettingsLoadAndValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte("server:\n  i18n:\n    default_language: es\n    available_languages: [en, es, es, unsupported]\n  notifications:\n    email:\n      events:\n        startup: true\n        backup_failed: false\n"), cfg); err != nil {
		t.Fatal(err)
	}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.I18N.DefaultLanguage != "es" || strings.Join(cfg.Server.I18N.AvailableLanguages, ",") != "en,es" {
		t.Fatalf("validated i18n config = %+v", cfg.Server.I18N)
	}
	if !cfg.Server.Notifications.Email.Events["startup"] || cfg.Server.Notifications.Email.Events["backup_failed"] || !cfg.Server.Notifications.Email.Events["scheduler_error"] {
		t.Fatalf("email event defaults/overrides = %+v", cfg.Server.Notifications.Email.Events)
	}
	cfg.Server.I18N.DefaultLanguage = "unsupported"
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.I18N.DefaultLanguage != "en" {
		t.Fatalf("unsupported default language = %q, want en", cfg.Server.I18N.DefaultLanguage)
	}
}

func TestMalformedSMTPSettingsFallBackToDefaults(t *testing.T) {
	configuration := DefaultConfig()
	if err := yaml.Unmarshal([]byte("server:\n  notifications:\n    email:\n      smtp:\n        port: not-a-port\n        tls: invalid-mode\n"), configuration); err != nil {
		t.Fatalf("malformed SMTP values prevented config loading: %v", err)
	}
	if err := validate(configuration); err != nil {
		t.Fatalf("malformed SMTP values prevented config validation: %v", err)
	}
	if configuration.Server.Notifications.Email.SMTP.Port != 587 || configuration.Server.Notifications.Email.SMTP.TLS != "auto" {
		t.Fatalf("SMTP defaults after malformed input = %+v", configuration.Server.Notifications.Email.SMTP)
	}
	t.Setenv("SMTP_PORT", "invalid")
	if err := applySMTPEnvironment(configuration); err != nil {
		t.Fatalf("malformed SMTP_PORT prevented config loading: %v", err)
	}
	if configuration.Server.Notifications.Email.SMTP.Port != 587 {
		t.Fatalf("SMTP_PORT fallback = %d, want 587", configuration.Server.Notifications.Email.SMTP.Port)
	}
}

func TestInvalidEmailReplyToFallsBackToUnset(t *testing.T) {
	for _, value := range []string{"not-an-address", "operator@example.test\r\nBcc: attacker@example.test"} {
		configuration := DefaultConfig()
		configuration.Server.Notifications.Email.ReplyTo = value
		if err := validate(configuration); err != nil {
			t.Fatalf("invalid reply_to %q prevented config validation: %v", value, err)
		}
		if configuration.Server.Notifications.Email.ReplyTo != "" {
			t.Errorf("invalid reply_to %q was not cleared", value)
		}
	}
}

func TestComplianceStandardsLoadIndividually(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("server:\n  compliance:\n    gdpr: true\n    pci_dss: true\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Server.Compliance.GDPR || !cfg.Server.Compliance.PCIDSS || cfg.Server.Compliance.CCPA || !cfg.Server.Compliance.AnyEnabled() {
		t.Fatalf("compliance settings were not loaded independently: %+v", cfg.Server.Compliance)
	}
}

func TestSEOConfigurationValidationUsesSafeDefaults(t *testing.T) {
	configuration := DefaultConfig()
	configuration.Server.SEO.TitleSeparator = "123456789"
	configuration.Server.SEO.Canonical = "http://untrusted.example"
	configuration.Server.SEO.Twitter.Card = "unknown"
	configuration.Server.SEO.MetaTags = map[string]string{
		"description":  "duplicate generated tag",
		"invalid name": "invalid key",
		"valid.name":   "safe value",
	}
	configuration.Server.SEO.Verification.Google = "<script>"
	configuration.Server.SEO.Verification.Custom = []SEOCustomMetaConfig{
		{Name: "safe-name", Content: "valid"},
		{Name: "unsafe name", Content: "invalid"},
	}
	validateSEOSettings(configuration)
	if configuration.Server.SEO.TitleSeparator != " - " || configuration.Server.SEO.Canonical != "" || configuration.Server.SEO.Twitter.Card != "summary_large_image" {
		t.Fatalf("SEO settings did not fall back safely: %+v", configuration.Server.SEO)
	}
	if len(configuration.Server.SEO.MetaTags) != 1 || configuration.Server.SEO.MetaTags["valid.name"] != "safe value" {
		t.Fatalf("invalid or reserved meta tags were retained: %#v", configuration.Server.SEO.MetaTags)
	}
	if configuration.Server.SEO.Verification.Google != "" || len(configuration.Server.SEO.Verification.Custom) != 1 || configuration.Server.SEO.Verification.Custom[0].Name != "safe-name" {
		t.Fatalf("invalid verification values were retained: %+v", configuration.Server.SEO.Verification)
	}
}

func TestLoadSecurityFeedAndSchedulerConfiguration(t *testing.T) {
	directory := projectTestDir(t)
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(configPath, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	areaDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(areaDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	security := "server:\n  geoip:\n    enabled: false\n    deny_countries: [CN]\n  security:\n    reports:\n      retention_days: 180\n    allowlist:\n      - cidr: 192.0.2.1\n        description: monitor\n    blocklists:\n      enabled: true\n      extra_sources:\n        - id: example\n          type: domain\n          url: https://feeds.example.test/domains.txt\n          enabled: true\n        - id: disabled_feed\n          type: ip\n          url: https://feeds.example.test/disabled.txt\n          enabled: false\n        - id: default_enabled\n          type: ip\n          url: https://feeds.example.test/default.txt\ndata:\n  cve:\n    source: https://nvd.example.test/feed\n  trivy:\n    enabled: true\n    source: ghcr.io/example/trivy-db:2\n"
	maintenance := "server:\n  scheduler:\n    timezone: UTC\n    catch_up_window: 2h\n    tasks:\n      backup_hourly:\n        enabled: true\n"
	if err := os.WriteFile(filepath.Join(areaDirectory, "security.yml"), []byte(security), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(areaDirectory, "maintenance.yml"), []byte(maintenance), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadMergedConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.GeoIP.Enabled || len(cfg.Server.GeoIP.DenyCountries) != 1 || cfg.Server.GeoIP.DenyCountries[0] != "CN" {
		t.Errorf("GeoIP configuration was not loaded: %+v", cfg.Server.GeoIP)
	}
	if len(cfg.Server.Security.Allowlist) != 1 || cfg.Server.Security.Allowlist[0].Description != "monitor" {
		t.Errorf("security configuration was not loaded: %+v", cfg.Server.Security)
	}
	if cfg.Server.Security.Reports.RetentionDays != 180 {
		t.Errorf("security report retention = %d, want 180", cfg.Server.Security.Reports.RetentionDays)
	}
	if len(cfg.Server.Security.Blocklists.ExtraSources) != 3 || !cfg.Server.Security.Blocklists.ExtraSources[2].Enabled {
		t.Error("blocklist source with omitted enabled field did not default to enabled")
	}
	if len(cfg.Server.Security.Blocklists.ExtraSources) < 2 || cfg.Server.Security.Blocklists.ExtraSources[1].Enabled {
		t.Errorf("explicitly disabled blocklist source was not preserved: %+v", cfg.Server.Security.Blocklists.ExtraSources)
	}
	if cfg.Data.CVE.Source != "https://nvd.example.test/feed" || !cfg.Data.Trivy.Enabled {
		t.Errorf("data source configuration was not loaded: %+v", cfg.Data)
	}
	if cfg.Server.Scheduler.Timezone != "UTC" || cfg.Server.Scheduler.CatchUpWindow != "2h" || !cfg.Server.Scheduler.Tasks["backup_hourly"].Enabled {
		t.Errorf("scheduler configuration was not loaded: %+v", cfg.Server.Scheduler)
	}
}

func TestValidateSkipsInvalidSecurityEntries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Security.Allowlist = []AllowlistEntry{
		{CIDR: "192.0.2.5", Description: "monitor"},
		{CIDR: "0.0.0.0/0", Description: "too broad"},
		{CIDR: "not-an-ip", Description: "invalid"},
	}
	cfg.Server.Security.BlockedIPs = []BlockedIPEntry{
		{CIDR: "198.51.100.8", Description: "test"},
		{CIDR: "invalid"},
	}
	cfg.Server.Security.Blocklists.ExtraSources = []BlocklistSourceConfig{
		{ID: "valid_feed", Type: "ip", URL: "https://feeds.example.test/list", Enabled: true},
		{ID: "firehol_level1", Type: "ip", URL: "https://example.test/list", Enabled: true},
		{ID: "bad scheme", Type: "domain", URL: "http://example.test/list", Enabled: true},
	}
	cfg.Server.GeoIP.DenyCountries = []string{"cn", "USA", "1!", "ZZ"}
	cfg.Server.Scheduler.CatchUpWindow = "not-a-duration"
	cfg.Server.Scheduler.Tasks["token_cleanup"] = ScheduledTaskConfig{Enabled: false, Schedule: "@every 15m"}
	if err := validate(cfg); err != nil {
		t.Fatalf("validate() returned an error for skippable security entries: %v", err)
	}
	if len(cfg.Server.Security.Allowlist) != 1 || cfg.Server.Security.Allowlist[0].CIDR != "192.0.2.5/32" {
		t.Fatalf("validated allowlist = %+v", cfg.Server.Security.Allowlist)
	}
	if len(cfg.Server.Security.BlockedIPs) != 1 || cfg.Server.Security.BlockedIPs[0].CIDR != "198.51.100.8/32" {
		t.Fatalf("validated blocked IPs = %+v", cfg.Server.Security.BlockedIPs)
	}
	if cfg.Server.Security.BlockedIPs[0].Description != "test" {
		t.Fatalf("blocked IP description = %q, want test", cfg.Server.Security.BlockedIPs[0].Description)
	}
	if len(cfg.Server.Security.Blocklists.ExtraSources) != 1 || cfg.Server.Security.Blocklists.ExtraSources[0].ID != "valid_feed" {
		t.Fatalf("validated blocklist sources = %+v", cfg.Server.Security.Blocklists.ExtraSources)
	}
	if len(cfg.Server.GeoIP.DenyCountries) != 1 || cfg.Server.GeoIP.DenyCountries[0] != "CN" {
		t.Fatalf("validated country codes = %+v", cfg.Server.GeoIP.DenyCountries)
	}
	if cfg.Server.Scheduler.CatchUpWindow != "1h" {
		t.Fatalf("invalid catch-up window resolved to %q, want 1h", cfg.Server.Scheduler.CatchUpWindow)
	}
	if !cfg.Server.Scheduler.Tasks["token_cleanup"].Enabled {
		t.Fatal("critical token_cleanup task was disabled by configuration")
	}
}

func TestValidateAPIVersion(t *testing.T) {
	for _, version := range []string{"v1", "v2", "v12"} {
		cfg := DefaultConfig()
		cfg.Server.APIVersion = version
		if err := validate(cfg); err != nil {
			t.Fatalf("validate %q: %v", version, err)
		}
		if cfg.Server.APIVersion != version {
			t.Errorf("API version = %q, want %q", cfg.Server.APIVersion, version)
		}
	}
	for _, version := range []string{"", "1", "V1", "v", "v1-beta", "v1/other"} {
		cfg := DefaultConfig()
		cfg.Server.APIVersion = version
		if err := validate(cfg); err != nil {
			t.Fatalf("validate invalid version %q: %v", version, err)
		}
		if cfg.Server.APIVersion != "v1" {
			t.Errorf("invalid API version %q resolved to %q, want v1", version, cfg.Server.APIVersion)
		}
	}
}

func TestValidateTracking(t *testing.T) {
	valid := []TrackingConfig{
		{Type: "google", ID: "G-ABC123"},
		{Type: "matomo", ID: "1", URL: "https://analytics.example.test"},
		{Type: "fathom", ID: "ABCDEFGH"},
		{Type: "plausible", ID: "example.test"},
		{Type: "umami", ID: "123e4567-e89b-42d3-a456-426614174000", URL: "https://analytics.example.test"},
		{Type: "simple"},
		{Type: "cloudflare", ID: "beacon-token"},
	}
	for _, tracking := range valid {
		if err := ValidateTracking(&tracking); err != nil {
			t.Errorf("valid tracking config %+v: %v", tracking, err)
		}
	}
	invalid := []TrackingConfig{
		{Type: "unknown", ID: "id"},
		{Type: "google", ID: "not-an-id"},
		{Type: "matomo", ID: "x", URL: "https://analytics.example.test"},
		{Type: "owa", ID: "site", URL: "javascript:alert(1)"},
		{Type: "umami", ID: "not-a-uuid", URL: "https://analytics.example.test"},
	}
	for _, tracking := range invalid {
		if err := ValidateTracking(&tracking); err == nil {
			t.Errorf("invalid tracking config was accepted: %+v", tracking)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{
		{input: "", want: "", valid: true},
		{input: "/", want: "", valid: true},
		{input: "/messages/", want: "/messages", valid: true},
		{input: "/api/v2", want: "/api/v2", valid: true},
		{input: "//attacker.invalid", valid: false},
		{input: "/../outside", valid: false},
		{input: "/messages?next=/", valid: false},
		{input: "https://example.test/messages", valid: false},
	} {
		got, err := NormalizeBaseURL(test.input)
		if (err == nil) != test.valid {
			t.Errorf("NormalizeBaseURL(%q) error = %v, valid = %t", test.input, err, test.valid)
			continue
		}
		if err == nil && got != test.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestLoad_PrivacyConfiguration(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "server.yml")
	yamlContent := "server:\n  tracking:\n    type: plausible\n    id: example.test\n  privacy:\n    data:\n      sold: true\n      sharing:\n        - condition: analytics\n          when: consented\n          data: aggregate usage\n    retention:\n      period: 30 days\n      export_available: true\n    consent:\n      message_if_sold: Custom consent\n      policy:\n        text: Privacy\n        url: /privacy\n    cookies:\n      analytics:\n        description: Usage analytics\n        description_suffix_sold: Shared\n    third_party:\n      services:\n        - name: Analytics\n          purpose: Usage\n          data_sent: Aggregate counts\n          policy_url: https://example.test/privacy\nweb:\n  headers:\n    honor_dnt: true\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	privacy := cfg.Server.Privacy
	if cfg.Server.Tracking.Type != "plausible" || cfg.Server.Tracking.ID != "example.test" || !cfg.Web.Headers.HonorDNT {
		t.Fatalf("tracking/privacy signal configuration was not loaded: tracking=%+v headers=%+v", cfg.Server.Tracking, cfg.Web.Headers)
	}
	if !privacy.Data.Sold || privacy.Data.Sharing[0].Data != "aggregate usage" || privacy.Retention.Period != "30 days" {
		t.Fatalf("privacy policy configuration was not loaded: %+v", privacy)
	}
	if privacy.GetConsentMessage() != "Custom consent" || privacy.GetAnalyticsDescription() != "Usage analytics Shared" || !privacy.IsCCPAApplicable() {
		t.Fatalf("sold-data dynamic privacy content is incorrect: %+v", privacy)
	}
	if len(privacy.ThirdParty.Services) != 1 || privacy.ThirdParty.Services[0].PolicyURL != "https://example.test/privacy" {
		t.Fatalf("third-party privacy services were not loaded: %+v", privacy.ThirdParty)
	}
}

func TestFirstRunConnectionEnvironmentSeedsSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "libsql://catalog.example.test?authToken=database-secret")
	t.Setenv("DATABASE_DRIVER", "sqlite")
	t.Setenv("CACHE_URL", "rediss://cache-user:cache-secret@cache.example.test:6379/2")
	path := filepath.Join(projectTestDir(t), "server.yml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Database.Driver != "libsql" || cfg.Server.Database.URL != "libsql://catalog.example.test?authToken=database-secret" {
		t.Fatalf("database connection = %+v", cfg.Server.Database)
	}
	if cfg.Server.Cache.Type != "redis" || cfg.Server.Cache.URL != "rediss://cache-user:cache-secret@cache.example.test:6379/2" {
		t.Fatalf("cache connection = %+v", cfg.Server.Cache)
	}
	serverConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverConfig), "database-secret") || strings.Contains(string(serverConfig), "cache-secret") {
		t.Fatal("connection credentials were written to server.yml")
	}
	secrets, err := os.ReadFile(filepath.Join(filepath.Dir(path), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secrets), "database-secret") || !strings.Contains(string(secrets), "cache-secret") {
		t.Fatal("connection credentials were not saved in secrets.yml")
	}
}

func TestConnectionURLsAndEnvironmentReferences(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"redis://cache.example.test:6379/0", false},
		{"redis://:secret@cache.example.test:6379/0", true},
		{"redis://secret@cache.example.test:6379/0", true},
		{"libsql://db.example.test?authToken=secret", true},
		{"libsql://db.example.test?authToken=", false},
	} {
		if got := connectionURLHasCredentials(test.value); got != test.want {
			t.Errorf("connectionURLHasCredentials(%q) = %t, want %t", test.value, got, test.want)
		}
	}
	t.Setenv("CONNECTION_TEST_SECRET", "resolved")
	expanded := map[string]any{"server": map[string]any{"database": map[string]any{"url": "${CONNECTION_TEST_SECRET}", "fallback": "${UNSET_CONNECTION_TEST:-fallback}"}}}
	expandEnvironmentReferences(expanded)
	if got, _ := getConfigPath(expanded, []string{"server", "database", "url"}); got != "resolved" {
		t.Fatalf("expanded database URL = %v, want resolved", got)
	}
	if got, _ := getConfigPath(expanded, []string{"server", "database", "fallback"}); got != "fallback" {
		t.Fatalf("expanded fallback = %v, want fallback", got)
	}
	redacted := RedactConnectionURL("rediss://user:secret@cache.example.test:6379/2?token=query-secret")
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "query-secret") {
		t.Fatalf("connection URL was not redacted: %s", redacted)
	}
}

func TestSavedConnectionIgnoresEnvironmentAfterFirstRun(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  database:\n    driver: sqlite\n    url: file:./data.db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "libsql://ignored.example.test?authToken=ignored")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Database.Driver != "sqlite" || cfg.Server.Database.URL != "file:./data.db" {
		t.Fatalf("saved database connection was overwritten: %+v", cfg.Server.Database)
	}
}

func TestDatabaseCacheSettingsDefaultsAndLoad(t *testing.T) {
	poolDefaults := DefaultConfig().Server.Database.Pool
	if poolDefaults.MaxOpen != 5 || poolDefaults.MaxIdle != 2 || poolDefaults.MaxLifetime != "5m" || poolDefaults.MaxIdleTime != "1m" {
		t.Fatalf("database pool defaults = %+v", poolDefaults)
	}
	defaults := DefaultConfig().Server.Database.Cache
	if defaults.SyncInterval != "30s" || defaults.MaxAge != "1h" || !defaults.OfflineMode || defaults.Enabled {
		t.Fatalf("database cache defaults = %+v", defaults)
	}
	configuration := DefaultConfig()
	if err := yaml.Unmarshal([]byte("server:\n  database:\n    driver: libsql\n    url: libsql://db.example.test\n    pool:\n      max_open: 12\n      max_idle: 7\n      max_lifetime: 10m\n      max_idle_time: 2m\n    cache:\n      enabled: true\n      path: /tmp/cache.db\n      sync_interval: 45s\n      offline_mode: false\n      max_age: 2h\n"), configuration); err != nil {
		t.Fatal(err)
	}
	if err := validate(configuration); err != nil {
		t.Fatal(err)
	}
	cache := configuration.Server.Database.Cache
	pool := configuration.Server.Database.Pool
	if pool.MaxOpen != 12 || pool.MaxIdle != 7 || pool.MaxLifetime != "10m" || pool.MaxIdleTime != "2m" {
		t.Fatalf("database pool settings = %+v", pool)
	}
	if !cache.Enabled || cache.Path != "/tmp/cache.db" || cache.SyncInterval != "45s" || cache.OfflineMode || cache.MaxAge != "2h" {
		t.Fatalf("database cache settings = %+v", cache)
	}
	configuration.Server.Database.Pool.MaxOpen = 4
	configuration.Server.Database.Pool.MaxIdle = 9
	configuration.Server.Database.Pool.MaxLifetime = "invalid"
	configuration.Server.Database.Pool.MaxIdleTime = "0s"
	if err := validate(configuration); err != nil {
		t.Fatal(err)
	}
	pool = configuration.Server.Database.Pool
	if pool.MaxOpen != 4 || pool.MaxIdle != 4 || pool.MaxLifetime != "5m" || pool.MaxIdleTime != "1m" {
		t.Fatalf("invalid database pool settings were not normalized: %+v", pool)
	}
}

func TestLoad_BrandingPageContent(t *testing.T) {
	directory := projectTestDir(t)
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(configPath, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	brandingDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(brandingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	branding := "server:\n  branding:\n    content:\n      about:\n        format: md\n        body:\n          default: Default text\n          fr: Texte français\n      terms:\n        format: txt\n        body: Terms text\n"
	if err := os.WriteFile(filepath.Join(brandingDirectory, "branding.yml"), []byte(branding), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadMergedConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Branding.Content.About.Format != "md" {
		t.Fatalf("about format = %q, want md", cfg.Server.Branding.Content.About.Format)
	}
	localized, ok := cfg.Server.Branding.Content.About.Body.(map[string]any)
	if !ok || localized["fr"] != "Texte français" || localized["default"] != "Default text" {
		t.Fatalf("localized about body = %#v", cfg.Server.Branding.Content.About.Body)
	}
	if cfg.Server.Branding.Content.Terms.Body != "Terms text" || cfg.Server.Branding.Content.Terms.Format != "txt" {
		t.Fatalf("terms content = %+v", cfg.Server.Branding.Content.Terms)
	}
}

// TestLoad_CreatesDefaultWhenMissing verifies Load writes a default config
// file when none exists yet, and that it can be read back.
func TestLoad_CreatesDefaultWhenMissing(t *testing.T) {
	tmp := projectTestDir(t)
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
	if !cfg.Server.PIDFile || cfg.Server.Daemonize {
		t.Errorf("startup defaults: pidfile=%t daemonize=%t, want true/false", cfg.Server.PIDFile, cfg.Server.Daemonize)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "  daemonize: false\n") || !strings.Contains(string(contents), "  pidfile: true\n") {
		t.Fatalf("generated server config omits startup settings:\n%s", contents)
	}
}

// TestLoad_ExistingOverridesDefaults verifies a partial YAML file on disk
// overrides only the fields it sets, with defaults filling the rest.
func TestLoad_ExistingOverridesDefaults(t *testing.T) {
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "config.yml")

	yamlContent := "server:\n  address: \"127.0.0.1\"\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
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
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "config.yml")

	if err := os.WriteFile(path, []byte("server: [this is not valid: yaml"), 0o644); err != nil {
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
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  misspelled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "field misspelled not found") {
		t.Fatalf("unknown configuration key should fail with its name, got %v", err)
	}
}

func TestSaveSMTPDetectionPersistsRelayToNotificationsArea(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "server.yml")
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
	tmp := projectTestDir(t)
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

func TestValidPort(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"8080", true},
		{"80,443", true},
		{" 8090 , 8443 ", true},
		{"", false},
		{"0", false},
		{"65536", false},
		{"80,", false},
		{",443", false},
		{"80,443,8443", false},
		{"80,https", false},
	} {
		if got := validPort(test.value); got != test.valid {
			t.Errorf("validPort(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestLoad_SSLConfiguration(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "server.yml")
	contents := "server:\n  port: \"8080,8443\"\n  ssl:\n    enabled: true\n    min_version: TLS1.3\n    letsencrypt:\n      enabled: true\n      challenge: tls-alpn-01\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Port != "8080,8443" || !cfg.Server.SSL.Enabled || cfg.Server.SSL.MinVersion != "TLS1.3" {
		t.Fatalf("SSL listener configuration was not loaded: port=%q ssl=%+v", cfg.Server.Port, cfg.Server.SSL)
	}
	if !cfg.Server.SSL.LetsEncrypt.Enabled || cfg.Server.SSL.LetsEncrypt.Challenge != "tls-alpn-01" {
		t.Fatalf("Let's Encrypt configuration was not loaded: %+v", cfg.Server.SSL.LetsEncrypt)
	}
}

// TestMigrateYamlToYml verifies a legacy .yaml sibling is renamed to .yml
// when the .yml target does not already exist, and left alone otherwise.
func TestMigrateYamlToYml(t *testing.T) {
	t.Run("renames when yml missing", func(t *testing.T) {
		tmp := projectTestDir(t)
		ymlPath := filepath.Join(tmp, "config.yml")
		yamlPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(yamlPath, []byte("server:\n  address: \"1.2.3.4\"\n"), 0o644); err != nil {
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
		tmp := projectTestDir(t)
		ymlPath := filepath.Join(tmp, "config.yml")
		yamlPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(ymlPath, []byte("existing yml\n"), 0o644); err != nil {
			t.Fatalf("failed to seed .yml file: %v", err)
		}
		if err := os.WriteFile(yamlPath, []byte("legacy yaml\n"), 0o644); err != nil {
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
		tmp := projectTestDir(t)
		path := filepath.Join(tmp, "config.json")
		// Should simply not panic or do anything for a non-.yml path.
		migrateYamlToYml(path)
	})
}

func TestLoadGeneratesPrivateMetricsTokens(t *testing.T) {
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"8080\"\n"), 0o600); err != nil {
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
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.yml mode = %04o, want 0600", info.Mode().Perm())
	}
}

// TestSaveAndGet verifies the Save/Get round trip through the package-level
// current config pointer.
func TestSaveAndGet(t *testing.T) {
	tmp := projectTestDir(t)
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
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	legacy := "server:\n  port: \"8080\"\n  update:\n    branch: beta\nweb:\n  ui:\n    theme: light\n  robots:\n    allow: [\"/\"]\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
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

func TestLoadMigratesCLIDistributionToMaintenanceArea(t *testing.T) {
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	legacy := "server:\n  cli:\n    binary_download:\n      dir: /srv/gitmessages/cli\n      require_auth: true\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI.BinaryDownload.Dir != "/srv/gitmessages/cli" || !cfg.CLI.BinaryDownload.RequireAuth {
		t.Fatalf("migrated CLI distribution config = %#v", cfg.CLI.BinaryDownload)
	}
	area, err := os.ReadFile(filepath.Join(tmp, "yaml.d", "maintenance.yml"))
	if err != nil || !strings.Contains(string(area), "binary_download:") || !strings.Contains(string(area), "require_auth: true") {
		t.Fatalf("maintenance area did not receive CLI config: %s, %v", area, err)
	}
	server, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(server), "cli:") {
		t.Fatalf("server.yml retained CLI distribution config: %s", server)
	}
}

func TestLoadMovesSecretsOutOfAreaFiles(t *testing.T) {
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  token: xxxxx\n  mode: production\n"), 0o600); err != nil {
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
	if info, err := os.Stat(secretPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.yml mode/stat = %v, %v", info, err)
	}
}

func TestLoadEncryptsMigratedDNSCredentials(t *testing.T) {
	tmp := projectTestDir(t)
	path := filepath.Join(tmp, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  ssl:\n    letsencrypt:\n      dns_credentials:\n        CF_DNS_API_TOKEN: private-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"]; got != "private-token" {
		t.Fatalf("in-memory DNS credential = %q, want plaintext value", got)
	}
	secretPath := filepath.Join(tmp, "secrets.yml")
	secretData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretData), "enc:v1:") || strings.Contains(string(secretData), "private-token") {
		t.Fatalf("DNS credential was not encrypted at rest: %s", secretData)
	}
	if info, err := os.Stat(secretPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.yml mode/stat = %v, %v", info, err)
	}
	rotatedKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := SaveEncryptionKey(rotatedKey); err != nil {
		t.Fatalf("rotate encryption key: %v", err)
	}
	rotatedData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rotatedData), "private-token") || string(rotatedData) == string(secretData) {
		t.Fatal("DNS credential ciphertext was not re-encrypted during key rotation")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"]; got != "private-token" {
		t.Fatalf("DNS credential after key rotation = %q", got)
	}
	for _, source := range []string{path, filepath.Join(tmp, "yaml.d", "app.yml")} {
		data, readErr := os.ReadFile(source)
		if readErr == nil && strings.Contains(string(data), "private-token") {
			t.Errorf("plaintext DNS credential remains in %s", source)
		}
	}
}

func TestSaveUpdateWritesMaintenanceArea(t *testing.T) {
	tmp := projectTestDir(t)
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
		{"toast duration", func(cfg *Config) { cfg.Server.Notifications.WebUI.Duration = -1 }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Notifications.WebUI.Duration != 3 {
				t.Fatal("invalid toast duration did not reset")
			}
		}},
		{"toast warning duration", func(cfg *Config) { cfg.Server.Notifications.WebUI.WarningDuration = -1 }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Notifications.WebUI.WarningDuration != 5 {
				t.Fatal("invalid warning toast duration did not reset")
			}
		}},
		{"toast position", func(cfg *Config) { cfg.Server.Notifications.WebUI.Position = "center" }, func(t *testing.T, cfg *Config) {
			if cfg.Server.Notifications.WebUI.Position != "top-right" {
				t.Fatal("invalid toast position did not reset")
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
