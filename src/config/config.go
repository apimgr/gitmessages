package config

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// Config represents the complete server configuration
type Config struct {
	Server ServerConfig `yaml:"server"`
	Web    WebConfig    `yaml:"web"`
	Data   DataConfig   `yaml:"data"`
	CLI    CLIConfig    `yaml:"cli"`
	Output OutputConfig `yaml:"output"`
}

// OutputConfig stores explicit output preferences; nil retains automatic detection.
type OutputConfig struct {
	Color *bool `yaml:"color,omitempty"`
	Emoji *bool `yaml:"emoji,omitempty"`
}

// DefaultAPIVersion is the canonical API prefix used when no version is configured.
const DefaultAPIVersion = "v1"

// ServerConfig contains server-related settings
type ServerConfig struct {
	Port           string               `yaml:"port"`
	Daemonize      bool                 `yaml:"daemonize"`
	PIDFile        bool                 `yaml:"pidfile"`
	APIVersion     string               `yaml:"api_version"`
	SSL            SSLConfig            `yaml:"ssl"`
	Database       DatabaseConfig       `yaml:"database"`
	Cache          CacheConfig          `yaml:"cache"`
	Token          string               `yaml:"token"`
	FQDN           string               `yaml:"fqdn"`
	Address        string               `yaml:"address"`
	Mode           string               `yaml:"mode"`
	Debug          DebugConfig          `yaml:"debug"`
	Update         UpdateConfig         `yaml:"update"`
	CORS           CORSConfig           `yaml:"cors"`
	CSRF           CSRFConfig           `yaml:"csrf"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	Logging        LoggingConfig        `yaml:"logs"`
	Maintenance    MaintenanceConfig    `yaml:"maintenance"`
	Tor            TorConfig            `yaml:"tor"`
	I2P            I2PConfig            `yaml:"i2p"`
	Contact        ContactConfig        `yaml:"contact"`
	Notifications  NotificationsConfig  `yaml:"notifications"`
	I18N           I18NConfig           `yaml:"i18n"`
	Security       SecurityConfig       `yaml:"security"`
	GeoIP          GeoIPConfig          `yaml:"geoip"`
	Scheduler      SchedulerConfig      `yaml:"scheduler"`
	Backup         BackupConfig         `yaml:"backup"`
	Restore        RestoreConfig        `yaml:"restore"`
	Compliance     ComplianceConfig     `yaml:"compliance"`
	Tokens         TokenConfig          `yaml:"tokens"`
	Pages          PagesConfig          `yaml:"pages"`
	BaseURL        string               `yaml:"baseurl"`
	TrustedProxies TrustedProxiesConfig `yaml:"trusted_proxies"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	Privacy        PrivacyConfig        `yaml:"privacy"`
	Tracking       TrackingConfig       `yaml:"tracking"`
	Branding       BrandingConfig       `yaml:"branding"`
	SEO            SEOConfig            `yaml:"seo"`
	Healthz        HealthzConfig        `yaml:"healthz"`
	URLDetection   URLDetectionConfig   `yaml:"url_detection"`
}

type DebugConfig struct {
	Pprof                bool   `yaml:"pprof"`
	LogQueries           bool   `yaml:"log_queries"`
	LogCache             bool   `yaml:"log_cache"`
	LogBodies            bool   `yaml:"log_bodies"`
	MaxBodyLogSize       string `yaml:"max_body_log_size"`
	BlockProfileRate     int    `yaml:"block_profile_rate"`
	MutexProfileFraction int    `yaml:"mutex_profile_fraction"`
	RuntimeEndpoints     bool   `yaml:"runtime_endpoints"`
}

type DatabaseConfig struct {
	Driver string                   `yaml:"driver"`
	URL    string                   `yaml:"url"`
	Token  string                   `yaml:"token"`
	Pool   DatabasePoolConfig       `yaml:"pool"`
	Cache  LocalDatabaseCacheConfig `yaml:"cache"`
}

type DatabasePoolConfig struct {
	MaxOpen     int    `yaml:"max_open"`
	MaxIdle     int    `yaml:"max_idle"`
	MaxLifetime string `yaml:"max_lifetime"`
	MaxIdleTime string `yaml:"max_idle_time"`
}

type LocalDatabaseCacheConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Path         string `yaml:"path"`
	SyncInterval string `yaml:"sync_interval"`
	OfflineMode  bool   `yaml:"offline_mode"`
	MaxAge       string `yaml:"max_age"`
}

type CacheConfig struct {
	Type          string `yaml:"type"`
	URL           string `yaml:"url"`
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	DB            int    `yaml:"db"`
	TLS           bool   `yaml:"tls"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
	TLSCA         string `yaml:"tls_ca"`
	TLSCert       string `yaml:"tls_cert"`
	TLSKey        string `yaml:"tls_key"`
	PoolSize      int    `yaml:"pool_size"`
	MinIdle       int    `yaml:"min_idle"`
	Timeout       string `yaml:"timeout"`
	Prefix        string `yaml:"prefix"`
	TTL           string `yaml:"ttl"`
}

type SSLConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Cert        string            `yaml:"cert"`
	Key         string            `yaml:"key"`
	MinVersion  string            `yaml:"min_version"`
	LetsEncrypt LetsEncryptConfig `yaml:"letsencrypt"`
}

type LetsEncryptConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Email          string            `yaml:"email"`
	Challenge      string            `yaml:"challenge"`
	Staging        bool              `yaml:"staging"`
	DNSProvider    string            `yaml:"dns_provider"`
	DNSCredentials map[string]string `yaml:"dns_credentials"`
}

type HealthzConfig struct {
	Root HealthzRootConfig `yaml:"root"`
}

type HealthzRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

// BrandingConfig contains operator-provided public page content.
type BrandingConfig struct {
	Title       string                `yaml:"title"`
	Tagline     string                `yaml:"tagline"`
	Description string                `yaml:"description"`
	Favicon     string                `yaml:"favicon"`
	Logo        string                `yaml:"logo"`
	Content     BrandingContentConfig `yaml:"content"`
}

type SEOConfig struct {
	Keywords       []string              `yaml:"keywords"`
	Author         string                `yaml:"author"`
	OGImage        string                `yaml:"og_image"`
	TwitterHandle  string                `yaml:"twitter_handle"`
	TitleSeparator string                `yaml:"title_separator"`
	MetaTags       map[string]string     `yaml:"meta_tags"`
	Canonical      string                `yaml:"canonical"`
	NoIndex        bool                  `yaml:"noindex"`
	OpenGraph      SEOOpenGraphConfig    `yaml:"opengraph"`
	Twitter        SEOTwitterConfig      `yaml:"twitter"`
	Verification   SEOVerificationConfig `yaml:"verification"`
	Sitemap        SitemapConfig         `yaml:"sitemap"`
	Feed           FeedConfig            `yaml:"feed"`
}

type SEOOpenGraphConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`
	SiteName string `yaml:"site_name"`
}

type SEOTwitterConfig struct {
	Enabled bool   `yaml:"enabled"`
	Card    string `yaml:"card"`
	Creator string `yaml:"creator"`
}

type SEOVerificationConfig struct {
	Google    string                `yaml:"google"`
	Bing      string                `yaml:"bing"`
	Yandex    string                `yaml:"yandex"`
	Baidu     string                `yaml:"baidu"`
	Pinterest string                `yaml:"pinterest"`
	Facebook  string                `yaml:"facebook"`
	Custom    []SEOCustomMetaConfig `yaml:"custom"`
}

type SEOCustomMetaConfig struct {
	Name     string `yaml:"name"`
	Property string `yaml:"property"`
	Content  string `yaml:"content"`
}

type SitemapConfig struct {
	MaxURLs       int  `yaml:"max_urls"`
	IncludeImages bool `yaml:"include_images"`
}

type FeedConfig struct {
	MaxItems int    `yaml:"max_items"`
	Title    string `yaml:"title"`
}

// BrandingContentConfig contains optional public page fragments.
type BrandingContentConfig struct {
	Header  OperatorContent `yaml:"header"`
	About   OperatorContent `yaml:"about"`
	Privacy OperatorContent `yaml:"privacy"`
	Footer  OperatorContent `yaml:"footer"`
	Terms   OperatorContent `yaml:"terms"`
}

// OperatorContent stores a format and either a shared body or language map.
type OperatorContent struct {
	Format string `yaml:"format"`
	Body   any    `yaml:"body"`
}

type URLDetectionConfig struct {
	Learning     bool   `yaml:"learning"`
	MinSamples   int    `yaml:"min_samples"`
	SampleWindow string `yaml:"sample_window"`
	LogChanges   bool   `yaml:"log_changes"`
	LiveReload   bool   `yaml:"live_reload"`
}

type TrackingConfig struct {
	Type string `yaml:"type"`
	ID   string `yaml:"id"`
	URL  string `yaml:"url"`
}

func ValidateTracking(cfg *TrackingConfig) error {
	if cfg == nil {
		return errors.New("tracking configuration is required")
	}
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.Type == "" || cfg.Type == "none" {
		return nil
	}
	validURL := func(value string) bool {
		parsed, err := url.Parse(value)
		return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
	}
	switch cfg.Type {
	case "google":
		if !regexp.MustCompile(`^(UA-\d+-\d+|G-[A-Z0-9]+)$`).MatchString(cfg.ID) {
			return errors.New("invalid Google Analytics ID format")
		}
	case "matomo", "piwik":
		if _, err := strconv.Atoi(cfg.ID); err != nil {
			return fmt.Errorf("%s ID must be an integer", cfg.Type)
		}
		if !validURL(cfg.URL) {
			return fmt.Errorf("%s requires a valid URL", cfg.Type)
		}
	case "owa":
		if cfg.ID == "" || !validURL(cfg.URL) {
			return errors.New("OWA requires a site ID and valid URL")
		}
	case "fathom", "plausible":
		if cfg.ID == "" {
			return fmt.Errorf("%s requires an ID", cfg.Type)
		}
		if cfg.Type == "plausible" && !regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`).MatchString(cfg.ID) {
			return errors.New("plausible ID must be a domain")
		}
		if cfg.URL != "" && !validURL(cfg.URL) {
			return fmt.Errorf("invalid %s URL", cfg.Type)
		}
	case "umami":
		if !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(cfg.ID) || !validURL(cfg.URL) {
			return errors.New("umami requires a valid UUID and URL")
		}
	case "simple":
	case "cloudflare":
		if cfg.ID == "" {
			return errors.New("cloudflare requires a beacon token")
		}
	default:
		return fmt.Errorf("unknown tracking type: %s", cfg.Type)
	}
	return nil
}

type PrivacyConfig struct {
	Data       PrivacyDataConfig       `yaml:"data"`
	Retention  PrivacyRetentionConfig  `yaml:"retention"`
	Consent    PrivacyConsentConfig    `yaml:"consent"`
	Cookies    PrivacyCookiesConfig    `yaml:"cookies"`
	ThirdParty PrivacyThirdPartyConfig `yaml:"third_party"`
}

type PrivacyDataConfig struct {
	Sold           bool                   `yaml:"sold"`
	StoredOnServer bool                   `yaml:"stored_on_server"`
	Sharing        []PrivacySharingConfig `yaml:"sharing"`
}

type PrivacySharingConfig struct {
	Condition string `yaml:"condition"`
	When      string `yaml:"when"`
	Data      string `yaml:"data"`
}

type PrivacyRetentionConfig struct {
	Period            any  `yaml:"period"`
	ExportAvailable   bool `yaml:"export_available"`
	DeletionAvailable bool `yaml:"deletion_available"`
}

// PeriodForLanguage resolves an operator-provided retention string or language map.
func (r PrivacyRetentionConfig) PeriodForLanguage(language string) string {
	values := make(map[string]any)
	switch value := r.Period.(type) {
	case string:
		if text := strings.TrimSpace(value); text != "" {
			return value
		}
	case map[string]any:
		values = value
	case map[string]string:
		for key, text := range value {
			values[key] = text
		}
	case map[any]any:
		for key, text := range value {
			if name, ok := key.(string); ok {
				values[name] = text
			}
		}
	}
	for _, key := range []string{language, "default"} {
		if text, ok := values[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return i18n.T(language, "privacy.retention.period")
}

type PrivacyConsentConfig struct {
	ShowUntilAcknowledged bool   `yaml:"show_until_acknowledged"`
	Message               string `yaml:"message"`
	MessageIfSold         string `yaml:"message_if_sold"`
	Policy                struct {
		Text    string `yaml:"text"`
		URL     string `yaml:"url"`
		Version string `yaml:"version"`
	} `yaml:"policy"`
	Buttons struct {
		Decline string `yaml:"decline"`
		Accept  string `yaml:"accept"`
	} `yaml:"buttons"`
	ShowPreferences bool   `yaml:"show_preferences"`
	PreferencesText string `yaml:"preferences_text"`
}

type PrivacyCookieCategory struct {
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description"`
}

type PrivacyAnalyticsCookie struct {
	PrivacyCookieCategory    `yaml:",inline"`
	DescriptionSuffixNotSold string `yaml:"description_suffix_not_sold"`
	DescriptionSuffixSold    string `yaml:"description_suffix_sold"`
}

type PrivacyCookiesConfig struct {
	Essential   PrivacyCookieCategory  `yaml:"essential"`
	Preferences PrivacyCookieCategory  `yaml:"preferences"`
	Analytics   PrivacyAnalyticsCookie `yaml:"analytics"`
}

type PrivacyThirdPartyConfig struct {
	Services []PrivacyThirdPartyService `yaml:"services"`
}

type PrivacyThirdPartyService struct {
	Name      string `yaml:"name"`
	Purpose   string `yaml:"purpose"`
	DataSent  string `yaml:"data_sent"`
	PolicyURL string `yaml:"policy_url"`
}

func (p PrivacyConfig) GetConsentMessage() string {
	if p.Data.Sold {
		return p.Consent.MessageIfSold
	}
	return p.Consent.Message
}

func (p PrivacyConfig) GetAnalyticsDescription() string {
	base, suffix := p.Cookies.Analytics.Description, p.Cookies.Analytics.DescriptionSuffixNotSold
	if p.Data.Sold {
		suffix = p.Cookies.Analytics.DescriptionSuffixSold
	}
	return strings.TrimSpace(base + " " + suffix)
}

func (p PrivacyConfig) GetDataUsageContent() string {
	if p.Data.Sold {
		return textDataUsageIfSold
	}
	return textDataUsage
}

func (p PrivacyConfig) IsCCPAApplicable() bool { return p.Data.Sold }

const (
	textDataCollection  = "The catalog is embedded in the application and is not submitted by visitors. The service processes request metadata for operation and abuse prevention. Contact and security reports contain the information the sender provides."
	textDataUsage       = "Data is used to provide the service, protect it from abuse, and respond to reports. This service does not sell personal data or use it for targeted advertising."
	textDataUsageIfSold = "Data is used to provide the service, protect it from abuse, and respond to reports. Personal data may be shared or sold as described in the configured sharing policy."
	textDataSecurity    = "The service applies access controls and security protections to operational state and report data. Operators should use HTTPS, protect configuration and key material, and keep the server software current."
)

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
	Email    string            `yaml:"email"`
	Webhooks map[string]string `yaml:"webhooks"`
}

type DPOContactConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type PagesConfig struct {
	Contact ContactPageConfig `yaml:"contact"`
	Help    HelpPageConfig    `yaml:"help"`
}

type HelpPageConfig struct {
	Content string `yaml:"content"`
	MCP     string `yaml:"mcp"`
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
	WebUI   WebUINotificationConfig   `yaml:"webui"`
	Email   EmailNotificationConfig   `yaml:"email"`
	DiskLow DiskLowNotificationConfig `yaml:"disk_low"`
}

type WebUINotificationConfig struct {
	Position        string `yaml:"position"`
	Duration        int    `yaml:"duration"`
	WarningDuration int    `yaml:"warning_duration"`
}

type DiskLowNotificationConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ThresholdPercent int    `yaml:"threshold_percent"`
	Repeat           string `yaml:"repeat"`
}

type EmailNotificationConfig struct {
	Enabled bool            `yaml:"enabled"`
	SMTP    SMTPConfig      `yaml:"smtp"`
	From    EmailFromConfig `yaml:"from"`
	ReplyTo string          `yaml:"reply_to"`
	Events  map[string]bool `yaml:"events"`
}

type I18NConfig struct {
	DefaultLanguage    string   `yaml:"default_language"`
	AvailableLanguages []string `yaml:"available_languages"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	TLS      string `yaml:"tls"`
}

func (s *SMTPConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Host     string `yaml:"host"`
		Port     any    `yaml:"port"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		TLS      string `yaml:"tls"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	tlsMode := raw.TLS
	if tlsMode == "" {
		tlsMode = "auto"
	}
	*s = SMTPConfig{Host: raw.Host, Port: 587, Username: raw.Username, Password: raw.Password, TLS: tlsMode}
	if raw.Port != nil {
		port, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(raw.Port)))
		if err != nil || port < 1 || port > 65535 {
			warnInvalid("server.notifications.email.smtp.port")
		} else {
			s.Port = port
		}
	}
	return nil
}

type EmailFromConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type SecurityConfig struct {
	EncryptionKey        string                `yaml:"encryption_key"`
	EncryptionKeyVersion int                   `yaml:"encryption_key_version"`
	Reports              SecurityReportsConfig `yaml:"reports"`
	Allowlist            []AllowlistEntry      `yaml:"allowlist"`
	BlockedIPs           []BlockedIPEntry      `yaml:"blocked_ips"`
	Blocklists           BlocklistsConfig      `yaml:"blocklists"`
}

type SecurityReportsConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

type AllowlistEntry struct {
	CIDR        string `yaml:"cidr" json:"cidr"`
	Description string `yaml:"description" json:"description"`
}

type BlockedIPEntry struct {
	CIDR        string `yaml:"cidr" json:"cidr"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

type BlocklistsConfig struct {
	Enabled      bool                    `yaml:"enabled"`
	ExtraSources []BlocklistSourceConfig `yaml:"extra_sources"`
}

type BlocklistSourceConfig struct {
	ID         string `yaml:"id"`
	Type       string `yaml:"type"`
	URL        string `yaml:"url"`
	Enabled    bool   `yaml:"enabled"`
	enabledSet bool
}

func (source *BlocklistSourceConfig) UnmarshalYAML(node *yaml.Node) error {
	type plain BlocklistSourceConfig
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*source = BlocklistSourceConfig(decoded)
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == "enabled" {
			source.enabledSet = true
			break
		}
	}
	return nil
}

type GeoIPConfig struct {
	Enabled        bool                `yaml:"enabled"`
	DenyCountries  []string            `yaml:"deny_countries"`
	AllowCountries []string            `yaml:"allow_countries"`
	Presets        map[string][]string `yaml:"presets"`
}

type SchedulerConfig struct {
	Timezone      string                         `yaml:"timezone"`
	CatchUpWindow string                         `yaml:"catch_up_window"`
	Tasks         map[string]ScheduledTaskConfig `yaml:"tasks"`
}

type ScheduledTaskConfig struct {
	Enabled     bool                  `yaml:"enabled"`
	Schedule    string                `yaml:"schedule"`
	RetryOnFail bool                  `yaml:"retry_on_fail"`
	RetryDelay  string                `yaml:"retry_delay"`
	Retention   BackupRetentionConfig `yaml:"retention"`
}

type MaintenanceConfig struct {
	SelfHealing MaintenanceSelfHealingConfig `yaml:"self_healing"`
	Cleanup     MaintenanceCleanupConfig     `yaml:"cleanup"`
	Notify      MaintenanceNotifyConfig      `yaml:"notify"`
}

type MaintenanceSelfHealingConfig struct {
	RetryInterval string `yaml:"retry_interval"`
	MaxAttempts   int    `yaml:"max_attempts"`
}

type MaintenanceCleanupConfig struct {
	DiskThreshold    int `yaml:"disk_threshold"`
	LogRetentionDays int `yaml:"log_retention_days"`
}

type MaintenanceNotifyConfig struct {
	OnEnter bool `yaml:"on_enter"`
	OnExit  bool `yaml:"on_exit"`
}

type BackupConfig struct {
	Encryption         BackupEncryptionConfig `yaml:"encryption"`
	EncryptionPassword string                 `yaml:"encryption_password"`
	Retention          BackupRetentionConfig  `yaml:"retention"`
	DiskThreshold      int                    `yaml:"disk_threshold"`
}

type BackupEncryptionConfig struct {
	Enabled bool `yaml:"enabled"`
}

type BackupRetentionConfig struct {
	MaxBackups   int    `yaml:"max_backups"`
	KeepWeekly   int    `yaml:"keep_weekly"`
	KeepMonthly  int    `yaml:"keep_monthly"`
	KeepYearly   int    `yaml:"keep_yearly"`
	MaxTotalSize string `yaml:"max_total_size"`
}

type RestoreConfig struct {
	MaxArchiveSize  string `yaml:"max_archive_size"`
	MaxExpandedSize string `yaml:"max_expanded_size"`
	MaxFiles        int    `yaml:"max_files"`
	MaxFileSize     string `yaml:"max_file_size"`
	MaxRatio        int    `yaml:"max_ratio"`
}

type ComplianceConfig struct {
	GDPR     bool `yaml:"gdpr"`
	CCPA     bool `yaml:"ccpa"`
	HIPAA    bool `yaml:"hipaa"`
	SOC2     bool `yaml:"soc2"`
	PCIDSS   bool `yaml:"pci_dss"`
	ISO27001 bool `yaml:"iso27001"`
	FedRAMP  bool `yaml:"fedramp"`
	LGPD     bool `yaml:"lgpd"`
	PIPEDA   bool `yaml:"pipeda"`
	APPI     bool `yaml:"appi"`
	PDPA     bool `yaml:"pdpa"`
}

func (c ComplianceConfig) AnyEnabled() bool {
	return c.GDPR || c.CCPA || c.HIPAA || c.SOC2 || c.PCIDSS || c.ISO27001 || c.FedRAMP || c.LGPD || c.PIPEDA || c.APPI || c.PDPA
}

type DataConfig struct {
	SecurityDir string      `yaml:"security_dir"`
	CVE         CVEConfig   `yaml:"cve"`
	Trivy       TrivyConfig `yaml:"trivy"`
}

type CVEConfig struct {
	Source      string `yaml:"source"`
	FilterByCPE bool   `yaml:"filter_by_cpe"`
	APIKey      string `yaml:"api_key"`
}

type TrivyConfig struct {
	Enabled bool   `yaml:"enabled"`
	Source  string `yaml:"source"`
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

type CLIConfig struct {
	BinaryDownload CLIBinaryDownloadConfig `yaml:"binary_download"`
}

type CLIBinaryDownloadConfig struct {
	Dir         string `yaml:"dir"`
	RequireAuth bool   `yaml:"require_auth"`
}

type TorConfig struct {
	OnionAddress              string `yaml:"onion_address"`
	Expose                    bool   `yaml:"expose"`
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
	Expose           bool   `yaml:"expose"`
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
	AccessFormat string            `yaml:"access_format"`
	Level        string            `yaml:"level"`
	Defaults     LogDefaultsConfig `yaml:"defaults"`
	Access       LogKindConfig     `yaml:"access"`
	Server       LogKindConfig     `yaml:"server"`
	Error        LogKindConfig     `yaml:"error"`
	App          LogKindConfig     `yaml:"app"`
	Auth         LogKindConfig     `yaml:"auth"`
	Audit        LogKindConfig     `yaml:"audit"`
	Security     LogKindConfig     `yaml:"security"`
	Debug        LogKindConfig     `yaml:"debug"`
}

type LogDefaultsConfig struct {
	Custom string `yaml:"custom"`
	Rotate string `yaml:"rotate"`
	Keep   string `yaml:"keep"`
}

type LogKindConfig struct {
	Enabled  bool            `yaml:"enabled"`
	Filename string          `yaml:"filename"`
	Format   string          `yaml:"format"`
	Rotate   string          `yaml:"rotate"`
	Keep     string          `yaml:"keep"`
	Compress bool            `yaml:"compress"`
	Events   map[string]bool `yaml:"events"`
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
	Headers  WebHeadersConfig  `yaml:"headers"`
}

type WebHeadersConfig struct {
	HonorDNT bool `yaml:"honor_dnt"`
}

// WebRobotsConfig contains robots.txt settings
type WebRobotsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WebSecurityConfig contains security settings
type WebSecurityConfig struct {
	Contact       string   `yaml:"contact"`
	Policy        string   `yaml:"policy"`
	ReportURL     string   `yaml:"report_url"`
	PublishPGPKey bool     `yaml:"publish_pgp_key"`
	Keyservers    []string `yaml:"keyservers"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type CSRFConfig struct {
	Enabled     bool     `yaml:"enabled"`
	TokenLength int      `yaml:"token_length"`
	CookieName  string   `yaml:"cookie_name"`
	HeaderName  string   `yaml:"header_name"`
	Secure      string   `yaml:"secure"`
	ExemptPaths []string `yaml:"exempt_paths"`
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
		CLI: CLIConfig{BinaryDownload: CLIBinaryDownloadConfig{Dir: "{data_dir}/cli"}},
		Server: ServerConfig{
			Port:       initialPort(),
			PIDFile:    true,
			APIVersion: DefaultAPIVersion,
			SSL:        SSLConfig{MinVersion: "TLS1.2", LetsEncrypt: LetsEncryptConfig{Challenge: "http-01"}},
			Database:   DatabaseConfig{Driver: "sqlite", Pool: DatabasePoolConfig{MaxOpen: 5, MaxIdle: 2, MaxLifetime: "5m", MaxIdleTime: "1m"}, Cache: LocalDatabaseCacheConfig{SyncInterval: "30s", OfflineMode: true, MaxAge: "1h"}},
			Cache:      CacheConfig{Type: "memory", Host: "localhost", Port: 6379, TLSSkipVerify: true, PoolSize: 10, MinIdle: 2, Timeout: "5s", Prefix: "gitmessages:", TTL: "1h"},
			FQDN:       "",
			Address:    initialAddress(),
			Mode:       "production",
			Debug:      DebugConfig{Pprof: true, LogQueries: true, LogCache: true, MaxBodyLogSize: "10KB", BlockProfileRate: 1, MutexProfileFraction: 1, RuntimeEndpoints: true},
			Branding: BrandingConfig{
				Title:       "Git Messages",
				Tagline:     "A curated reference for git commit message conventions.",
				Description: "Gitmessages is a full-stack Go web application serving a curated reference for git commit message conventions, with an interactive message builder, REST and GraphQL APIs, and a companion CLI.",
			},
			I18N: I18NConfig{DefaultLanguage: "en", AvailableLanguages: []string{"en", "es", "fr", "de", "it", "ar"}},
			SEO: SEOConfig{
				TitleSeparator: " - ",
				OpenGraph:      SEOOpenGraphConfig{Enabled: true, Type: "website"},
				Twitter:        SEOTwitterConfig{Enabled: true, Card: "summary_large_image"},
				Sitemap:        SitemapConfig{MaxURLs: 50000},
				Feed:           FeedConfig{MaxItems: 50},
			},
			Tokens:    TokenConfig{MaxLinked: 25, CookieMaxAge: "1y", IdlePurgeDays: 0},
			GeoIP:     GeoIPConfig{Enabled: true, DenyCountries: []string{}, AllowCountries: []string{}, Presets: map[string][]string{}},
			Scheduler: defaultSchedulerConfig(),
			Backup: BackupConfig{
				Retention:     BackupRetentionConfig{MaxBackups: 1, MaxTotalSize: "10%"},
				DiskThreshold: 90,
			},
			Restore:  RestoreConfig{MaxArchiveSize: "1G", MaxExpandedSize: "4G", MaxFiles: 10000, MaxFileSize: "512M", MaxRatio: 100},
			Security: SecurityConfig{Reports: SecurityReportsConfig{RetentionDays: 365}, Blocklists: BlocklistsConfig{Enabled: true, ExtraSources: []BlocklistSourceConfig{}}},
			RateLimit: RateLimitConfig{
				Read:         RateLimitBucketConfig{Requests: 120, Window: 60},
				Write:        RateLimitBucketConfig{Requests: 10, Window: 60},
				Health:       RateLimitBucketConfig{Requests: 120, Window: 60},
				Strict:       RateLimitBucketConfig{Requests: 10, Window: 900},
				AuthFailures: RateLimitBucketConfig{Requests: 5, Window: 900}, GlobalBurst: 240,
			},
			Privacy:      defaultPrivacyConfig(),
			URLDetection: URLDetectionConfig{Learning: true, MinSamples: 3, SampleWindow: "5m", LogChanges: true, LiveReload: true},
			Update:       UpdateConfig{Branch: "stable", AutoInstall: false, DeferDays: 0},
			CORS:         CORSConfig{AllowedOrigins: []string{"*"}},
			CSRF:         CSRFConfig{Enabled: true, TokenLength: 32, CookieName: "csrf_token", HeaderName: "X-CSRF-Token", Secure: "auto", ExemptPaths: []string{}},
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
				Level:        "",
				Defaults:     LogDefaultsConfig{Rotate: "weekly,50MB", Keep: "none"},
				Access:       LogKindConfig{Format: "apache", Rotate: "monthly"},
				Server:       LogKindConfig{Format: "text"},
				Error:        LogKindConfig{Format: "text"},
				App:          LogKindConfig{Format: "logfmt"},
				Auth:         LogKindConfig{Format: "syslog"},
				Audit:        LogKindConfig{Enabled: true, Format: "json", Rotate: "daily"},
				Security:     LogKindConfig{Format: "fail2ban"},
				Debug:        LogKindConfig{Format: "text"},
			},
			Maintenance: MaintenanceConfig{
				SelfHealing: MaintenanceSelfHealingConfig{RetryInterval: "30s", MaxAttempts: 0},
				Cleanup:     MaintenanceCleanupConfig{DiskThreshold: 90, LogRetentionDays: 7},
				Notify:      MaintenanceNotifyConfig{OnEnter: true, OnExit: true},
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
			Notifications: NotificationsConfig{
				WebUI: WebUINotificationConfig{Position: "top-right", Duration: 3, WarningDuration: 5},
				Email: EmailNotificationConfig{
					SMTP: SMTPConfig{Port: 587, TLS: "auto"},
					Events: map[string]bool{
						"startup": false, "shutdown": false, "backup_complete": false, "backup_failed": true,
						"ssl_expiring": true, "ssl_renewed": false, "ssl_renewal_failed": true, "security_alert": true,
						"scheduler_error": true, "update_available": false, "update_installed": true, "update_failed": true,
						"update_staged": true, "database_error": true,
						"maintenance_entered": true, "maintenance_exited": true, "self_healing_exhausted": true,
					},
				},
				DiskLow: DiskLowNotificationConfig{Enabled: true, ThresholdPercent: 85, Repeat: "24h"},
			},
			Pages: PagesConfig{
				Contact: ContactPageConfig{
					Enabled: true, Captcha: "simple", SuccessMessage: "Thank you for your report.",
					Antispam: ContactAntispamConfig{
						Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200,
						PerIP: 5, PerEmail: 3, Global: 60, Window: "1h", MaxLinks: 10,
						RejectMarkup: true, Strikes: 3, BanHours: 24,
						ReviewMinSeconds: 2, ReviewMaxSeconds: 900, DuplicateWindowHours: 24,
					},
				},
				Help: HelpPageConfig{
					Content: "Git Messages provides a curated catalog of commit types, conventions and templates, plus random examples and a stateless message builder. Read-only catalog endpoints are public; the message builder validates and formats input without storing it.",
					MCP:     "Use `list_commit_types`, `list_conventions`, `list_templates` and `get_random_example` to browse the curated catalog. Use `build_commit_message` to validate and format a commit message. For example, ask: **Build a Conventional Commit message of type `feat`, scope `api`, and description `add message search`.** The MCP tools do not accept catalog submissions or manage tokens.",
				},
			},
		},
		Data: DataConfig{CVE: CVEConfig{Source: "https://services.nvd.nist.gov/rest/json/cves/2.0"}, Trivy: TrivyConfig{Enabled: false, Source: "ghcr.io/aquasecurity/trivy-db:2"}},
		Web: WebConfig{
			UI: WebUIConfig{Theme: "dark", Accent: "#bd93f9"},
			Robots: WebRobotsConfig{
				Allow: []string{"/", "/api"},
				Deny:  []string{"/debug"},
			},
			Security: WebSecurityConfig{Contact: "", PublishPGPKey: true, Keyservers: []string{}},
		},
	}
}

func defaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Timezone: "America/New_York", CatchUpWindow: "1h",
		Tasks: map[string]ScheduledTaskConfig{
			"ssl_renewal":        {Enabled: true, Schedule: "0 3 * * *"},
			"geoip_update":       {Enabled: true, Schedule: "0 3 * * 0"},
			"blocklist_update":   {Enabled: true, Schedule: "0 4 * * *", RetryOnFail: true, RetryDelay: "1h"},
			"security_cleanup":   {Enabled: true, Schedule: "30 3 * * *"},
			"keyserver_retry":    {Enabled: true, Schedule: "@every 15m"},
			"cve_update":         {Enabled: true, Schedule: "0 5 * * *", RetryOnFail: true, RetryDelay: "1h"},
			"update_check":       {Enabled: true, Schedule: "0 6 * * *"},
			"token_cleanup":      {Enabled: true, Schedule: "@every 15m"},
			"log_rotation":       {Enabled: true, Schedule: "0 0 * * *"},
			"backup_daily":       {Enabled: true, Schedule: "0 2 * * *"},
			"backup_hourly":      {Enabled: false, Schedule: "@hourly"},
			"disk_check":         {Enabled: true, Schedule: "@every 5m"},
			"database_check":     {Enabled: true, Schedule: "@every 1m"},
			"health_stats_flush": {Enabled: true, Schedule: "@every 30s"},
			"healthcheck_self":   {Enabled: true, Schedule: "@every 5m"},
			"tor_health":         {Enabled: true, Schedule: "@every 10m"},
			"i2p_health":         {Enabled: true, Schedule: "@every 10m"},
		},
	}
}

func defaultPrivacyConfig() PrivacyConfig {
	privacy := PrivacyConfig{
		Data: PrivacyDataConfig{
			Sold: false, StoredOnServer: true,
			Sharing: []PrivacySharingConfig{{Condition: "email", When: "SMTP is configured for sending contact or security-report email", Data: "Sender email address and report content"}},
		},
		Retention: PrivacyRetentionConfig{Period: "", ExportAvailable: true, DeletionAvailable: true},
		Cookies: PrivacyCookiesConfig{
			Essential:   PrivacyCookieCategory{Enabled: true, Description: "Required for the site to function. Includes the owner cookie, security tokens (CSRF) and the consent cookie itself. The theme and language preferences are not essential and require consent."},
			Preferences: PrivacyCookieCategory{Enabled: true, Description: "Remember settings such as theme, language and interface preferences after consent."},
			Analytics:   PrivacyAnalyticsCookie{PrivacyCookieCategory: PrivacyCookieCategory{Enabled: true, Description: "Help understand how visitors use the site."}, DescriptionSuffixNotSold: "Analytics data is anonymized and never sold.", DescriptionSuffixSold: "Analytics data may be shared with third parties."},
		},
		ThirdParty: PrivacyThirdPartyConfig{Services: []PrivacyThirdPartyService{}},
	}
	privacy.Consent.ShowUntilAcknowledged = true
	privacy.Consent.Message = "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data is stored on our servers and is never sold."
	privacy.Consent.MessageIfSold = "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data may be shared with or sold to third parties as described in our Privacy Policy."
	privacy.Consent.Policy.Text = "Privacy Policy"
	privacy.Consent.Policy.URL = "/server/privacy"
	privacy.Consent.Policy.Version = "1"
	privacy.Consent.Buttons.Decline = "Decline"
	privacy.Consent.Buttons.Accept = "I Agree"
	privacy.Consent.ShowPreferences = true
	privacy.Consent.PreferencesText = "Manage Preferences"
	return privacy
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
	if port == "" {
		return false
	}
	parts := strings.Split(port, ",")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 5 {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	return true
}

func runningInContainer() bool {
	pid1 := ""
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		pid1 = strings.TrimSpace(string(data))
	}
	if isSystemManagerHost(os.Getenv("INVOCATION_ID"), os.Getenv("RC_SVCNAME"), pid1, pathExists("/run/openrc")) {
		return false
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv", "/dev/lxc"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if strings.TrimSpace(os.Getenv("container")) != "" || strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != "" {
		return true
	}
	if pid1 != "" {
		name := strings.ToLower(pid1)
		if name == "tini" || name == "dumb-init" || name == "s6-svscan" || name == "runsv" || name == "runsvdir" || name == "catatonit" {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		text := strings.ToLower(string(data))
		for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "lxc"} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

func isSystemManagerHost(invocationID, rcServiceName, pid1 string, openRCRunning bool) bool {
	pid1 = strings.TrimSpace(pid1)
	return strings.TrimSpace(invocationID) != "" || strings.TrimSpace(rcServiceName) != "" || pid1 == "systemd" || pid1 == "openrc-init" || pid1 == "init" && openRCRunning
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	_, statErr := os.Stat(path)
	firstRun := errors.Is(statErr, os.ErrNotExist)

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
	if err := seedConnectionEnvironment(path, firstRun); err != nil {
		return nil, fmt.Errorf("seed connection configuration: %w", err)
	}
	if err := ensureGeneratedSecrets(filepath.Join(filepath.Dir(path), "secrets.yml")); err != nil {
		return nil, fmt.Errorf("failed to initialize server credentials: %w", err)
	}
	if err := encryptDNSCredentials(filepath.Join(filepath.Dir(path), "secrets.yml")); err != nil {
		return nil, fmt.Errorf("failed to secure DNS credentials: %w", err)
	}
	cfg, err := loadMergedConfig(path)
	if err != nil {
		return nil, err
	}
	if err := applySMTPEnvironment(cfg); err != nil {
		return nil, err
	}
	warnConnectionEnvironmentMismatch(cfg)
	validateSEOSettings(cfg)
	migrateAreaFiles(path)

	current = cfg
	return cfg, nil
}

func validateSEOSettings(configuration *Config) {
	if configuration == nil {
		return
	}
	seo := &configuration.Server.SEO
	if len(seo.TitleSeparator) > 8 {
		seo.TitleSeparator = " - "
		fmt.Fprintln(os.Stderr, "warning: invalid configuration value for server.seo.title_separator; using the default")
	}
	if canonical := strings.TrimSpace(seo.Canonical); canonical != "" {
		parsed, err := url.Parse(canonical)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			seo.Canonical = ""
			fmt.Fprintln(os.Stderr, "warning: invalid configuration value for server.seo.canonical; using the default")
		}
	}
	if seo.Twitter.Card != "summary" && seo.Twitter.Card != "summary_large_image" {
		seo.Twitter.Card = "summary_large_image"
		fmt.Fprintln(os.Stderr, "warning: invalid configuration value for server.seo.twitter.card; using the default")
	}
	reserved := map[string]struct{}{"description": {}, "keywords": {}, "author": {}, "robots": {}, "viewport": {}}
	metaTags := make(map[string]string)
	metaName := regexp.MustCompile(`^[a-z0-9:_.-]+$`)
	metaKeys := make([]string, 0, len(seo.MetaTags))
	for key := range seo.MetaTags {
		metaKeys = append(metaKeys, key)
	}
	sort.Strings(metaKeys)
	for _, key := range metaKeys {
		if len(metaTags) >= 20 {
			fmt.Fprintln(os.Stderr, "warning: server.seo.meta_tags has more than 20 entries; ignoring additional entries")
			break
		}
		value := strings.TrimSpace(seo.MetaTags[key])
		_, isReserved := reserved[key]
		if !metaName.MatchString(key) || isReserved || value == "" {
			fmt.Fprintln(os.Stderr, "warning: invalid configuration entry server.seo.meta_tags; skipping")
			continue
		}
		metaTags[key] = value
	}
	seo.MetaTags = metaTags
	verification := &seo.Verification
	patterns := map[string]*regexp.Regexp{
		"google": regexp.MustCompile(`^[A-Za-z0-9_-]{1,43}$`), "bing": regexp.MustCompile(`^[A-F0-9]{1,32}$`),
		"yandex": regexp.MustCompile(`^[a-f0-9]{1,32}$`), "baidu": regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`),
		"pinterest": regexp.MustCompile(`^[a-f0-9]{1,32}$`), "facebook": regexp.MustCompile(`^[a-z0-9]{1,64}$`),
	}
	providers := []struct {
		key   string
		value *string
	}{{"google", &verification.Google}, {"bing", &verification.Bing}, {"yandex", &verification.Yandex}, {"baidu", &verification.Baidu}, {"pinterest", &verification.Pinterest}, {"facebook", &verification.Facebook}}
	for _, provider := range providers {
		if value := strings.TrimSpace(*provider.value); value != "" && !patterns[provider.key].MatchString(value) {
			*provider.value = ""
			fmt.Fprintf(os.Stderr, "warning: invalid configuration value for server.seo.verification.%s; using the default\n", provider.key)
		}
	}
	customTags := make([]SEOCustomMetaConfig, 0, len(verification.Custom))
	customName := regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)
	for _, tag := range verification.Custom {
		name, property, content := strings.TrimSpace(tag.Name), strings.TrimSpace(tag.Property), strings.TrimSpace(tag.Content)
		if content == "" || len(content) > 256 || (name == "") == (property == "") || name != "" && !customName.MatchString(name) || property != "" && !customName.MatchString(property) {
			fmt.Fprintln(os.Stderr, "warning: invalid configuration entry server.seo.verification.custom; skipping")
			continue
		}
		tag.Name, tag.Property, tag.Content = name, property, content
		customTags = append(customTags, tag)
	}
	verification.Custom = customTags
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
		if err := os.Chmod(path, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not secure config file %s: %v\n", path, err)
		}
	}
	directory := filepath.Join(filepath.Dir(path), "yaml.d")
	if info, err := os.Stat(directory); err == nil && info.IsDir() {
		if chmodErr := os.Chmod(directory, 0o700); chmodErr != nil {
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
				if chmodErr := os.Chmod(resolved, 0o600); chmodErr != nil {
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
		if chmodErr := os.Chmod(secrets, 0o600); chmodErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not secure secrets file: %v\n", chmodErr)
		}
		mergeMaps(merged, secretMap)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: ignoring invalid secrets file: %v\n", err)
	}
	migrateLegacyConfig(merged)
	expandEnvironmentReferences(merged)
	encoded, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged configuration: %w", err)
	}
	cfg := DefaultConfig()
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode merged configuration: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	if err := decryptDNSCredentials(cfg); err != nil {
		return nil, fmt.Errorf("decrypt DNS credentials: %w", err)
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
	if legacyCLI, ok := server["cli"]; ok {
		if legacyCLIMap, ok := legacyCLI.(map[string]any); ok {
			cli := nestedMap(root, "cli")
			mergeMaps(cli, legacyCLIMap)
			root["cli"] = cli
		} else {
			root["cli"] = legacyCLI
		}
		delete(server, "cli")
	}
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

// SaveWebSecurity persists the web security area without rewriting unrelated configuration.
func SaveWebSecurity() error {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || configPath == "" {
		return fmt.Errorf("no configuration loaded")
	}
	return saveArea(configPath, "web.yml", map[string]any{"web": map[string]any{"security": current.Web.Security}})
}

// SaveEncryptionKey persists a rotated encryption key and version in secrets.yml.
func SaveEncryptionKey(value string) error {
	mu.Lock()
	defer mu.Unlock()
	if current == nil || configPath == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("loaded configuration and encryption key are required")
	}
	secretPath := filepath.Join(filepath.Dir(configPath), "secrets.yml")
	secrets, err := readConfigMap(secretPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read secrets configuration: %w", err)
	}
	if secrets == nil {
		secrets = make(map[string]any)
	}
	oldKey, err := decodeConfigEncryptionKey(current.Server.Security.EncryptionKey)
	if err != nil {
		return fmt.Errorf("decode current encryption key: %w", err)
	}
	newKey, err := decodeConfigEncryptionKey(value)
	if err != nil {
		return fmt.Errorf("decode replacement encryption key: %w", err)
	}
	if err := reencryptDNSCredentialsMap(secrets, oldKey, newKey); err != nil {
		return fmt.Errorf("re-encrypt DNS credentials: %w", err)
	}
	version := current.Server.Security.EncryptionKeyVersion + 1
	putConfigPath(secrets, []string{"server", "security", "encryption_key"}, value)
	putConfigPath(secrets, []string{"server", "security", "encryption_key_version"}, version)
	encoded, err := yaml.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encode secrets configuration: %w", err)
	}
	if err := writePrivateAtomic(secretPath, encoded); err != nil {
		return fmt.Errorf("persist encryption key: %w", err)
	}
	current.Server.Security.EncryptionKey = value
	current.Server.Security.EncryptionKeyVersion = version
	return nil
}

func reencryptDNSCredentialsMap(secrets map[string]any, oldKey, newKey []byte) error {
	value, ok := getConfigPath(secrets, []string{"server", "ssl", "letsencrypt", "dns_credentials"})
	if !ok {
		return nil
	}
	credentials, ok := value.(map[string]any)
	if !ok {
		return errors.New("server.ssl.letsencrypt.dns_credentials must be a flat mapping")
	}
	for name, value := range credentials {
		credential, ok := value.(string)
		if !ok {
			return fmt.Errorf("DNS credential %s must be a string", name)
		}
		plain := credential
		if strings.HasPrefix(credential, "enc:v1:") {
			var err error
			plain, err = openDNSCredential(oldKey, strings.TrimPrefix(credential, "enc:v1:"))
			if err != nil {
				return fmt.Errorf("decrypt DNS credential %s", name)
			}
		}
		sealed, err := sealDNSCredential(newKey, plain)
		if err != nil {
			return fmt.Errorf("encrypt DNS credential %s: %w", name, err)
		}
		credentials[name] = sealed
	}
	putConfigPath(secrets, []string{"server", "ssl", "letsencrypt", "dns_credentials"}, credentials)
	return nil
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
	server := map[string]any{"port": cfg.Server.Port, "address": cfg.Server.Address, "fqdn": cfg.Server.FQDN, "mode": cfg.Server.Mode, "daemonize": cfg.Server.Daemonize, "pidfile": cfg.Server.PIDFile, "ssl": cfg.Server.SSL, "database": cfg.Server.Database, "cache": cfg.Server.Cache, "baseurl": cfg.Server.BaseURL, "trusted_proxies": cfg.Server.TrustedProxies, "rate_limit": cfg.Server.RateLimit, "url_detection": cfg.Server.URLDetection}
	data, err := yaml.Marshal(map[string]any{"server": server})
	if err != nil {
		return "# Git Messages Server Configuration\n"
	}
	return "# Settings of this node only (listen, identity, database, cache, ssl). Everything else lives in yaml.d/: branding.yml, web.yml, pages.yml, contact.yml, privacy.yml, security.yml, logs.yml, metrics.yml, notifications.yml, tor.yml, i2p.yml, maintenance.yml, app.yml. Credentials live in secrets.yml.\n" + string(data)
}

func saveArea(configFile, name string, values map[string]any) error {
	areaDirectory := filepath.Join(filepath.Dir(configFile), "yaml.d")
	if err := os.MkdirAll(areaDirectory, 0o700); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
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
	contactSettings, err := readWebhookSettings(configPath)
	if err != nil {
		return fmt.Errorf("read webhook settings: %w", err)
	}
	for role, transports := range contactSettings {
		for transport, endpoint := range transports {
			if strings.TrimSpace(endpoint) == "" {
				continue
			}
			secretPath := []string{"server", "contact", role, "webhooks", transport + "_secret"}
			if value, exists := getConfigPath(secrets, secretPath); exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
				continue
			}
			raw := make([]byte, 32)
			if _, err := cryptorand.Read(raw); err != nil {
				return fmt.Errorf("generate %s webhook secret: %w", role+"."+transport, err)
			}
			putConfigPath(secrets, secretPath, base64.RawURLEncoding.EncodeToString(raw))
			changed = true
			generated = append(generated, strings.Join(secretPath, "."))
		}
	}
	operator, ok := getConfigPath(secrets, []string{"server", "token"})
	if !ok || strings.TrimSpace(fmt.Sprint(operator)) == "" {
		token, err := generatedToken("adm_")
		if err != nil {
			return fmt.Errorf("generate operator token: %w", err)
		}
		putConfigPath(secrets, []string{"server", "token"}, token)
		changed = true
		generated = append(generated, "server.token")
		fmt.Fprintln(os.Stderr, "Operator token generated and stored in secrets configuration.")
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

func readWebhookSettings(path string) (map[string]map[string]string, error) {
	root := make(map[string]any)
	for _, candidate := range []string{path, filepath.Join(filepath.Dir(path), "yaml.d", "contact.yml")} {
		values, err := readConfigMap(candidate)
		if err != nil {
			continue
		}
		mergeMaps(root, values)
	}
	server := nestedMap(root, "server")
	contact := nestedMap(server, "contact")
	result := make(map[string]map[string]string)
	for _, role := range []string{"admin", "security", "abuse", "general"} {
		roleConfig := nestedMap(contact, role)
		webhooks := nestedMap(roleConfig, "webhooks")
		if len(webhooks) == 0 {
			continue
		}
		result[role] = make(map[string]string)
		for transport, value := range webhooks {
			if strings.HasSuffix(transport, "_secret") {
				continue
			}
			if endpoint, ok := value.(string); ok {
				result[role][transport] = endpoint
			}
		}
	}
	return result, nil
}

func encryptDNSCredentials(path string) error {
	secrets, err := readConfigMap(path)
	if err != nil {
		return fmt.Errorf("read secrets file: %w", err)
	}
	changed, err := encryptDNSCredentialMap(secrets)
	if err != nil || !changed {
		return err
	}
	encoded, err := yaml.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encode secrets file: %w", err)
	}
	return writePrivateAtomic(path, encoded)
}

func encryptDNSCredentialMap(secrets map[string]any) (bool, error) {
	credentialsValue, ok := getConfigPath(secrets, []string{"server", "ssl", "letsencrypt", "dns_credentials"})
	if !ok {
		return false, nil
	}
	credentials, ok := credentialsValue.(map[string]any)
	if !ok {
		return false, errors.New("server.ssl.letsencrypt.dns_credentials must be a flat mapping")
	}
	keyValue, ok := getConfigPath(secrets, []string{"server", "security", "encryption_key"})
	if !ok {
		hasPlaintext := false
		hasEncrypted := false
		for _, value := range credentials {
			text, isString := value.(string)
			if isString && text != "" && !strings.HasPrefix(text, "enc:v1:") {
				hasPlaintext = true
				break
			}
			if isString && strings.HasPrefix(text, "enc:v1:") {
				hasEncrypted = true
			}
		}
		if !hasPlaintext {
			if !hasEncrypted {
				return false, nil
			}
			return false, errors.New("server.security.encryption_key is required for encrypted DNS credentials")
		}
		key := make([]byte, 32)
		if _, err := cryptorand.Read(key); err != nil {
			return false, fmt.Errorf("generate encryption key: %w", err)
		}
		keyValue = base64.RawURLEncoding.EncodeToString(key)
		putConfigPath(secrets, []string{"server", "security", "encryption_key"}, keyValue)
		putConfigPath(secrets, []string{"server", "security", "encryption_key_version"}, 1)
		generatedSecretMu.Lock()
		generatedSecretNames = append(generatedSecretNames, "server.security.encryption_key")
		generatedSecretMu.Unlock()
	}
	key, err := decodeConfigEncryptionKey(fmt.Sprint(keyValue))
	if err != nil {
		return false, err
	}
	changed := false
	for name, value := range credentials {
		plain, ok := value.(string)
		if !ok || strings.HasPrefix(plain, "enc:v1:") {
			continue
		}
		sealed, err := sealDNSCredential(key, plain)
		if err != nil {
			return false, fmt.Errorf("encrypt DNS credential %s: %w", name, err)
		}
		credentials[name] = sealed
		changed = true
	}
	if !changed {
		return false, nil
	}
	putConfigPath(secrets, []string{"server", "ssl", "letsencrypt", "dns_credentials"}, credentials)
	return true, nil
}

func decryptDNSCredentials(configuration *Config) error {
	if configuration == nil {
		return nil
	}
	var key []byte
	for name, value := range configuration.Server.SSL.LetsEncrypt.DNSCredentials {
		if !strings.HasPrefix(value, "enc:v1:") {
			continue
		}
		if key == nil {
			var err error
			key, err = decodeConfigEncryptionKey(configuration.Server.Security.EncryptionKey)
			if err != nil {
				return err
			}
		}
		plain, err := openDNSCredential(key, strings.TrimPrefix(value, "enc:v1:"))
		if err != nil {
			return fmt.Errorf("invalid encrypted DNS credential %s", name)
		}
		configuration.Server.SSL.LetsEncrypt.DNSCredentials[name] = plain
	}
	return nil
}

func decodeConfigEncryptionKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("server.security.encryption_key must contain 32 encoded bytes")
	}
	return key, nil
}

func sealDNSCredential(key []byte, value string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := cryptorand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(value), nil)
	return "enc:v1:" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func openDNSCredential(key []byte, value string) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return "", errors.New("invalid DNS credential ciphertext")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	return string(plain), err
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
	secretPaths := [][]string{
		{"server", "token"},
		{"server", "security", "encryption_key"},
		{"server", "security", "encryption_key_version"},
		{"server", "cache", "password"},
		{"server", "database", "token"},
		{"server", "notifications", "email", "smtp", "password"},
		{"server", "pages", "contact", "captcha_secret_key"},
		{"server", "ssl", "letsencrypt", "dns_credentials"},
		{"server", "backup", "encryption_password"},
		{"server", "metrics", "auth", "tokens"},
		{"server", "contact", "admin", "webhooks"},
		{"server", "contact", "security", "webhooks"},
		{"server", "contact", "abuse", "webhooks"},
		{"server", "contact", "general", "webhooks"},
		{"data", "cve", "api_key"},
	}
	changed := false
	for i := range sources {
		for _, keyPath := range secretPaths {
			if value, ok := takeConfigPath(sources[i].values, keyPath); ok {
				putConfigPath(secrets, keyPath, value)
				sources[i].changed = true
				changed = true
			}
		}
		for _, keyPath := range connectionURLSecretPaths {
			if value, ok := getConfigPath(sources[i].values, keyPath); ok && connectionURLHasCredentials(fmt.Sprint(value)) {
				if value, ok := takeConfigPath(sources[i].values, keyPath); ok {
					putConfigPath(secrets, keyPath, value)
					sources[i].changed = true
					changed = true
				}
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
	if _, err := encryptDNSCredentialMap(existing); err != nil {
		return fmt.Errorf("encrypt migrated DNS credentials: %w", err)
	}
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
	if cli, ok := original["cli"]; ok {
		if areas["maintenance.yml"] == nil {
			areas["maintenance.yml"] = make(map[string]any)
		}
		areas["maintenance.yml"]["cli"] = cli
		delete(original, "cli")
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
		area := "app.yml"
		if key == "data" {
			area = "security.yml"
		}
		if areas[area] == nil {
			areas[area] = make(map[string]any)
		}
		areas[area][key] = value
		delete(original, key)
	}
	secrets := make(map[string]any)
	secretPaths := [][]string{
		{"server", "token"},
		{"server", "security", "encryption_key"},
		{"server", "security", "encryption_key_version"},
		{"server", "notifications", "email", "smtp", "password"},
		{"server", "cache", "password"},
		{"server", "database", "token"},
		{"server", "pages", "contact", "captcha_secret_key"},
		{"server", "ssl", "letsencrypt", "dns_credentials"},
		{"server", "backup", "encryption_password"},
		{"server", "metrics", "auth", "tokens"},
		{"server", "contact", "admin", "webhooks"},
		{"server", "contact", "security", "webhooks"},
		{"server", "contact", "abuse", "webhooks"},
		{"server", "contact", "general", "webhooks"},
		{"server", "auth", "ldap", "providers"},
		{"server", "auth", "oidc", "providers"},
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
	for _, path := range connectionURLSecretPaths {
		if value, ok := getConfigPath(original, path); ok && connectionURLHasCredentials(fmt.Sprint(value)) {
			if value, ok := takeConfigPath(original, path); ok {
				putConfigPath(secrets, path, value)
			}
		}
		for _, name := range areaNames {
			values := areas[name]
			if value, ok := getConfigPath(values, path); ok && connectionURLHasCredentials(fmt.Sprint(value)) {
				if value, ok := takeConfigPath(values, path); ok {
					putConfigPath(secrets, path, value)
				}
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
	if err := os.MkdirAll(areaDirectory, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: configuration migration skipped: %v\n", err)
		return
	}
	if err := os.Chmod(areaDirectory, 0o700); err != nil {
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
	if cfg.Server.CSRF.TokenLength < 16 || cfg.Server.CSRF.TokenLength > 128 {
		warnInvalid("server.csrf.token_length")
		cfg.Server.CSRF.TokenLength = defaults.Server.CSRF.TokenLength
	}
	if cfg.Server.CSRF.CookieName == "" || strings.ContainsAny(cfg.Server.CSRF.CookieName, " ;,=\r\n\t") {
		warnInvalid("server.csrf.cookie_name")
		cfg.Server.CSRF.CookieName = defaults.Server.CSRF.CookieName
	}
	if cfg.Server.CSRF.HeaderName == "" || strings.ContainsAny(cfg.Server.CSRF.HeaderName, " :\r\n\t") {
		warnInvalid("server.csrf.header_name")
		cfg.Server.CSRF.HeaderName = defaults.Server.CSRF.HeaderName
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Server.CSRF.Secure)) {
	case "auto", "true", "false":
		cfg.Server.CSRF.Secure = strings.ToLower(strings.TrimSpace(cfg.Server.CSRF.Secure))
	default:
		warnInvalid("server.csrf.secure")
		cfg.Server.CSRF.Secure = defaults.Server.CSRF.Secure
	}
	if !validAPIVersion(cfg.Server.APIVersion) {
		warnInvalid("server.api_version")
		cfg.Server.APIVersion = defaults.Server.APIVersion
	}
	if !validPort(cfg.Server.Port) {
		warnInvalid("server.port")
		cfg.Server.Port = defaults.Server.Port
	}
	if cfg.Server.SSL.MinVersion == "" {
		cfg.Server.SSL.MinVersion = defaults.Server.SSL.MinVersion
	}
	if cfg.Server.SSL.MinVersion != "TLS1.2" && cfg.Server.SSL.MinVersion != "TLS1.3" {
		warnInvalid("server.ssl.min_version")
		cfg.Server.SSL.MinVersion = defaults.Server.SSL.MinVersion
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Server.SSL.LetsEncrypt.Challenge)) {
	case "", "http-01", "tls-alpn-01", "dns-01":
	default:
		warnInvalid("server.ssl.letsencrypt.challenge")
		cfg.Server.SSL.LetsEncrypt.Challenge = defaults.Server.SSL.LetsEncrypt.Challenge
	}
	if prefix, err := NormalizeBaseURL(cfg.Server.BaseURL); err != nil {
		warnInvalid("server.baseurl")
		cfg.Server.BaseURL = defaults.Server.BaseURL
	} else {
		cfg.Server.BaseURL = prefix
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
	if err := ValidateTracking(&cfg.Server.Tracking); err != nil {
		return fmt.Errorf("invalid server.tracking configuration: %w", err)
	}
	validatePrivacyRetention(&cfg.Server.Privacy.Retention, defaults.Server.Privacy.Retention)
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
	validateSecurityEntries(&cfg.Server.Security)
	validateGeoIP(&cfg.Server.GeoIP)
	validateBlocklistSources(&cfg.Server.Security.Blocklists)
	catchUpWindow, catchUpErr := time.ParseDuration(cfg.Server.Scheduler.CatchUpWindow)
	if catchUpErr != nil || catchUpWindow <= 0 {
		warnInvalid("server.scheduler.catch_up_window")
		cfg.Server.Scheduler.CatchUpWindow = defaults.Server.Scheduler.CatchUpWindow
	}
	if cfg.Server.Scheduler.Tasks == nil {
		cfg.Server.Scheduler.Tasks = make(map[string]ScheduledTaskConfig)
	}
	for _, taskID := range []string{"ssl_renewal", "security_cleanup", "keyserver_retry", "token_cleanup", "log_rotation", "healthcheck_self", "database_check", "health_stats_flush"} {
		task, exists := cfg.Server.Scheduler.Tasks[taskID]
		if !exists {
			task = defaults.Server.Scheduler.Tasks[taskID]
		}
		if !task.Enabled {
			warnInvalid("server.scheduler.tasks." + taskID + ".enabled")
			task.Enabled = true
		}
		if task.Schedule == "" {
			task.Schedule = defaults.Server.Scheduler.Tasks[taskID].Schedule
		}
		cfg.Server.Scheduler.Tasks[taskID] = task
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
		warnInvalid("server.notifications.email.smtp.port")
		cfg.Server.Notifications.Email.SMTP.Port = defaults.Server.Notifications.Email.SMTP.Port
	}
	if cfg.Server.Notifications.WebUI.Position == "" {
		cfg.Server.Notifications.WebUI.Position = defaults.Server.Notifications.WebUI.Position
	}
	switch cfg.Server.Notifications.WebUI.Position {
	case "top-right", "top-left", "bottom-right", "bottom-left":
	default:
		warnInvalid("server.notifications.webui.position")
		cfg.Server.Notifications.WebUI.Position = defaults.Server.Notifications.WebUI.Position
	}
	if cfg.Server.Notifications.WebUI.Duration < 0 {
		warnInvalid("server.notifications.webui.duration")
		cfg.Server.Notifications.WebUI.Duration = defaults.Server.Notifications.WebUI.Duration
	}
	if cfg.Server.Notifications.WebUI.WarningDuration < 0 {
		warnInvalid("server.notifications.webui.warning_duration")
		cfg.Server.Notifications.WebUI.WarningDuration = defaults.Server.Notifications.WebUI.WarningDuration
	}
	if replyTo := strings.TrimSpace(cfg.Server.Notifications.Email.ReplyTo); replyTo != "" {
		if strings.ContainsAny(replyTo, "\r\n") {
			warnInvalid("server.notifications.email.reply_to")
			cfg.Server.Notifications.Email.ReplyTo = ""
		} else if _, err := mail.ParseAddress(replyTo); err != nil {
			warnInvalid("server.notifications.email.reply_to")
			cfg.Server.Notifications.Email.ReplyTo = ""
		} else {
			cfg.Server.Notifications.Email.ReplyTo = replyTo
		}
	}
	if !i18n.IsSupported(cfg.Server.I18N.DefaultLanguage) {
		warnInvalid("server.i18n.default_language")
		cfg.Server.I18N.DefaultLanguage = defaults.Server.I18N.DefaultLanguage
	}
	if len(cfg.Server.I18N.AvailableLanguages) == 0 {
		cfg.Server.I18N.AvailableLanguages = append([]string(nil), defaults.Server.I18N.AvailableLanguages...)
	}
	availableLanguages := make([]string, 0, len(cfg.Server.I18N.AvailableLanguages))
	seenLanguages := make(map[string]struct{}, len(cfg.Server.I18N.AvailableLanguages))
	for _, code := range cfg.Server.I18N.AvailableLanguages {
		code = strings.ToLower(strings.TrimSpace(code))
		if !i18n.IsSupported(code) {
			warnInvalid("server.i18n.available_languages")
			continue
		}
		if _, duplicate := seenLanguages[code]; duplicate {
			continue
		}
		seenLanguages[code] = struct{}{}
		availableLanguages = append(availableLanguages, code)
	}
	if len(availableLanguages) == 0 {
		availableLanguages = append([]string(nil), defaults.Server.I18N.AvailableLanguages...)
	}
	cfg.Server.I18N.AvailableLanguages = availableLanguages
	if _, ok := seenLanguages[cfg.Server.I18N.DefaultLanguage]; !ok {
		cfg.Server.I18N.DefaultLanguage = "en"
	}
	if cfg.Server.Notifications.Email.Events == nil {
		cfg.Server.Notifications.Email.Events = make(map[string]bool)
	}
	for event, enabled := range defaults.Server.Notifications.Email.Events {
		if _, exists := cfg.Server.Notifications.Email.Events[event]; !exists {
			cfg.Server.Notifications.Email.Events[event] = enabled
		}
	}
	switch cfg.Server.Notifications.Email.SMTP.TLS {
	case "", "auto", "starttls", "tls", "none":
	default:
		warnInvalid("server.notifications.email.smtp.tls")
		cfg.Server.Notifications.Email.SMTP.TLS = defaults.Server.Notifications.Email.SMTP.TLS
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
	return validateConnections(cfg)
}

func validateSecurityEntries(security *SecurityConfig) {
	allowlist := make([]AllowlistEntry, 0, len(security.Allowlist))
	for index, entry := range security.Allowlist {
		prefix, ok := normalizeIPPrefix(entry.CIDR)
		if !ok || (prefix.Addr().Is4() && prefix.Bits() < 8) || (prefix.Addr().Is6() && prefix.Bits() < 16) {
			warnInvalidEntry("server.security.allowlist", index, "cidr")
			continue
		}
		entry.CIDR = prefix.String()
		allowlist = append(allowlist, entry)
	}
	security.Allowlist = allowlist
	blocked := make([]BlockedIPEntry, 0, len(security.BlockedIPs))
	for index, entry := range security.BlockedIPs {
		prefix, ok := normalizeIPPrefix(entry.CIDR)
		if !ok {
			warnInvalidEntry("server.security.blocked_ips", index, "cidr")
			continue
		}
		entry.CIDR = prefix.String()
		if strings.TrimSpace(entry.Description) == "" {
			entry.Description = "(no description)"
		}
		blocked = append(blocked, entry)
	}
	security.BlockedIPs = blocked
}

func normalizeIPPrefix(value string) (netip.Prefix, bool) {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), true
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, false
	}
	address = address.Unmap()
	return netip.PrefixFrom(address, address.BitLen()), true
}

func validateBlocklistSources(blocklists *BlocklistsConfig) {
	valid := make([]BlocklistSourceConfig, 0, len(blocklists.ExtraSources))
	seen := map[string]struct{}{"firehol_level1": {}}
	for index, source := range blocklists.ExtraSources {
		if !source.enabledSet {
			source.Enabled = true
		}
		field := "id"
		_, duplicate := seen[source.ID]
		ok := validBlocklistID(source.ID) && !duplicate
		if ok {
			field = "type"
			ok = source.Type == "ip" || source.Type == "domain"
		}
		if ok {
			field = "url"
			parsed, err := url.Parse(strings.TrimSpace(source.URL))
			ok = err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
		}
		if !ok {
			warnInvalidEntry("server.security.blocklists.extra_sources", index, field)
			continue
		}
		seen[source.ID] = struct{}{}
		valid = append(valid, source)
	}
	blocklists.ExtraSources = valid
}

func validBlocklistID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				if character != '_' {
					return false
				}
			}
		}
	}
	return value != "firehol_level1"
}

func validateGeoIP(geoIP *GeoIPConfig) {
	geoIP.DenyCountries = validCountryCodes(geoIP.DenyCountries, "server.geoip.deny_countries")
	geoIP.AllowCountries = validCountryCodes(geoIP.AllowCountries, "server.geoip.allow_countries")
	if geoIP.Presets == nil {
		geoIP.Presets = map[string][]string{}
	}
	for name, codes := range geoIP.Presets {
		validName := name != "" && len(name) <= 64
		for _, character := range name {
			switch {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '_' || character == '-':
			default:
				validName = false
			}
		}
		if !validName {
			warnInvalid("server.geoip.presets")
			delete(geoIP.Presets, name)
			continue
		}
		geoIP.Presets[name] = validCountryCodes(codes, "server.geoip.presets."+name)
	}
}

func validCountryCodes(values []string, key string) []string {
	valid := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		code := strings.ToUpper(strings.TrimSpace(value))
		region, err := language.ParseRegion(code)
		if len(code) != 2 || err != nil || !region.IsCountry() || region.ISO3() == "ZZZ" {
			warnInvalidEntry(key, index, "country_code")
			continue
		}
		if _, ok := seen[code]; !ok {
			valid = append(valid, code)
			seen[code] = struct{}{}
		}
	}
	return valid
}

func warnInvalidEntry(key string, index int, field string) {
	fmt.Fprintf(os.Stderr, "warning: invalid configuration entry %s[%d].%s; skipping\n", key, index, field)
}

func validAPIVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// NormalizeBaseURL validates and canonicalizes the configured URL path prefix.
func NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "", nil
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#\\") {
		return "", fmt.Errorf("base URL must be a same-origin absolute path")
	}
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._~!$&'()*+,;=:@%/-"
	for _, character := range value {
		if !strings.ContainsRune(allowed, character) {
			return "", fmt.Errorf("base URL contains an invalid character")
		}
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("base URL must be a same-origin absolute path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("base URL cannot contain dot segments")
		}
	}
	return strings.TrimRight(value, "/"), nil
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
			warnInvalid("SMTP_PORT")
			smtp.Port = 587
		} else {
			smtp.Port = port
		}
	}
	return nil
}

func validatePrivacyRetention(retention *PrivacyRetentionConfig, defaults PrivacyRetentionConfig) {
	switch value := retention.Period.(type) {
	case string:
	case map[string]any:
		for key, text := range value {
			if key != "default" && !i18n.IsSupported(key) {
				warnInvalid("server.privacy.retention.period")
				retention.Period = defaults.Period
				return
			}
			if _, ok := text.(string); !ok {
				warnInvalid("server.privacy.retention.period")
				retention.Period = defaults.Period
				return
			}
		}
	default:
		warnInvalid("server.privacy.retention.period")
		retention.Period = defaults.Period
	}
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
