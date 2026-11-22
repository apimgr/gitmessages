package config

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config represents the complete server configuration
type Config struct {
	Server ServerConfig `yaml:"server"`
	Web    WebConfig    `yaml:"web"`
}

// ServerConfig contains server-related settings
type ServerConfig struct {
	Port           string               `yaml:"port"`
	Token          string               `yaml:"token"`
	FQDN           string               `yaml:"fqdn"`
	Address        string               `yaml:"address"`
	Mode           string               `yaml:"mode"`
	Update         UpdateConfig         `yaml:"update"`
	CORS           CORSConfig           `yaml:"cors"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	Logging        LoggingConfig        `yaml:"logs"`
	Tor            TorConfig            `yaml:"tor"`
	I2P            I2PConfig            `yaml:"i2p"`
	Contact        ContactConfig        `yaml:"contact"`
	Notifications  NotificationsConfig  `yaml:"notifications"`
	Security       SecurityConfig       `yaml:"security"`
	Tokens         TokenConfig          `yaml:"tokens"`
	Pages          PagesConfig          `yaml:"pages"`
	BaseURL        string               `yaml:"baseurl"`
	TrustedProxies TrustedProxiesConfig `yaml:"trusted_proxies"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	Privacy        PrivacyConfig        `yaml:"privacy"`
	URLDetection   URLDetectionConfig   `yaml:"url_detection"`
}

type URLDetectionConfig struct {
	Learning     bool   `yaml:"learning"`
	MinSamples   int    `yaml:"min_samples"`
	SampleWindow string `yaml:"sample_window"`
	LogChanges   bool   `yaml:"log_changes"`
	LiveReload   bool   `yaml:"live_reload"`
}

type PrivacyConfig struct {
	Data PrivacyDataConfig `yaml:"data"`
}

type PrivacyDataConfig struct {
	Sold bool `yaml:"sold"`
}

type TrustedProxiesConfig struct {
	Additional     []string `yaml:"additional"`
	ClientIPHeader string   `yaml:"client_ip_header"`
}

type RateLimitConfig struct {
	Read         RateLimitBucketConfig `yaml:"read"`
	Write        RateLimitBucketConfig `yaml:"write"`
	Health       RateLimitBucketConfig `yaml:"health"`
	Strict       RateLimitBucketConfig `yaml:"strict"`
	AuthFailures RateLimitBucketConfig `yaml:"auth_failures"`
	GlobalBurst  int                   `yaml:"global_burst"`
}

type RateLimitBucketConfig struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

type ContactConfig struct {
	Admin    ContactRecipientConfig `yaml:"admin"`
	Security ContactRecipientConfig `yaml:"security"`
	Abuse    ContactRecipientConfig `yaml:"abuse"`
	General  ContactRecipientConfig `yaml:"general"`
	DPO      DPOContactConfig       `yaml:"dpo"`
}

type ContactRecipientConfig struct {
	Email string `yaml:"email"`
}

type DPOContactConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type PagesConfig struct {
	Contact ContactPageConfig `yaml:"contact"`
}

type ContactPageConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	Captcha          string                `yaml:"captcha"`
	CaptchaSiteKey   string                `yaml:"captcha_site_key"`
	CaptchaSecretKey string                `yaml:"captcha_secret_key"`
	Antispam         ContactAntispamConfig `yaml:"antispam"`
	SuccessMessage   string                `yaml:"success_message"`
}

type ContactAntispamConfig struct {
	Honeypot             bool     `yaml:"honeypot"`
	MinFillSeconds       int      `yaml:"min_fill_seconds"`
	MaxAgeSeconds        int      `yaml:"max_age_seconds"`
	PerIP                int      `yaml:"per_ip"`
	PerEmail             int      `yaml:"per_email"`
	Global               int      `yaml:"global"`
	Window               string   `yaml:"window"`
	MaxLinks             int      `yaml:"max_links"`
	RejectMarkup         bool     `yaml:"reject_markup"`
	BlockedWords         []string `yaml:"blocked_words"`
	BlockedEmailDomains  []string `yaml:"blocked_email_domains"`
	Strikes              int      `yaml:"strikes"`
	BanHours             int      `yaml:"ban_hours"`
	ReviewMinSeconds     int      `yaml:"review_min_seconds"`
	ReviewMaxSeconds     int      `yaml:"review_max_seconds"`
	DuplicateWindowHours int      `yaml:"duplicate_window_hours"`
}

type NotificationsConfig struct {
	Email EmailNotificationConfig `yaml:"email"`
}

type EmailNotificationConfig struct {
	Enabled bool            `yaml:"enabled"`
	SMTP    SMTPConfig      `yaml:"smtp"`
	From    EmailFromConfig `yaml:"from"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	TLS      string `yaml:"tls"`
}

type EmailFromConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type SecurityConfig struct {
	EncryptionKey string `yaml:"encryption_key"`
}

type TokenConfig struct {
	MaxLinked     int    `yaml:"max_linked"`
	CookieMaxAge  string `yaml:"cookie_max_age"`
	IdlePurgeDays int    `yaml:"idle_purge_days"`
}

type UpdateConfig struct {
	Branch        string `yaml:"branch"`
	AutoInstall   bool   `yaml:"auto_install"`
	DeferDays     int    `yaml:"defer_days"`
	CLIMinVersion string `yaml:"cli_min_version"`
}

type TorConfig struct {
	OnionAddress              string `yaml:"onion_address"`
	Binary                    string `yaml:"binary"`
	UseNetwork                bool   `yaml:"use_network"`
	MaxCircuits               int    `yaml:"max_circuits"`
	StartupTimeout            int    `yaml:"startup_timeout"`
	CircuitTimeout            int    `yaml:"circuit_timeout"`
	BootstrapTimeout          int    `yaml:"bootstrap_timeout"`
	SafeLogging               bool   `yaml:"safe_logging"`
	MaxStreamsPerCircuit      int    `yaml:"max_streams_per_circuit"`
	CloseCircuitOnStreamLimit bool   `yaml:"close_circuit_on_stream_limit"`
	BandwidthRate             string `yaml:"bandwidth_rate"`
	BandwidthBurst            string `yaml:"bandwidth_burst"`
	MaxMonthlyBandwidth       string `yaml:"max_monthly_bandwidth"`
	NumIntroPoints            int    `yaml:"num_intro_points"`
	VirtualPort               int    `yaml:"virtual_port"`
}

type I2PConfig struct {
	Enabled          bool   `yaml:"enabled"`
	B32Address       string `yaml:"b32_address"`
	Binary           string `yaml:"binary"`
	SAMAddress       string `yaml:"sam_address"`
	VirtualPort      int    `yaml:"virtual_port"`
	InboundLength    int    `yaml:"inbound_length"`
	OutboundLength   int    `yaml:"outbound_length"`
	InboundQuantity  int    `yaml:"inbound_quantity"`
	OutboundQuantity int    `yaml:"outbound_quantity"`
	SignatureType    int    `yaml:"signature_type"`
	BootstrapTimeout int    `yaml:"bootstrap_timeout"`
}

// MetricsConfig contains metrics settings
type MetricsConfig struct {
	Enabled         bool              `yaml:"enabled"`
	Root            MetricsRootConfig `yaml:"root"`
	Auth            MetricsAuthConfig `yaml:"auth"`
	IncludeSystem   bool              `yaml:"include_system"`
	IncludeRuntime  bool              `yaml:"include_runtime"`
	IncludeApp      bool              `yaml:"include_app"`
	Loki            MetricsLokiConfig `yaml:"loki"`
	DurationBuckets []float64         `yaml:"duration_buckets"`
	SizeBuckets     []float64         `yaml:"size_buckets"`
}

type MetricsRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

type MetricsAuthConfig struct {
	AllowUnauthenticated bool               `yaml:"allow_unauthenticated"`
	Tokens               MetricsTokenConfig `yaml:"tokens"`
}

type MetricsTokenConfig struct {
	Prometheus string `yaml:"prometheus"`
	Grafana    string `yaml:"grafana"`
	Loki       string `yaml:"loki"`
}

type MetricsLokiConfig struct {
	MaxEntries int    `yaml:"max_entries"`
	MaxAge     string `yaml:"max_age"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	AccessFormat string `yaml:"access_format"`
	Level        string `yaml:"level"`
}

// WebUIConfig contains web UI settings
type WebUIConfig struct {
	Theme  string `yaml:"theme"`
	Accent string `yaml:"accent"`
}

type WebConfig struct {
	UI       WebUIConfig       `yaml:"ui"`
	Robots   WebRobotsConfig   `yaml:"robots"`
	Security WebSecurityConfig `yaml:"security"`
}

// WebRobotsConfig contains robots.txt settings
type WebRobotsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WebSecurityConfig contains security settings
type WebSecurityConfig struct {
	Contact       string `yaml:"contact"`
	Policy        string `yaml:"policy"`
	ReportURL     string `yaml:"report_url"`
	PublishPGPKey bool   `yaml:"publish_pgp_key"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

var (
	current              *Config
	mu                   sync.RWMutex
	configPath           string
	generatedSecretMu    sync.Mutex
	generatedSecretNames []string
)

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:    initialPort(),
			FQDN:    "",
			Address: initialAddress(),
			Mode:    "production",
			Tokens:  TokenConfig{MaxLinked: 25, CookieMaxAge: "1y", IdlePurgeDays: 0},
			RateLimit: RateLimitConfig{
				Read:         RateLimitBucketConfig{Requests: 120, Window: 60},
				Write:        RateLimitBucketConfig{Requests: 10, Window: 60},
				Health:       RateLimitBucketConfig{Requests: 120, Window: 60},
				Strict:       RateLimitBucketConfig{Requests: 10, Window: 900},
				AuthFailures: RateLimitBucketConfig{Requests: 5, Window: 900}, GlobalBurst: 240,
			},
			Privacy:      PrivacyConfig{Data: PrivacyDataConfig{Sold: false}},
			URLDetection: URLDetectionConfig{Learning: true, MinSamples: 3, SampleWindow: "5m", LogChanges: true, LiveReload: true},
			Update:       UpdateConfig{Branch: "stable", AutoInstall: false, DeferDays: 0},
			CORS:         CORSConfig{AllowedOrigins: []string{"*"}},
			Metrics: MetricsConfig{
				Enabled:         false,
				Root:            MetricsRootConfig{Enabled: true},
				IncludeSystem:   true,
				IncludeRuntime:  true,
				IncludeApp:      true,
				Loki:            MetricsLokiConfig{MaxEntries: 1000, MaxAge: "1h"},
				DurationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
				SizeBuckets:     []float64{100, 1000, 10000, 100000, 1000000, 10000000},
			},
			Logging: LoggingConfig{
				AccessFormat: "apache",
				Level:        "info",
			},
			Tor: TorConfig{
				MaxCircuits: 32, StartupTimeout: 60, CircuitTimeout: 60, BootstrapTimeout: 180,
				SafeLogging: true, MaxStreamsPerCircuit: 100, CloseCircuitOnStreamLimit: true,
				BandwidthRate: "1 MB", BandwidthBurst: "2 MB", MaxMonthlyBandwidth: "100 GB",
				NumIntroPoints: 3, VirtualPort: 80,
			},
			I2P: I2PConfig{
				Enabled: runningInContainer(), SAMAddress: "127.0.0.1:7656", VirtualPort: 80,
				InboundLength: 3, OutboundLength: 3, InboundQuantity: 5, OutboundQuantity: 5,
				SignatureType: 7, BootstrapTimeout: 300,
			},
			Contact: ContactConfig{
				Security: ContactRecipientConfig{Email: ""},
			},
			Notifications: NotificationsConfig{Email: EmailNotificationConfig{
				SMTP: SMTPConfig{Port: 587, TLS: "auto"},
			}},
			Pages: PagesConfig{Contact: ContactPageConfig{
				Enabled: true, Captcha: "simple", SuccessMessage: "Thank you for your report.",
				Antispam: ContactAntispamConfig{
					Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200,
					PerIP: 5, PerEmail: 3, Global: 60, Window: "1h", MaxLinks: 10,
					RejectMarkup: true, Strikes: 3, BanHours: 24,
					ReviewMinSeconds: 2, ReviewMaxSeconds: 900, DuplicateWindowHours: 24,
				},
			}},
		},
		Web: WebConfig{
			UI: WebUIConfig{Theme: "dark", Accent: "#bd93f9"},
			Robots: WebRobotsConfig{
				Allow: []string{"/", "/api"},
				Deny:  []string{"/debug"},
			},
			Security: WebSecurityConfig{Contact: ""},
		},
	}
}

func initialPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); validPort(port) {
		return port
	}
	if runningInContainer() {
		return "80"
	}
	for attempt := 0; attempt < 100; attempt++ {
		port := 64000 + rand.IntN(1000)
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return fmt.Sprintf("%d", port)
	}
	return "64000"
}

func initialAddress() string {
	if address := strings.TrimSpace(os.Getenv("LISTEN")); address != "" {
		return address
	}
	return "[::]"
}

func validPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("container"))); value == "docker" || value == "podman" || value == "lxc" || value == "incus" || value == "systemd-nspawn" {
		return true
	}
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		name := strings.ToLower(strings.TrimSpace(string(data)))
		if name == "tini" || name == "dumb-init" || name == "systemd" || name == "init" {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		text := strings.ToLower(string(data))
		for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "lxc", "incus", "machine.slice"} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

// migrateYamlToYml migrates old .yaml files to .yml
func migrateYamlToYml(path string) {
	if !strings.HasSuffix(path, ".yml") {
		return
	}

	// Check if .yaml version exists
	oldPath := strings.TrimSuffix(path, ".yml") + ".yaml"
	if _, err := os.Stat(oldPath); err == nil {
		// Old .yaml file exists, check if new .yml doesn't
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// Rename .yaml to .yml
			if err := os.Rename(oldPath, path); err == nil {
				fmt.Printf("Migrated config: %s -> %s\n", oldPath, path)
			}
		}
	}
}

// Load loads configuration from a YAML file
func Load(path string) (*Config, error) {
	mu.Lock()
	defer mu.Unlock()

	// Migrate old .yaml to .yml if needed
	migrateYamlToYml(path)

	configPath = path
	if err := migrateSecretsFromServer(path); err != nil {
		return nil, fmt.Errorf("failed to migrate server secrets: %w", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		if err := saveConfig(cfg, path); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
	}
	if err := ensureGeneratedSecrets(filepath.Join(filepath.Dir(path), "secrets.yml")); err != nil {
		return nil, fmt.Errorf("failed to initialize server credentials: %w", err)
	}
	cfg, err := loadMergedConfig(path)
	if err != nil {
		return nil, err
	}
	if err := applySMTPEnvironment(cfg); err != nil {
		return nil, err
	}
	migrateAreaFiles(path)

	current = cfg
	return cfg, nil
}

func loadMergedConfig(path string) (*Config, error) {
	defaults, err := configMap(DefaultConfig())
	if err != nil {
		return nil, err
	}
	merged := defaults
	base, err := readConfigMap(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: ignoring invalid config file %s: %v\n", path, err)
		}
	} else {
		mergeMaps(merged, base)
		if err := os.Chmod(path, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not secure config file %s: %v\n", path, err)
		}
	}
	directory := filepath.Join(filepath.Dir(path), "yaml.d")
	if info, err := os.Stat(directory); err == nil && info.IsDir() {
		if chmodErr := os.Chmod(directory, 0700); chmodErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not secure config drop-in directory %s: %v\n", directory, chmodErr)
		}
		entries, err := os.ReadDir(directory)
		if err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				extension := filepath.Ext(entry.Name())
				if extension != ".yml" && extension != ".yaml" || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				file := filepath.Join(directory, entry.Name())
				resolved, resolveErr := filepath.EvalSymlinks(file)
				if resolveErr != nil {
					continue
				}
				resolved, _ = filepath.Abs(resolved)
				root, _ := filepath.Abs(directory)
				relative, relErr := filepath.Rel(root, resolved)
				if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					fmt.Fprintf(os.Stderr, "warning: ignoring config drop-in outside yaml.d: %s\n", entry.Name())
					continue
				}
				info, statErr := os.Stat(resolved)
				if statErr != nil || !info.Mode().IsRegular() {
					continue
				}
				if chmodErr := os.Chmod(resolved, 0600); chmodErr != nil {
					fmt.Fprintf(os.Stderr, "warning: ignoring insecure config drop-in %s: %v\n", entry.Name(), chmodErr)
					continue
				}
				area, readErr := readConfigMap(resolved)
				if readErr != nil {
					fmt.Fprintf(os.Stderr, "warning: ignoring invalid config drop-in %s: %v\n", entry.Name(), readErr)
					continue
				}
				mergeMaps(merged, area)
			}
		}
	}
	secrets := filepath.Join(filepath.Dir(path), "secrets.yml")
	if secretMap, err := readConfigMap(secrets); err == nil {
		if chmodErr := os.Chmod(secrets, 0600); chmodErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not secure secrets file: %v\n", chmodErr)
		}
		mergeMaps(merged, secretMap)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: ignoring invalid secrets file: %v\n", err)
	}
	migrateLegacyConfig(merged)
	encoded, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged configuration: %w", err)
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(encoded, cfg); err != nil {
		return nil, fmt.Errorf("decode merged configuration: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func migrateLegacyConfig(root map[string]any) {
	web := nestedMap(root, "web")
	for legacy, destination := range map[string]string{"web-ui": "ui", "web-robots": "robots", "web-security": "security"} {
		value, ok := root[legacy].(map[string]any)
		if !ok {
			continue
		}
		if _, exists := web[destination]; !exists {
			web[destination] = value
		}
		delete(root, legacy)
	}
	if len(web) > 0 {
		root["web"] = web
	}
	server := nestedMap(root, "server")
	if oldLogs, ok := server["logging"]; ok {
		if _, exists := server["logs"]; !exists {
			server["logs"] = oldLogs
		}
		delete(server, "logging")
	}
	if oldBranch, ok := server["update_branch"]; ok {
		update := nestedMap(server, "update")
		if _, exists := update["branch"]; !exists {
			update["branch"] = oldBranch
		}
		server["update"] = update
		delete(server, "update_branch")
	}
	if legacySecurity, ok := web["security"].(map[string]any); ok {
		if oldCORS, ok := legacySecurity["cors"]; ok {
			cors := nestedMap(server, "cors")
			if _, exists := cors["allowed_origins"]; !exists {
				switch value := oldCORS.(type) {
				case string:
					cors["allowed_origins"] = []any{value}
				case []any:
					cors["allowed_origins"] = value
				}
			}
			server["cors"] = cors
			delete(legacySecurity, "cors")
		}
	}
	if len(server) > 0 {
		root["server"] = server
	}
}

func nestedMap(root map[string]any, key string) map[string]any {
	if current, ok := root[key].(map[string]any); ok {
		return current
	}
	current := make(map[string]any)
	root[key] = current
	return current
}

func configMap(value any) (map[string]any, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any)
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func readConfigMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any)
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func mergeMaps(destination, source map[string]any) {
	for key, value := range source {
		if value == nil {
			delete(destination, key)
			continue
		}
		sourceMap, sourceIsMap := value.(map[string]any)
		if !sourceIsMap {
			destination[key] = value
			continue
		}
		destinationMap, destinationIsMap := destination[key].(map[string]any)
		if !destinationIsMap {
			destinationMap = make(map[string]any)
			destination[key] = destinationMap
		}
		mergeMaps(destinationMap, sourceMap)
	}
}

// Get returns the current configuration
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return DefaultConfig()
	}
	return current
}

// ConfigDirectory returns the directory containing the loaded server config.
func ConfigDirectory() string {
	mu.RLock()
	defer mu.RUnlock()
	if configPath == "" {
		return ""
	}
	return filepath.Dir(configPath)
}

// Save saves the current configuration to file
func Save() error {
	mu.Lock()
	defer mu.Unlock()
	if current == nil || configPath == "" {
		return fmt.Errorf("no configuration loaded")
	}
	return saveConfig(current, configPath)
}

func SaveUpdate() error {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || configPath == "" {
		return fmt.Errorf("no configuration loaded")
	}
	return saveArea(configPath, "maintenance.yml", map[string]any{"server": map[string]any{"update": current.Server.Update}})
}

// SaveSMTPDetection persists a detected relay in the notifications area.
func SaveSMTPDetection(host string, port int) error {
	mu.Lock()
	defer mu.Unlock()
	if current == nil || configPath == "" || strings.TrimSpace(host) == "" || port < 1 || port > 65535 {
		return errors.New("valid SMTP detection and loaded configuration are required")
	}
	values := map[string]any{"server": map[string]any{"notifications": map[string]any{"email": map[string]any{"enabled": true, "smtp": map[string]any{"host": host, "port": port}}}}}
	if err := saveArea(configPath, "notifications.yml", values); err != nil {
		return err
	}
	current.Server.Notifications.Email.SMTP.Host = host
	current.Server.Notifications.Email.SMTP.Port = port
	current.Server.Notifications.Email.Enabled = true
	return nil
}

// saveConfig writes configuration to a YAML file
func saveConfig(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	content := generateConfigYAML(cfg)
	file, err := os.CreateTemp(dir, ".server.yml-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	tmpPath := file.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to secure temporary config file: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to sync config file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close config file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace config file: %w", err)
	}

	return nil
}

// generateConfigYAML generates YAML content with comments
func generateConfigYAML(cfg *Config) string {
	server := map[string]any{"port": cfg.Server.Port, "address": cfg.Server.Address, "fqdn": cfg.Server.FQDN, "mode": cfg.Server.Mode, "baseurl": cfg.Server.BaseURL, "trusted_proxies": cfg.Server.TrustedProxies, "rate_limit": cfg.Server.RateLimit, "url_detection": cfg.Server.URLDetection}
	data, err := yaml.Marshal(map[string]any{"server": server})
	if err != nil {
		return "# Git Messages Server Configuration\n"
	}
	return "# Settings of this node only (listen, identity, database, cache, ssl). Everything else lives in yaml.d/: branding.yml, web.yml, pages.yml, contact.yml, privacy.yml, security.yml, logs.yml, metrics.yml, notifications.yml, tor.yml, i2p.yml, maintenance.yml, app.yml. Credentials live in secrets.yml.\n" + string(data)
}

func saveArea(configFile, name string, values map[string]any) error {
	areaDirectory := filepath.Join(filepath.Dir(configFile), "yaml.d")
	if err := os.MkdirAll(areaDirectory, 0700); err != nil {
		return fmt.Errorf("create configuration area directory: %w", err)
	}
	path := filepath.Join(areaDirectory, name)
	existing, err := readConfigMap(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing configuration area %s: %w", name, err)
	}
	if existing == nil {
		existing = make(map[string]any)
	}
	mergeMaps(existing, values)
	data, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("encode configuration area %s: %w", name, err)
	}
	return writePrivateAtomic(path, data)
}

func writePrivateAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func ensureGeneratedSecrets(path string) error {
	secrets, err := readConfigMap(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read secrets file: %w", err)
	}
	if secrets == nil {
		secrets = make(map[string]any)
	}
	changed := false
	var generated []string
	operator, ok := getConfigPath(secrets, []string{"server", "token"})
	if !ok || strings.TrimSpace(fmt.Sprint(operator)) == "" {
		token, err := generatedToken("adm_")
		if err != nil {
			return fmt.Errorf("generate operator token: %w", err)
		}
		putConfigPath(secrets, []string{"server", "token"}, token)
		changed = true
		generated = append(generated, "server.token")
		fmt.Fprintf(os.Stderr, "Operator token created: %s\n", token)
	}
	encryptionKey, ok := getConfigPath(secrets, []string{"server", "security", "encryption_key"})
	if !ok || strings.TrimSpace(fmt.Sprint(encryptionKey)) == "" {
		raw := make([]byte, 32)
		if _, err := cryptorand.Read(raw); err != nil {
			return fmt.Errorf("generate encryption key: %w", err)
		}
		putConfigPath(secrets, []string{"server", "security", "encryption_key"}, base64.RawURLEncoding.EncodeToString(raw))
		putConfigPath(secrets, []string{"server", "security", "encryption_key_version"}, 1)
		changed = true
		generated = append(generated, "server.security.encryption_key")
	} else if version, ok := getConfigPath(secrets, []string{"server", "security", "encryption_key_version"}); !ok || strings.TrimSpace(fmt.Sprint(version)) == "" {
		putConfigPath(secrets, []string{"server", "security", "encryption_key_version"}, 1)
		changed = true
	}
	for _, service := range []string{"prometheus", "grafana", "loki"} {
		value, ok := getConfigPath(secrets, []string{"server", "metrics", "auth", "tokens", service})
		if ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			continue
		}
		token, err := generatedToken("mtr_")
		if err != nil {
			return fmt.Errorf("generate %s token: %w", service, err)
		}
		putConfigPath(secrets, []string{"server", "metrics", "auth", "tokens", service}, token)
		changed = true
		generated = append(generated, "server.metrics.auth.tokens."+service)
	}
	if !changed {
		return nil
	}
	encoded, err := yaml.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encode secrets file: %w", err)
	}
	if err := writePrivateAtomic(path, encoded); err != nil {
		return err
	}
	generatedSecretMu.Lock()
	generatedSecretNames = append(generatedSecretNames, generated...)
	generatedSecretMu.Unlock()
	return nil
}

// ConsumeGeneratedSecrets returns names created since the previous call; secret values are never returned.
func ConsumeGeneratedSecrets() []string {
	generatedSecretMu.Lock()
	defer generatedSecretMu.Unlock()
	names := append([]string(nil), generatedSecretNames...)
	generatedSecretNames = nil
	return names
}

func generatedToken(prefix string) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var token strings.Builder
	token.WriteString(prefix)
	for token.Len() < len(prefix)+32 {
		var value [1]byte
		if _, err := cryptorand.Read(value[:]); err != nil {
			return "", err
		}
		if value[0] < 248 {
			token.WriteByte(alphabet[int(value[0])%len(alphabet)])
		}
	}
	return token.String(), nil
}

func migrateSecretsFromServer(configPath string) error {
	type sourceFile struct {
		path    string
		values  map[string]any
		header  bool
		changed bool
	}
	sources := []sourceFile{}
	original, err := readConfigMap(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || original == nil {
		return nil
	}
	sources = append(sources, sourceFile{path: configPath, values: original, header: true})
	areaDir := filepath.Join(filepath.Dir(configPath), "yaml.d")
	if entries, readErr := os.ReadDir(areaDir); readErr == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			ext := filepath.Ext(entry.Name())
			if entry.IsDir() || (ext != ".yml" && ext != ".yaml") || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(areaDir, entry.Name())
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			values, readErr := readConfigMap(path)
			if readErr == nil && values != nil {
				sources = append(sources, sourceFile{path: path, values: values})
			}
		}
	}
	secrets := make(map[string]any)
	secretPaths := [][]string{{"server", "token"}, {"server", "security", "encryption_key"}, {"server", "security", "encryption_key_version"}}
	changed := false
	for i := range sources {
		for _, keyPath := range secretPaths {
			if value, ok := takeConfigPath(sources[i].values, keyPath); ok {
				putConfigPath(secrets, keyPath, value)
				sources[i].changed = true
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	secretPath := filepath.Join(filepath.Dir(configPath), "secrets.yml")
	existing, err := readConfigMap(secretPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if existing == nil {
		existing = make(map[string]any)
	}
	mergeMapsPreferExisting(existing, secrets)
	encodedSecrets, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if !source.changed {
			continue
		}
		if originalBytes, readErr := os.ReadFile(source.path); readErr == nil {
			if err := writePrivateAtomic(source.path+".bak", originalBytes); err != nil {
				return err
			}
		}
	}
	if err := writePrivateAtomic(secretPath, encodedSecrets); err != nil {
		return err
	}
	for _, source := range sources {
		if !source.changed {
			continue
		}
		encodedConfig, err := yaml.Marshal(source.values)
		if err != nil {
			return err
		}
		if source.header {
			err = writePrivateAtomic(source.path, []byte(configHeader+string(encodedConfig)))
		} else {
			err = writePrivateAtomic(source.path, encodedConfig)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func migrateAreaFiles(configFile string) {
	original, err := readConfigMap(configFile)
	if err != nil {
		return
	}
	migrateLegacyConfig(original)
	server, _ := original["server"].(map[string]any)
	if server == nil {
		server = make(map[string]any)
	}
	areas := make(map[string]map[string]any)
	put := func(file, section, key string, value any) {
		if areas[file] == nil {
			areas[file] = make(map[string]any)
		}
		sectionMap, _ := areas[file][section].(map[string]any)
		if sectionMap == nil {
			sectionMap = make(map[string]any)
			areas[file][section] = sectionMap
		}
		sectionMap[key] = value
	}
	areaForServer := map[string]string{
		"update": "maintenance.yml", "auto_update": "maintenance.yml", "backup": "maintenance.yml", "restore": "maintenance.yml", "scheduler": "maintenance.yml",
		"metrics": "metrics.yml", "tor": "tor.yml", "i2p": "i2p.yml", "logs": "logs.yml", "notifications": "notifications.yml",
		"privacy": "privacy.yml", "compliance": "privacy.yml", "tracking": "privacy.yml", "security": "security.yml", "rate_limit": "security.yml", "geoip": "security.yml", "limits": "security.yml", "tokens": "security.yml",
		"pages": "pages.yml", "contact": "contact.yml", "cors": "web.yml", "csrf": "web.yml", "compression": "web.yml", "healthz": "web.yml", "i18n": "web.yml", "url_detection": "web.yml",
	}
	nodeKeys := map[string]bool{"port": true, "address": true, "fqdn": true, "mode": true, "api_version": true, "baseurl": true, "pidfile": true, "daemonize": true, "debug": true, "database": true, "cache": true, "ssl": true, "trusted_proxies": true, "cluster": true}
	for key, value := range server {
		if nodeKeys[key] {
			continue
		}
		file := areaForServer[key]
		if file == "" {
			file = "app.yml"
		}
		put(file, "server", key, value)
		delete(server, key)
	}
	web, _ := original["web"].(map[string]any)
	if web == nil {
		web = make(map[string]any)
	}
	if ui, ok := web["ui"]; ok {
		if areas["branding.yml"] == nil {
			areas["branding.yml"] = make(map[string]any)
		}
		areas["branding.yml"]["web"] = map[string]any{"ui": ui}
		delete(web, "ui")
	}
	if len(web) > 0 {
		if areas["web.yml"] == nil {
			areas["web.yml"] = make(map[string]any)
		}
		areas["web.yml"]["web"] = web
		delete(original, "web")
	}
	for key, value := range original {
		if key == "server" || key == "web" {
			continue
		}
		if areas["app.yml"] == nil {
			areas["app.yml"] = make(map[string]any)
		}
		areas["app.yml"][key] = value
		delete(original, key)
	}
	secrets := make(map[string]any)
	secretPaths := [][]string{
		{"server", "token"}, {"server", "security", "encryption_key"}, {"server", "security", "encryption_key_version"},
		{"server", "notifications", "email", "smtp", "password"}, {"server", "cache", "password"},
		{"server", "database", "token"}, {"server", "database", "url"}, {"server", "cache", "url"},
		{"server", "pages", "contact", "captcha_secret_key"}, {"server", "ssl", "letsencrypt", "dns_credentials"},
		{"server", "backup", "encryption_password"}, {"server", "metrics", "auth", "tokens"},
		{"server", "contact", "webhooks"}, {"server", "auth", "ldap", "providers"}, {"server", "auth", "oidc", "providers"},
		{"data", "cve", "api_key"},
	}
	areaNames := make([]string, 0, len(areas))
	for name := range areas {
		areaNames = append(areaNames, name)
	}
	sort.Strings(areaNames)
	for _, path := range secretPaths {
		if value, ok := takeConfigPath(original, path); ok {
			putConfigPath(secrets, path, value)
		}
		for _, name := range areaNames {
			values := areas[name]
			if value, ok := takeConfigPath(values, path); ok {
				putConfigPath(secrets, path, value)
			}
		}
	}
	if len(areas) == 0 {
		return
	}
	if data, err := os.ReadFile(configFile); err == nil {
		backupPath := configFile + ".bak"
		_ = writePrivateAtomic(backupPath, data)
	}
	areaDirectory := filepath.Join(filepath.Dir(configFile), "yaml.d")
	if err := os.MkdirAll(areaDirectory, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: configuration migration skipped: %v\n", err)
		return
	}
	if err := os.Chmod(areaDirectory, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: configuration migration skipped: %v\n", err)
		return
	}
	if len(secrets) > 0 {
		secretPath := filepath.Join(filepath.Dir(configFile), "secrets.yml")
		existing, err := readConfigMap(secretPath)
		if err == nil {
			mergeMapsPreferExisting(existing, secrets)
			secrets = existing
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: configuration migration skipped secrets.yml: %v\n", err)
			return
		}
		data, err := yaml.Marshal(secrets)
		if err != nil || writePrivateAtomic(secretPath, data) != nil {
			fmt.Fprintln(os.Stderr, "warning: configuration migration skipped secrets.yml")
			return
		}
	}
	for name, values := range areas {
		path := filepath.Join(areaDirectory, name)
		existing, err := readConfigMap(path)
		if err == nil {
			mergeMapsPreferExisting(existing, values)
			values = existing
		}
		data, err := yaml.Marshal(values)
		if err != nil || writePrivateAtomic(path, data) != nil {
			fmt.Fprintf(os.Stderr, "warning: configuration migration skipped for %s\n", name)
			return
		}
	}
	original["server"] = server
	if err := writePrivateAtomic(configFile, []byte(generateNodeFromMap(original))); err != nil {
		fmt.Fprintf(os.Stderr, "warning: configuration migration left original server.yml in place: %v\n", err)
	}
}

func takeConfigPath(root map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	if len(path) == 1 {
		value, exists := root[path[0]]
		delete(root, path[0])
		return value, exists
	}
	current, ok := root[path[0]].(map[string]any)
	if !ok {
		return nil, false
	}
	value, exists := takeConfigPath(current, path[1:])
	if len(current) == 0 {
		delete(root, path[0])
	}
	return value, exists
}

func getConfigPath(root map[string]any, path []string) (any, bool) {
	current := root
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	value, ok := current[path[len(path)-1]]
	return value, ok
}

func putConfigPath(root map[string]any, path []string, value any) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		root[path[0]] = value
		return
	}
	child, ok := root[path[0]].(map[string]any)
	if !ok {
		child = make(map[string]any)
		root[path[0]] = child
	}
	putConfigPath(child, path[1:], value)
}

func mergeMapsPreferExisting(existing, incoming map[string]any) {
	for key, value := range incoming {
		old, found := existing[key]
		oldMap, oldIsMap := old.(map[string]any)
		newMap, newIsMap := value.(map[string]any)
		if found && oldIsMap && newIsMap {
			mergeMapsPreferExisting(oldMap, newMap)
			continue
		}
		if !found {
			existing[key] = value
		}
	}
}

func generateNodeFromMap(values map[string]any) string {
	server, _ := values["server"].(map[string]any)
	if server == nil {
		server = make(map[string]any)
	}
	data, err := yaml.Marshal(map[string]any{"server": server})
	if err != nil {
		return generateConfigYAML(DefaultConfig())
	}
	return configHeader + string(data)
}

const configHeader = "# Settings of this node only (listen, identity, database, cache, ssl). Everything else lives in yaml.d/: branding.yml, web.yml, pages.yml, contact.yml, privacy.yml, security.yml, logs.yml, metrics.yml, notifications.yml, tor.yml, i2p.yml, maintenance.yml, app.yml. Credentials live in secrets.yml.\n"

func validate(cfg *Config) error {
	defaults := DefaultConfig()
	if !validPort(cfg.Server.Port) {
		warnInvalid("server.port")
		cfg.Server.Port = defaults.Server.Port
	}
	address := strings.Trim(strings.TrimSpace(cfg.Server.Address), "[]")
	if net.ParseIP(address) == nil {
		warnInvalid("server.address")
		cfg.Server.Address = defaults.Server.Address
	}
	switch cfg.Server.Mode {
	case "production", "development", "debug":
	default:
		warnInvalid("server.mode")
		cfg.Server.Mode = defaults.Server.Mode
	}
	if cfg.Server.Update.Branch != "stable" && cfg.Server.Update.Branch != "beta" && cfg.Server.Update.Branch != "devel" {
		warnInvalid("server.update.branch")
		cfg.Server.Update.Branch = defaults.Server.Update.Branch
	}
	if cfg.Server.Update.DeferDays < 0 || cfg.Server.Update.DeferDays > 365 {
		warnInvalid("server.update.defer_days")
		cfg.Server.Update.DeferDays = defaults.Server.Update.DeferDays
	}
	if cfg.Web.UI.Theme != "dark" && cfg.Web.UI.Theme != "light" && cfg.Web.UI.Theme != "auto" {
		warnInvalid("web.ui.theme")
		cfg.Web.UI.Theme = defaults.Web.UI.Theme
	}
	for _, origin := range cfg.Server.CORS.AllowedOrigins {
		if origin == "*" || origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			warnInvalid("server.cors.allowed_origins")
			cfg.Server.CORS = defaults.Server.CORS
			break
		}
	}
	if cfg.Server.Tor.MaxCircuits < 1 || cfg.Server.Tor.StartupTimeout < 10 || cfg.Server.Tor.StartupTimeout > 300 || cfg.Server.Tor.BootstrapTimeout < 10 || cfg.Server.Tor.BootstrapTimeout > 3600 || cfg.Server.Tor.VirtualPort < 1 || cfg.Server.Tor.VirtualPort > 65535 || cfg.Server.Tor.NumIntroPoints < 3 || cfg.Server.Tor.NumIntroPoints > 10 {
		warnInvalid("server.tor")
		cfg.Server.Tor = defaults.Server.Tor
	}
	if cfg.Server.I2P.VirtualPort < 1 || cfg.Server.I2P.VirtualPort > 65535 || cfg.Server.I2P.InboundLength < 0 || cfg.Server.I2P.InboundLength > 7 || cfg.Server.I2P.OutboundLength < 0 || cfg.Server.I2P.OutboundLength > 7 || cfg.Server.I2P.InboundQuantity < 1 || cfg.Server.I2P.InboundQuantity > 16 || cfg.Server.I2P.OutboundQuantity < 1 || cfg.Server.I2P.OutboundQuantity > 16 || cfg.Server.I2P.SignatureType != 7 || cfg.Server.I2P.BootstrapTimeout < 30 || cfg.Server.I2P.BootstrapTimeout > 600 {
		warnInvalid("server.i2p")
		cfg.Server.I2P = defaults.Server.I2P
	}
	if cfg.Server.Notifications.Email.SMTP.Port < 1 || cfg.Server.Notifications.Email.SMTP.Port > 65535 {
		return errors.New("server.notifications.email.smtp.port must be between 1 and 65535")
	}
	switch cfg.Server.Notifications.Email.SMTP.TLS {
	case "", "auto", "starttls", "tls", "none":
	default:
		return errors.New("server.notifications.email.smtp.tls must be auto, starttls, tls, or none")
	}
	switch cfg.Server.Pages.Contact.Captcha {
	case "", "simple":
	case "recaptcha", "hcaptcha":
		if cfg.Server.Pages.Contact.CaptchaSiteKey == "" || cfg.Server.Pages.Contact.CaptchaSecretKey == "" {
			return errors.New("configured contact CAPTCHA provider requires site and secret keys")
		}
	default:
		return errors.New("server.pages.contact.captcha must be simple, recaptcha, or hcaptcha")
	}
	if cfg.Server.Notifications.Email.SMTP.Host != "" {
		cfg.Server.Notifications.Email.Enabled = true
	} else {
		cfg.Server.Notifications.Email.Enabled = false
	}
	return nil
}

func applySMTPEnvironment(cfg *Config) error {
	smtp := &cfg.Server.Notifications.Email.SMTP
	for name, target := range map[string]*string{"SMTP_HOST": &smtp.Host, "SMTP_USERNAME": &smtp.Username, "SMTP_PASSWORD": &smtp.Password, "SMTP_TLS": &smtp.TLS, "SMTP_FROM_NAME": &cfg.Server.Notifications.Email.From.Name, "SMTP_FROM_EMAIL": &cfg.Server.Notifications.Email.From.Email} {
		if value, ok := os.LookupEnv(name); ok {
			*target = value
		}
	}
	if value, ok := os.LookupEnv("SMTP_PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("SMTP_PORT must be a valid TCP port")
		}
		smtp.Port = port
	}
	return nil
}

func warnInvalid(key string) {
	fmt.Fprintf(os.Stderr, "warning: invalid configuration value for %s; using the default\n", key)
}

func formatStringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	result := "["
	for i, v := range s {
		if i > 0 {
			result += ", "
		}
		result += fmt.Sprintf("\"%s\"", v)
	}
	result += "]"
	return result
}

// GetTheme returns the current theme
func GetTheme() string {
	cfg := Get()
	return cfg.Web.UI.Theme
}

// GetCORS returns the CORS setting
func GetCORS() string {
	cfg := Get()
	if len(cfg.Server.CORS.AllowedOrigins) == 0 {
		return "*"
	}
	return strings.Join(cfg.Server.CORS.AllowedOrigins, ",")
}
