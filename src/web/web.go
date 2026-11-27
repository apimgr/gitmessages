package web

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"expvar"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/gitmessages/src/cache"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	graphqlapi "github.com/apimgr/gitmessages/src/graphql"
	"github.com/apimgr/gitmessages/src/maintenance"
	"github.com/apimgr/gitmessages/src/messages"
	"github.com/apimgr/gitmessages/src/metrics"
	"github.com/apimgr/gitmessages/src/mode"
	"github.com/apimgr/gitmessages/src/overlay"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/scheduler"
	swaggerapi "github.com/apimgr/gitmessages/src/swagger"
	htmlnode "golang.org/x/net/html"
	languagetags "golang.org/x/text/language"
)

// APIBasePath returns the configured canonical REST API prefix.
func APIBasePath() string {
	apiBasePathMu.RLock()
	defer apiBasePathMu.RUnlock()
	return apiBasePath
}

var (
	serverVersion           = "dev"
	serverCommit            = "unknown"
	serverBuildDate         = ""
	serverStartedAt         = time.Now().UTC()
	panicLoggerMu           sync.RWMutex
	panicLogger             *log.Logger
	overlayStatusMu         sync.RWMutex
	overlayStatusProvider   func() map[string]any
	apiBasePathMu           sync.RWMutex
	apiBasePath             = "/api/" + config.DefaultAPIVersion
	corsOriginsMu           sync.RWMutex
	corsOrigins             = []string{"*"}
	defaultThemeMu          sync.RWMutex
	defaultTheme            = "dark"
	metricsExporterMu       sync.RWMutex
	activeMetricsExporter   *metrics.Exporter
	maintenanceMu           sync.RWMutex
	maintenanceProvider     func() (maintenance.Info, bool)
	maintenanceReadProvider func(context.Context) bool
	healthProviderMu        sync.RWMutex
	healthStatsProvider     func() HealthStats
	healthRestartProvider   func() []string
	healthShutdownProvider  func() bool
	activeRequests          atomic.Int64
	requestCounterMu        sync.Mutex
	pendingRequestTotal     int64
	pendingRequestHours     = make(map[int64]int64)
	rateLimitCounter        cache.Counter
	rateLimitCounterMu      sync.RWMutex
	copyCodeSequence        atomic.Uint64
	debugRequestCount       = expvar.NewInt("requests_total")
	debugRequestSeconds     = expvar.NewFloat("requests_duration_seconds")
	debugErrorCount         = expvar.NewInt("errors_total")
)

//go:embed assets/*.css
var webAssets embed.FS

//go:embed static
var documentationAssets embed.FS

// Handler returns the complete public web and API surface.
func Handler(version string, commit ...string) http.Handler {
	apiVersion := strings.TrimSpace(config.Get().Server.APIVersion)
	if apiVersion == "" {
		apiVersion = config.DefaultAPIVersion
	}
	apiBasePathMu.Lock()
	apiBasePath = "/api/" + apiVersion
	apiBasePathMu.Unlock()
	if version != "" {
		serverVersion = version
	}
	serverCommit = "unknown"
	serverBuildDate = ""
	if len(commit) > 0 && commit[0] != "" {
		serverCommit = commit[0]
	}
	if len(commit) > 1 && commit[1] != "" && commit[1] != "N/A" {
		serverBuildDate = commit[1]
	}
	mux := newRouteMux()
	mux.HandleFunc("/robots.txt", robotsTxt)
	mux.HandleFunc("/sitemap.xml", sitemapXML)
	mux.HandleFunc("/feed.xml", atomFeedXML)
	mux.HandleFunc("/rss.xml", rssFeedXML)
	registerTokenRoutes(mux)
	metricsConfig := config.Get().Server.Metrics
	var exporter *metrics.Exporter
	if metricsConfig.Enabled {
		buildDate := "N/A"
		if len(commit) > 1 && commit[1] != "" {
			buildDate = commit[1]
		}
		exporter = metrics.New(metricsConfig, serverVersion, serverCommit, buildDate)
	}
	metricsExporterMu.Lock()
	activeMetricsExporter = exporter
	metricsExporterMu.Unlock()
	mux.HandleFunc("/", home)
	mux.HandleFunc("/types", frontendPage(typesPage, apiTypes, "/types"))
	mux.HandleFunc("/templates", frontendPage(templatesPage, apiTemplates, "/templates"))
	mux.HandleFunc("/builder", frontendPage(builderPage, apiMessageForm, "/messages"))
	mux.HandleFunc("/server", serverRootRedirect)
	mux.HandleFunc("/server/tracking.js", trackingScriptPage)
	mux.HandleFunc("/server/about", frontendPage(aboutPage, apiAbout, "/server/about"))
	mux.HandleFunc("/server/help", frontendPage(helpPage, apiHelp, "/server/help"))
	mux.HandleFunc("/server/help/mcp", frontendPage(mcpHelpPage, apiMCPHelp, "/server/help/mcp"))
	mux.HandleFunc("/server/privacy", frontendPage(privacyPage, apiPrivacy, "/server/privacy"))
	mux.HandleFunc("/server/terms", frontendPage(termsPage, apiTerms, "/server/terms"))
	mux.HandleFunc("/server/preferences", frontendPage(preferencesPage, apiPreferences, "/server/preferences"))
	mux.HandleFunc("/server/ccpa", ccpaOptOutPage)
	mux.HandleFunc("/server/consent", consentPage)
	registerPreferenceSyncRoutes(mux)
	mux.HandleFunc("/server/healthz", frontendPage(healthPage, apiHealth, "/server/healthz"))
	if config.Get().Server.Healthz.Root.Enabled {
		mux.HandleFunc("/healthz", frontendPage(healthPage, apiHealth, "/server/healthz"))
	}
	mux.HandleFunc("/manifest.json", manifest)
	mux.HandleFunc("/offline.html", offlinePage)
	mux.HandleFunc("/icon.svg", icon)
	mux.HandleFunc("/.well-known/branding/", brandingImage)
	mux.HandleFunc("/static/icons/", pwaIcon)
	mux.HandleFunc("/static/splash/", pwaSplash)
	mux.HandleFunc("/sw.js", serviceWorker)
	mux.HandleFunc("/styles.css", styles)
	mux.HandleFunc("/static/css/", stylesheetAsset)
	mux.HandleFunc("/static/vendor/swagger-ui/", swaggerStaticAsset)
	mux.HandleFunc("/static/vendor/graphiql/", graphQLStaticAsset)
	mux.HandleFunc("/static/js/", documentationScript)
	mux.HandleFunc("/locales/", localeJSON)
	mux.HandleFunc("/cli/binaries/", serveCLIBinary)
	mux.HandleFunc("/api/healthz.txt", apiTextExtension)
	mux.HandleFunc(APIBasePath()+".txt", apiTextExtension)
	mux.HandleFunc(APIBasePath()+"/", apiTextExtension)
	registerAPIRoutes(mux)
	registerContactRoutes(mux)
	mux.HandleFunc("/server/docs/swagger", swaggerUI)
	mux.HandleFunc("/server/docs/graphql", graphQLUI)
	mux.HandleDebugOnly("/debug/", debugHandler(mux.routeList), mode.IsDebugEnabled())
	handler := language(mux)
	handler = geoIPMiddleware(handler)
	handler = rateLimit(handler)
	handler = blocklistMiddleware(handler)
	handler = allowlistMiddleware(handler)
	handler = secureHeaders(cors(handler))
	handler = withConfiguredBaseURL(normalizeURL(handler))
	handler = withBuildStamp(handler)
	if exporter != nil {
		handler = exporter.Middleware(handler)
	}
	handler = debugMiddleware(handler)
	handler = privacySignalMiddleware(handler)
	handler = maintenanceMiddleware(handler)
	return requestStatsMiddleware(RecoverMiddleware(handler))
}

// ConfigurePanicLogger directs private panic diagnostics to the configured log file.
func ConfigurePanicLogger(logger *log.Logger) {
	panicLoggerMu.Lock()
	panicLogger = logger
	panicLoggerMu.Unlock()
}

// SetMaintenanceProvider connects maintenance state to health output and write gating.
func SetMaintenanceProvider(provider func() (maintenance.Info, bool)) {
	maintenanceMu.Lock()
	maintenanceProvider = provider
	maintenanceMu.Unlock()
}

// SetMaintenanceReadProvider reports whether the database or an allowed cache can answer reads.
func SetMaintenanceReadProvider(provider func(context.Context) bool) {
	maintenanceMu.Lock()
	maintenanceReadProvider = provider
	maintenanceMu.Unlock()
}

func canServeMaintenanceReads(ctx context.Context) (bool, bool) {
	maintenanceMu.RLock()
	provider := maintenanceReadProvider
	maintenanceMu.RUnlock()
	if provider == nil {
		return true, false
	}
	return provider(ctx), true
}

func currentMaintenance() (maintenance.Info, bool) {
	maintenanceMu.RLock()
	provider := maintenanceProvider
	maintenanceMu.RUnlock()
	if provider == nil {
		return maintenance.Info{}, false
	}
	return provider()
}

// SetHealthStatsProvider connects persistent request counters to health output.
func SetHealthStatsProvider(provider func() HealthStats) {
	healthProviderMu.Lock()
	healthStatsProvider = provider
	healthProviderMu.Unlock()
}

// SetHealthRestartProvider connects pending restart reasons to health output.
func SetHealthRestartProvider(provider func() []string) {
	healthProviderMu.Lock()
	healthRestartProvider = provider
	healthProviderMu.Unlock()
}

// SetHealthShutdownProvider reports graceful shutdown state to health output.
func SetHealthShutdownProvider(provider func() bool) {
	healthProviderMu.Lock()
	healthShutdownProvider = provider
	healthProviderMu.Unlock()
}

func currentHealthStats() HealthStats {
	healthProviderMu.RLock()
	provider := healthStatsProvider
	healthProviderMu.RUnlock()
	if provider == nil {
		return HealthStats{}
	}
	return provider()
}

func currentHealthRestartReasons() []string {
	healthProviderMu.RLock()
	provider := healthRestartProvider
	healthProviderMu.RUnlock()
	if provider == nil {
		return nil
	}
	return append([]string(nil), provider()...)
}

func currentHealthShuttingDown() bool {
	healthProviderMu.RLock()
	provider := healthShutdownProvider
	healthProviderMu.RUnlock()
	return provider != nil && provider()
}

// ConfigureDatabaseMetrics connects the active metrics exporter to the shared SQLite store.
func ConfigureDatabaseMetrics(store *database.Store) {
	metricsExporterMu.RLock()
	exporter := activeMetricsExporter
	metricsExporterMu.RUnlock()
	if store == nil {
		return
	}
	store.SetQueryObserver(func(observation database.QueryObservation) {
		if exporter != nil {
			exporter.ObserveDatabaseQuery(observation)
		}
		if mode.IsDebugEnabled() && config.Get().Server.Debug.LogQueries {
			slog.Debug("db query",
				"operation", observation.Operation,
				"table", observation.Table,
				"duration_ms", observation.Duration.Milliseconds(),
				"error_type", observation.ErrorType,
				"args", "[REDACTED]",
			)
		}
	})
	if exporter != nil {
		exporter.SetDatabaseStatsProvider(func() (int, int) {
			stats := store.Read().Stats()
			return stats.OpenConnections, stats.InUse
		})
	}
}

func ObserveDatabaseBusy(wait time.Duration) {
	metricsExporterMu.RLock()
	exporter := activeMetricsExporter
	metricsExporterMu.RUnlock()
	if exporter != nil {
		exporter.ObserveDatabaseBusy(wait)
	}
}

// ConfigureRateLimitCache installs the optional shared counter backend.
func ConfigureRateLimitCache(counter cache.Counter) {
	rateLimitCounterMu.Lock()
	rateLimitCounter = counter
	rateLimitCounterMu.Unlock()
}

// ConfigureSchedulerMetrics connects task execution events to the active exporter.
func ConfigureSchedulerMetrics(backgroundScheduler *scheduler.Scheduler) {
	setDebugScheduler(backgroundScheduler)
	metricsExporterMu.RLock()
	exporter := activeMetricsExporter
	metricsExporterMu.RUnlock()
	if exporter != nil && backgroundScheduler != nil {
		backgroundScheduler.AddObserver(exporter.ObserveScheduler)
	}
}

func recordMessageBuild() {
	metricsExporterMu.RLock()
	exporter := activeMetricsExporter
	metricsExporterMu.RUnlock()
	if exporter != nil {
		exporter.RecordMessageBuild()
	}
}

func withConfiguredBaseURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := config.Get().Server.BaseURL
		if prefix == "" || prefix == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.NotFound(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL = new(url.URL)
		*clone.URL = *r.URL
		clone.URL.Path = strings.TrimPrefix(clone.URL.Path, prefix)
		if clone.URL.Path == "" {
			clone.URL.Path = "/"
		}
		if clone.URL.RawPath != "" {
			clone.URL.RawPath = strings.TrimPrefix(clone.URL.RawPath, prefix)
		}
		next.ServeHTTP(w, clone)
	})
}

func withBuildStamp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Build", assetStamp())
		next.ServeHTTP(w, r)
	})
}

func normalizeURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		segment := path
		if strings.HasSuffix(segment, "/") {
			segment = strings.TrimSuffix(segment, "/")
			segment = segment[strings.LastIndex(segment, "/")+1:]
		}
		if path != "/" && strings.HasSuffix(path, "/") && !strings.Contains(segment, ".") {
			canonical := strings.TrimSuffix(path, "/")
			if prefix := validURLPrefix(config.Get().Server.BaseURL); prefix != "" {
				canonical = prefix + canonical
			}
			if r.URL.RawQuery != "" {
				canonical += "?" + r.URL.RawQuery
			}
			status := http.StatusMovedPermanently
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				status = http.StatusPermanentRedirect
			}
			http.Redirect(w, r, canonical, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SetOverlayStatus(provider func() map[string]any) {
	overlayStatusMu.Lock()
	defer overlayStatusMu.Unlock()
	overlayStatusProvider = provider
}

func overlayStatuses() map[string]any {
	overlayStatusMu.RLock()
	provider := overlayStatusProvider
	overlayStatusMu.RUnlock()
	if provider == nil {
		return nil
	}
	return provider()
}

func SetCORSOrigins(origins []string) {
	corsOriginsMu.Lock()
	defer corsOriginsMu.Unlock()
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	corsOrigins = append([]string(nil), origins...)
}

func SetDefaultTheme(theme string) {
	if theme != "auto" && theme != "light" && theme != "dark" {
		theme = "dark"
	}
	defaultThemeMu.Lock()
	defaultTheme = theme
	defaultThemeMu.Unlock()
}

func language(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := ""
		if query := r.URL.Query().Get("lang"); query != "" && i18n.IsSupported(query) {
			lang = query
			if privacySignalFromRequest(r) == "" && preferenceCategoryAllowed(r) {
				http.SetCookie(w, &http.Cookie{Name: "lang", Value: lang, Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
			}
		}
		if lang == "" && privacySignalFromRequest(r) == "" {
			if cookie, err := r.Cookie("lang"); err == nil && i18n.IsSupported(cookie.Value) {
				lang = cookie.Value
			}
		}
		if lang == "" {
			lang = acceptLanguage(r.Header.Get("Accept-Language"))
		}
		if lang == "" {
			configured := strings.TrimSpace(os.Getenv("GITMESSAGES_LANG"))
			if i18n.IsSupported(configured) {
				lang = configured
			}
		}
		if lang == "" {
			lang = "en"
		}
		r.Header.Set("X-GitMessages-Language", lang)
		w.Header().Set("Content-Language", lang)
		w.Header().Add("Vary", "Accept-Language")
		next.ServeHTTP(w, r)
	})
}

func acceptLanguage(header string) string {
	codes := i18n.Supported()
	tags := make([]languagetags.Tag, 0, len(codes))
	for _, code := range codes {
		tags = append(tags, languagetags.MustParse(code))
	}
	accepted, _, err := languagetags.ParseAcceptLanguage(header)
	if err != nil || len(accepted) == 0 {
		return ""
	}
	_, index, _ := languagetags.NewMatcher(tags).Match(accepted...)
	if index < 0 || index >= len(codes) {
		return ""
	}
	return codes[index]
}

func languageCode(r *http.Request) string { return r.Header.Get("X-GitMessages-Language") }

func tr(r *http.Request, key string) string { return i18n.T(languageCode(r), key) }

func assetStamp() string {
	return strings.TrimSpace(serverVersion) + "-" + strings.TrimSpace(serverCommit)
}

func setVersionedAssetHeaders(w http.ResponseWriter, r *http.Request, versioned bool) bool {
	return setAssetHeaders(w, r, assetStamp(), versioned)
}

func setAssetHeaders(w http.ResponseWriter, r *http.Request, stamp string, versioned bool) bool {
	etag := `"` + stamp + `"`
	w.Header().Set("ETag", etag)
	if versioned && r.URL.Query().Get("v") == stamp {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return false
	}
	return true
}

func localizedRandomTemplate(lang string) messages.Template {
	items := messages.TemplatesForLocale(lang)
	return items[rand.IntN(len(items))]
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corsOriginsMu.RLock()
		origins := append([]string(nil), corsOrigins...)
		corsOriginsMu.RUnlock()
		if len(origins) == 1 && origins[0] == "" {
			next.ServeHTTP(w, r)
			return
		}
		for _, domain := range domainList(os.Getenv("DOMAIN"), "") {
			origins = append(origins, "https://"+domain)
		}
		for _, host := range learnedURLHosts() {
			origins = append(origins, "https://"+host)
		}
		if len(origins) == 0 {
			origins = []string{"*"}
		}
		origin := r.Header.Get("Origin")
		allowed := false
		for _, candidate := range origins {
			if candidate == "*" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				allowed = true
				break
			}
			if origin != "" && origin == candidate {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				allowed = true
				break
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seoRobotsDirective(r.URL.Path) == "noindex,nofollow" {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Origin-Agent-Cluster", "?1")
		w.Header().Set("Cross-Origin-Opener-Policy", "unsafe-none")
		w.Header().Set("Cross-Origin-Embedder-Policy", "unsafe-none")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		scriptSources := "'self'"
		frameSources := "'self'"
		connectSources := "'self'"
		imageSources := "'self'"
		provider := contactCaptchaProvider(r)
		switch provider {
		case "recaptcha":
			scriptSources += " https://www.google.com/recaptcha/ https://www.gstatic.com/recaptcha/"
			frameSources += " https://www.google.com/recaptcha/ https://recaptcha.google.com/recaptcha/"
			connectSources += " https://www.google.com/recaptcha/ https://www.gstatic.com/recaptcha/"
		case "hcaptcha":
			scriptSources += " https://js.hcaptcha.com https://hcaptcha.com https://*.hcaptcha.com"
			frameSources += " https://hcaptcha.com https://*.hcaptcha.com"
			connectSources += " https://hcaptcha.com https://*.hcaptcha.com"
		}
		trackingScript, trackingConnect, trackingImage := trackingCSPHosts(r)
		for _, source := range strings.Fields(trackingScript) {
			scriptSources += " " + source
		}
		for _, source := range strings.Fields(trackingConnect) {
			connectSources += " " + source
		}
		for _, source := range strings.Fields(trackingImage) {
			imageSources += " " + source
		}
		csp := "default-src 'self'; style-src 'self'; font-src 'self' data: https:; script-src " + scriptSources + "; frame-src " + frameSources + "; connect-src " + connectSources + "; img-src " + imageSources + "; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"
		if requestIsSecure(r) {
			csp += "; upgrade-insecure-requests"
		}
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), usb=(), payment=(), accelerometer=(), gyroscope=(), magnetometer=(), midi=(), display-capture=()")
		if requestIsSecure(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "unavailable"
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

type clientWindow struct {
	started time.Time
	count   int
}

var limits = struct {
	sync.Mutex
	clients map[string]clientWindow
}{clients: make(map[string]clientWindow)}

func rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIsAllowlisted(r) {
			next.ServeHTTP(w, r)
			return
		}
		ip := requestClientIP(r)
		rateLimitCounterMu.RLock()
		counter := rateLimitCounter
		rateLimitCounterMu.RUnlock()
		if counter != nil {
			started := time.Now()
			count, err := counter.Increment(r.Context(), "rate-limit:"+ip, time.Minute)
			if mode.IsDebugEnabled() && config.Get().Server.Debug.LogCache {
				slog.Debug("cache",
					"operation", "increment",
					"namespace", "rate-limit",
					"hit", count > 1 && err == nil,
					"duration_us", time.Since(started).Microseconds(),
					"error", err != nil,
				)
			}
			if err == nil {
				metricsExporterMu.RLock()
				exporter := activeMetricsExporter
				metricsExporterMu.RUnlock()
				if exporter != nil {
					exporter.ObserveCacheOperation("rate_limit", count > 1, counter.Stats())
				}
				if count > 120 {
					writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", tr(r, "api.rate_limited"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
		}
		now := time.Now()
		limits.Lock()
		window, exists := limits.clients[ip]
		if !exists && len(limits.clients) >= 4096 {
			for key, current := range limits.clients {
				if now.Sub(current.started) >= time.Minute {
					delete(limits.clients, key)
				}
			}
		}
		if !exists && len(limits.clients) >= 4096 {
			limits.Unlock()
			writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", tr(r, "api.rate_limited"))
			return
		}
		if !exists || now.Sub(window.started) >= time.Minute {
			window = clientWindow{started: now}
		}
		window.count++
		limits.clients[ip] = window
		limits.Unlock()
		if window.count > 120 {
			writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", tr(r, "api.rate_limited"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	if wantsText(r) {
		if envelope, ok := value.(map[string]any); ok {
			if success, _ := envelope["ok"].(bool); success {
				if data, exists := envelope["data"]; exists {
					writeText(w, status, serializeRouteText(r, data))
					return
				}
			} else if code, exists := envelope["error"].(string); exists {
				message, _ := envelope["message"].(string)
				writeText(w, status, "ERROR: "+code+": "+message)
				return
			}
		}
		if routeForRequest(r) != nil {
			writeText(w, status, serializeRouteText(r, value))
			return
		}
		output, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "ENCODE_FAILED", tr(r, "api.encode_failed"))
			return
		}
		writeText(w, status, string(output))
		return
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		writeError(w, r, http.StatusInternalServerError, "ENCODE_FAILED", tr(r, "api.encode_failed"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

func writeSuccess(w http.ResponseWriter, r *http.Request, data any) {
	w.Header().Set("Cache-Control", "public, max-age=60")
	if wantsText(r) {
		if routeForRequest(r) != nil {
			writeText(w, http.StatusOK, serializeRouteText(r, data))
		} else {
			writeText(w, http.StatusOK, plainData(data))
		}
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		recordRejectedCLIToken(r)
	}
	if wantsText(r) {
		writeText(w, status, "ERROR: "+code+": "+message)
		return
	}
	if htmlPageRequest(r) {
		writeFrontendError(w, r, status, message)
		return
	}
	writeJSON(w, r, status, map[string]any{"ok": false, "error": code, "message": message, "details": map[string]any{}, "request_id": w.Header().Get("X-Request-ID")})
}

func writeHTTPError(w http.ResponseWriter, r *http.Request, message string, status int) {
	if htmlPageRequest(r) {
		writeFrontendError(w, r, status, message)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, r, status, "HTTP_ERROR", message)
		return
	}
	http.Error(w, message, status)
}

func htmlPageRequest(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html") && !strings.HasPrefix(r.URL.Path, "/api/")
}

func writeFrontendError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status >= http.StatusInternalServerError {
		message = tr(r, "errors.internal")
	}
	pageResponse := &capturedPageResponse{header: make(http.Header)}
	page(pageResponse, r, strconv.Itoa(status), template.HTML(`<section class="section-card" aria-labelledby="error-message"><h2 id="error-message">`+template.HTMLEscapeString(message)+`</h2></section>`))
	for name, values := range pageResponse.header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(pageResponse.body.Bytes())
}

type capturedPageResponse struct {
	header http.Header
	body   bytes.Buffer
}

func (w *capturedPageResponse) Header() http.Header            { return w.header }
func (w *capturedPageResponse) Write(body []byte) (int, error) { return w.body.Write(body) }
func (w *capturedPageResponse) WriteHeader(int)                {}

func wantsText(r *http.Request) bool {
	if r.Header.Get("X-GitMessages-Text-Response") == "true" || strings.HasSuffix(r.URL.Path, ".txt") {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return false
	}
	if strings.Contains(accept, "text/plain") {
		return true
	}
	userAgent := strings.ToLower(r.UserAgent())
	return strings.Contains(userAgent, "curl/") || strings.Contains(userAgent, "wget/") || strings.Contains(userAgent, "httpie/")
}

func wantsFrontendText(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/html") {
		return false
	}
	return strings.Contains(accept, "text/plain") || isTextClient(r.UserAgent())
}

func frontendPage(page http.HandlerFunc, api http.HandlerFunc, apiPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept")
		if r.Method == http.MethodGet && acceptsJSON(r.Header.Get("Accept")) {
			request := r.Clone(r.Context())
			request.URL = new(url.URL)
			*request.URL = *r.URL
			request.URL.Path = APIBasePath() + apiPath
			api(w, request)
			return
		}
		page(w, r)
	}
}

func acceptsJSON(header string) bool {
	for _, mediaRange := range strings.Split(header, ",") {
		parts := strings.Split(mediaRange, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "application/json") {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if found && strings.EqualFold(strings.TrimSpace(key), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || parsed <= 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > 0 {
			return true
		}
	}
	return false
}

func isTextClient(userAgent string) bool {
	userAgent = strings.ToLower(userAgent)
	return strings.Contains(userAgent, "curl/") || strings.Contains(userAgent, "wget/") || strings.Contains(userAgent, "httpie/")
}

func formatHTMLText(source string) string {
	document, err := htmlnode.Parse(strings.NewReader(source))
	if err != nil {
		return html.UnescapeString(source)
	}
	var output strings.Builder
	var walk func(*htmlnode.Node)
	walk = func(node *htmlnode.Node) {
		if node.Type == htmlnode.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		block := node.Type == htmlnode.ElementNode && strings.Contains(" address article aside blockquote br dd div dl dt fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 header hr li main nav ol p pre section table tr ul ", " "+node.Data+" ")
		if block {
			output.WriteByte('\n')
		}
		if node.Type == htmlnode.TextNode {
			output.WriteString(strings.TrimSpace(node.Data))
			output.WriteByte(' ')
		}
		if node.Type == htmlnode.ElementNode && node.Data == "li" {
			output.WriteString("• ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			output.WriteByte('\n')
		}
	}
	walk(document)
	lines := strings.Split(output.String(), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if len(result) > 0 && result[len(result)-1] != "" {
				result = append(result, "")
			}
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func writeText(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, strings.TrimRight(value, "\n")+"\n")
}

func plainData(data any) string {
	switch value := data.(type) {
	case string:
		return value
	case []messages.CommitType:
		var output strings.Builder
		for _, item := range value {
			fmt.Fprintf(&output, "%s %s: %s\n%s\n\n", item.Emoji, item.Type, item.Description, item.Example)
		}
		return output.String()
	case []messages.Convention:
		var output strings.Builder
		for _, item := range value {
			fmt.Fprintf(&output, "%s\n%s\nFormat: %s\nExample: %s\n\n", item.Name, item.Description, item.Format, item.Example)
		}
		return output.String()
	case []messages.Template:
		var output strings.Builder
		for _, item := range value {
			fmt.Fprintf(&output, "%s (%s)\n%s\n\n", item.Name, item.Category, item.Message)
		}
		return output.String()
	case map[string]string:
		if message, ok := value["message"]; ok {
			return message
		}
		var output strings.Builder
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&output, "%s: %s\n", key, value[key])
		}
		return output.String()
	default:
		encoded, _ := json.MarshalIndent(data, "", "  ")
		return string(encoded)
	}
}

func routeForRequest(r *http.Request) *Route {
	if r == nil || r.URL == nil {
		return nil
	}
	for _, definition := range apiOperationDefinitions(r) {
		if routePathMatches(definition.Path, r.URL.Path) && (definition.Method == r.Method || r.Method == http.MethodHead && definition.Method == http.MethodGet) {
			return &definition
		}
	}
	return nil
}

func routePathMatches(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for index, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if pathParts[index] == "" {
				return false
			}
			continue
		}
		if part != pathParts[index] {
			return false
		}
	}
	return true
}

func serializeRouteText(r *http.Request, data any) string {
	definition := routeForRequest(r)
	if definition == nil {
		return plainData(data)
	}
	value := reflect.ValueOf(data)
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			value = reflect.Value{}
			break
		}
		value = value.Elem()
	}
	if definition.Primary != "" {
		if primary, ok := jsonField(value, definition.Primary); ok {
			return textScalar(primary)
		}
	}
	if !value.IsValid() || isEmptyCollection(value) {
		if value.IsValid() && (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) {
			return fmt.Sprintf("OK: %d items found", value.Len())
		}
		return "OK: " + routeTextMessage(*definition)
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		if value.Len() == 0 {
			return "OK: 0 items found"
		}
		first := dereference(value.Index(0))
		if isTextObject(first) {
			var objects []string
			for index := 0; index < value.Len(); index++ {
				var lines []string
				appendTextFields(&lines, "", value.Index(index))
				objects = append(objects, strings.Join(lines, "\n"))
			}
			return fmt.Sprintf("OK: %d items found\n%s", value.Len(), strings.Join(objects, "\n\n"))
		}
		var items []string
		for index := 0; index < value.Len(); index++ {
			items = append(items, textScalar(value.Index(index)))
		}
		return fmt.Sprintf("OK: %d items found\n%s", len(items), strings.Join(items, "\n"))
	}
	if isTextObject(value) {
		lines := []string{"OK: " + routeTextMessage(*definition)}
		appendTextFields(&lines, "", value)
		return strings.Join(lines, "\n")
	}
	return "OK: " + routeTextMessage(*definition) + "\n" + textScalar(value)
}

func routeTextMessage(route Route) string {
	if route.Message != "" {
		return route.Message
	}
	switch route.Method {
	case http.MethodGet:
		return "Retrieved successfully"
	case http.MethodPut, http.MethodPatch:
		return "Updated successfully"
	case http.MethodDelete:
		return "Deleted successfully"
	default:
		return "OK"
	}
}

func dereference(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func isEmptyCollection(value reflect.Value) bool {
	value = dereference(value)
	return value.IsValid() && (value.Kind() == reflect.Slice || value.Kind() == reflect.Array || value.Kind() == reflect.Map) && value.Len() == 0
}

func isTextObject(value reflect.Value) bool {
	value = dereference(value)
	return value.IsValid() && (value.Kind() == reflect.Struct || value.Kind() == reflect.Map)
}

func jsonField(value reflect.Value, name string) (reflect.Value, bool) {
	value = dereference(value)
	if !value.IsValid() {
		return reflect.Value{}, false
	}
	switch value.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(name)
		if key.Type().AssignableTo(value.Type().Key()) {
			field := value.MapIndex(key)
			return field, field.IsValid()
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonName == "" {
				jsonName = field.Name
			}
			if jsonName == name && field.PkgPath == "" {
				return value.Field(index), true
			}
		}
	}
	return reflect.Value{}, false
}

func appendTextFields(lines *[]string, prefix string, value reflect.Value) {
	value = dereference(value)
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" {
				continue
			}
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fieldValue := value.Field(index)
			if strings.Contains(options, "omitempty") && fieldValue.IsZero() {
				continue
			}
			appendTextField(lines, joinTextField(prefix, name), fieldValue)
		}
	case reflect.Map:
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
		for _, key := range keys {
			appendTextField(lines, joinTextField(prefix, fmt.Sprint(key.Interface())), value.MapIndex(key))
		}
	}
}

func appendTextField(lines *[]string, name string, value reflect.Value) {
	value = dereference(value)
	if !value.IsValid() {
		*lines = append(*lines, name+": null")
		return
	}
	if value.Kind() == reflect.Struct || value.Kind() == reflect.Map {
		appendTextFields(lines, name, value)
		return
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		items := make([]string, 0, value.Len())
		for index := 0; index < value.Len(); index++ {
			items = append(items, textScalar(value.Index(index)))
		}
		*lines = append(*lines, name+": "+strings.Join(items, ", "))
		return
	}
	*lines = append(*lines, name+": "+textScalar(value))
}

func joinTextField(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func textScalar(value reflect.Value) string {
	value = dereference(value)
	if !value.IsValid() {
		return "null"
	}
	if value.Kind() == reflect.String {
		return value.String()
	}
	return fmt.Sprint(value.Interface())
}

func apiTextExtension(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, ".txt") {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", tr(r, "errors.not_found"))
		return
	}
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = strings.TrimSuffix(urlCopy.Path, ".txt")
	clone.URL = &urlCopy
	clone.Header.Set("X-GitMessages-Text-Response", "true")
	if urlCopy.Path == "/api/healthz" {
		apiHealth(w, clone)
		return
	}
	pathMatched := false
	allowed := make([]string, 0, 2)
	for _, route := range apiOperationDefinitions(clone) {
		if !routePathMatches(route.Path, urlCopy.Path) {
			continue
		}
		pathMatched = true
		if route.Binary != "" {
			continue
		}
		allowed = append(allowed, route.Method)
		if route.Method == clone.Method || clone.Method == http.MethodHead && route.Method == http.MethodGet {
			route.Handler(w, clone)
			return
		}
	}
	if pathMatched && len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return
	}
	writeText(w, http.StatusNotFound, tr(r, "errors.not_found"))
}

func method(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method != allowed {
		w.Header().Set("Allow", allowed)
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return false
	}
	return true
}

func apiRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath() || !method(w, r, http.MethodGet) {
		if r.URL.Path != APIBasePath() {
			http.NotFound(w, r)
		}
		return
	}
	writeSuccess(w, r, map[string]any{"name": seoBrandingTitle() + " API", "version": serverVersion, "resources": []string{"types", "conventions", "templates", "messages"}, "server": []string{"healthz", "about", "help", "help/mcp", "privacy", "terms", "security", "preferences", "swagger", "mcp"}})
}

func apiAutodiscover(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	cfg := config.Get()
	urls := discoveryOrigins(r)
	data := map[string]any{
		"name": paths.ProjectName, "version": serverVersion,
		"api":  map[string]string{"current_version": strings.TrimPrefix(APIBasePath(), "/api/")},
		"urls": urls, "cli_versions": cliBinaryVersions(), "cli_min_version": cfg.Server.Update.CLIMinVersion,
	}
	statuses := overlayStatuses()
	if cfg.Server.Tor.Expose {
		if status, ok := statuses["tor"].(overlay.TorStatus); ok && status.Hostname != "" {
			data["onion"] = status.Hostname
		}
	}
	if cfg.Server.I2P.Expose {
		if status, ok := statuses["i2p"].(overlay.I2PStatus); ok {
			address := status.Address
			if address == "" {
				address = status.Hostname
			}
			if address != "" {
				data["i2p"] = address
			}
		}
	}
	writeSuccess(w, r, data)
}

func discoveryOrigins(r *http.Request) []string {
	urls := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(value string) {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return
		}
		origin := strings.ToLower(parsed.Scheme) + "://" + parsed.Host
		if _, exists := seen[origin]; exists {
			return
		}
		seen[origin] = struct{}{}
		urls = append(urls, origin)
	}
	if requestUsesTorAddress(r) {
		if requestURL, err := url.Parse(BuildURL(r, "")); err == nil {
			add(requestURL.Scheme + "://" + requestURL.Host)
		}
		return urls
	}
	if requestURL, err := url.Parse(BuildURL(r, "")); err == nil {
		add(requestURL.Scheme + "://" + requestURL.Host)
	}
	for _, domain := range strings.Split(os.Getenv("DOMAIN"), ",") {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		if !strings.Contains(domain, "://") {
			scheme := "https"
			if requestURL, err := url.Parse(BuildURL(r, "")); err == nil && requestURL.Scheme != "" {
				scheme = requestURL.Scheme
			}
			domain = scheme + "://" + domain
		}
		add(domain)
	}
	return urls
}

func apiTypes(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, messages.TypesForLocale(languageCode(r)))
}

func apiConventions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, messages.ConventionsForLocale(languageCode(r)))
}

func apiTemplates(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, messages.TemplatesForLocale(languageCode(r)))
}

func apiRandomTemplate(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, localizedRandomTemplate(languageCode(r)))
}

func apiMessages(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input messages.BuildInput
	if err := decoder.Decode(&input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", tr(r, "api.invalid_request"))
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", tr(r, "api.invalid_json"))
		return
	}
	result, err := messages.Build(input)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_MESSAGE", tr(r, "api.invalid_message"))
		return
	}
	recordMessageBuild()
	writeSuccess(w, r, map[string]string{"message": result})
}

func apiMessageForm(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]any{"fields": buildMessageInputSchema(r)})
}

func apiRandom(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	catalog := messages.TypesForLocale(languageCode(r))
	item := catalog[rand.IntN(len(catalog))]
	writeSuccess(w, r, map[string]string{"type": item.Type, "message": item.Example})
}

func apiHealth(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeHealthResponse(w, r)
}

func apiAbout(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	name := seoBrandingTitle()
	tagline, description := aboutCopy(r)
	content, format, ok := operatorSource(r, "about")
	if !ok {
		content = fmt.Sprintf("# %s\n\n> %s\n\n%s\n\n## %s\n\n- %s\n- %s\n- %s\n- %s\n- %s\n- %s\n\n## %s\n\n- [GitHub](https://github.com/apimgr/gitmessages)\n- [Documentation](https://apimgr-gitmessages.readthedocs.io/)\n- [REST API](/server/docs/swagger)\n- [GraphQL](/server/docs/graphql)", name, tagline, description, tr(r, "about.features"), tr(r, "about.feature.catalog"), tr(r, "about.feature.templates"), tr(r, "about.feature.builder"), tr(r, "about.feature.api"), tr(r, "about.feature.cli"), tr(r, "about.feature.networks"), tr(r, "about.links"))
		format = "markdown"
	}
	content += "\n\n## " + tr(r, "about.original_content") + "\n\n" + tr(r, "about.catalog_attribution") + "\n\n## " + tr(r, "about.attribution") + "\n\n<a href=\"https://db-ip.com/\">IP Geolocation by DB-IP</a>"
	writeSuccess(w, r, map[string]string{"title": name, "content": content, "format": format})
}

func aboutCopy(r *http.Request) (string, string) {
	branding := config.Get().Server.Branding
	tagline := strings.TrimSpace(branding.Tagline)
	if tagline == "" {
		tagline = tr(r, "about.body")
	}
	description := strings.TrimSpace(branding.Description)
	if description == "" {
		description = tr(r, "about.detail")
	}
	return tagline, description
}

func apiHelp(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	content := strings.TrimSpace(config.Get().Server.Pages.Help.Content)
	if content == "" {
		content = config.DefaultConfig().Server.Pages.Help.Content
	}
	writeSuccess(w, r, map[string]any{
		"title":   tr(r, "help.title"),
		"content": content,
		"format":  "markdown",
		"pages":   serverHubPages(r, "/server/help"),
	})
}

func apiMCPHelp(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, mcpHelpData(r))
}

func mcpHelpData(r *http.Request) map[string]any {
	tools := make([]map[string]string, 0)
	for _, tool := range mcpTools(r) {
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		tools = append(tools, map[string]string{"name": name, "description": description})
	}
	project := strings.TrimSpace(config.Get().Server.Pages.Help.MCP)
	if project == "" {
		project = config.DefaultConfig().Server.Pages.Help.MCP
	}
	return map[string]any{
		"enabled": true,
		"url":     BuildURL(r, APIBasePath()+"/server/mcp"),
		"aliases": []string{BuildURL(r, "/api/mcp"), BuildURL(r, "/mcp")},
		"auth":    map[string]string{"scheme": "Bearer"},
		"tools":   tools,
		"project": project,
	}
}

func apiPrivacy(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	privacy := config.Get().Server.Privacy
	trackingConfig := config.Get().Server.Tracking
	configuredServices := privacyThirdPartyServices(privacy, trackingConfig)
	services := make([]map[string]string, 0, len(configuredServices))
	for _, service := range configuredServices {
		services = append(services, map[string]string{"name": service.Name, "purpose": service.Purpose, "data_sent": service.DataSent, "policy_url": service.PolicyURL})
	}
	configuredSharing := privacySharing(privacy, trackingConfig)
	sharing := make([]map[string]string, 0, len(configuredSharing))
	for _, item := range configuredSharing {
		sharing = append(sharing, map[string]string{"condition": item.Condition, "when": item.When, "data": item.Data})
	}
	analyticsEnabled := privacy.Cookies.Analytics.Enabled
	tracking := map[string]any{"enabled": trackingConfigured(), "type": trackingConfig.Type, "type_name": trackingTypeName(trackingConfig)}
	category := func(value config.PrivacyCookieCategory) map[string]any {
		return map[string]any{"enabled": value.Enabled, "description": value.Description}
	}
	ccpaOptedOut := false
	if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" || privacySignalFromRequest(r) == "gpc" {
		ccpaOptedOut = true
	}
	writeSuccess(w, r, map[string]any{
		"summary": map[string]any{"data_stored_on_server": privacy.Data.StoredOnServer, "data_sold": privacy.Data.Sold, "user_control": true},
		"cookies": map[string]any{
			"essential":   category(privacy.Cookies.Essential),
			"preferences": category(privacy.Cookies.Preferences),
			"analytics":   map[string]any{"enabled": analyticsEnabled, "description": privacy.GetAnalyticsDescription()},
		},
		"data":        map[string]any{"sold": privacy.Data.Sold, "stored_on_server": privacy.Data.StoredOnServer, "sharing": sharing},
		"tracking":    tracking,
		"retention":   map[string]any{"period": privacy.Retention.PeriodForLanguage(languageCode(r)), "export_available": privacy.Retention.ExportAvailable, "deletion_available": privacy.Retention.DeletionAvailable},
		"third_party": map[string]any{"services": services},
		"ccpa":        map[string]any{"applicable": privacy.IsCCPAApplicable(), "opt_out_url": "/server/privacy#ccpa-opt-out", "user_opted_out": ccpaOptedOut},
		"content":     map[string]any{"consent_message": privacy.GetConsentMessage(), "data_collection": tr(r, "privacy.collection_body"), "data_usage": privacy.GetDataUsageContent(), "data_security": tr(r, "privacy.security_body")},
	})
}

func apiTerms(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	content, format, ok := operatorSource(r, "terms")
	if !ok {
		content = fmt.Sprintf("# %s\n\n## %s\n\n%s\n\n## %s\n\n%s\n\n[ %s ](/server/security/policy)\n\n## %s\n\n%s\n\n## %s\n\n%s\n\n## %s\n\n%s", tr(r, "terms.title"), tr(r, "terms.acceptance"), tr(r, "terms.acceptance_body"), tr(r, "terms.acceptable_use"), tr(r, "terms.acceptable_use_body"), tr(r, "security.policy"), tr(r, "terms.liability"), tr(r, "terms.liability_body"), tr(r, "terms.changes"), tr(r, "terms.changes_body"), tr(r, "terms.governing_law"), tr(r, "terms.governing_law_body"))
		format = "markdown"
	}
	writeSuccess(w, r, map[string]string{"title": tr(r, "terms.title"), "content": content, "format": format})
}

func apiPreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		apiPreferenceSave(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	theme, lang, _ := preferenceState(r)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"lang": lang, "theme": theme, "prefs": map[string]string{}}})
}

func serverPageBreadcrumb(r *http.Request) template.HTML {
	var parent, label string
	switch {
	case strings.HasPrefix(r.URL.Path, "/server/preferences/"):
		parent, label = "/server/preferences", tr(r, "preferences.title")
	case strings.HasPrefix(r.URL.Path, "/server/help/"):
		parent, label = "/server/help", tr(r, "nav.help")
	default:
		return ""
	}
	return template.HTML(`<nav class="breadcrumb" aria-label="Breadcrumb"><a href="` + parent + `">← ` + template.HTMLEscapeString(label) + `</a></nav>`)
}

func page(w http.ResponseWriter, r *http.Request, title string, body template.HTML) {
	stamp := assetStamp()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+stamp+`"`)
	if old, err := r.Cookie("gitmessages_build"); err == nil && old.Value != stamp && isTopLevelHTMLNavigation(r) {
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	}
	http.SetCookie(w, &http.Cookie{Name: "gitmessages_build", Value: stamp, Path: "/", MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r) && secureOwnerCookie(r)})
	locale := i18n.Get(r.Header.Get("X-GitMessages-Language"))
	csrf := ensureCSRFCookie(w, r)
	theme := selectedTheme(r)
	var output bytes.Buffer
	stampURL := template.HTMLEscapeString(url.QueryEscape(stamp))
	notificationSettings := config.Get().Server.Notifications.WebUI
	componentStamp := fmt.Sprintf("%s-%d-%d", stamp, notificationSettings.Duration, notificationSettings.WarningDuration)
	componentStyleURL := template.HTMLEscapeString(url.QueryEscape(componentStamp))
	offlineStrings, _ := json.Marshal(map[string]string{
		"title":   tr(r, "common.offline"),
		"heading": tr(r, "common.offline"),
		"message": tr(r, "common.offline_body"),
		"reload":  tr(r, "offline.reload"),
	})
	nav := navigationMarkup(r)
	nextTheme := nextTheme(theme)
	themeLabel := strings.ReplaceAll(tr(r, "theme.switch_label"), "{theme}", tr(r, "theme."+theme))
	themeIcon := themeIconMarkup(theme)
	robots := `<meta name="robots" content="` + seoRobotsDirective(r.URL.Path) + `">`
	lead := serverPageLead(r)
	if r.URL.Query().Get("consent_required") == "1" {
		body = template.HTML(`<section class="section-card" role="status"><p>` + template.HTMLEscapeString(tr(r, "preferences.consent_required")) + ` <a href="/server/preferences#privacy-preferences">` + template.HTMLEscapeString(tr(r, "privacy.manage_preferences")) + `</a></p></section>` + string(body))
	}
	if strings.HasPrefix(r.URL.Path, "/server/") && !strings.Contains(string(body), `class="section-card`) {
		body = template.HTML(`<section class="section-card" aria-labelledby="page-details"><h2 id="page-details">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2>` + string(body) + `</section>`)
	}
	pageLead := ""
	if lead != "" {
		pageLead = `<p class="page-lead">` + template.HTMLEscapeString(lead) + `</p>`
	}
	customFooter := operatorHTML(r, "footer")
	customHeader := operatorHTML(r, "header")
	containerClass := pageContainerClass(r.URL.Path)
	toolStyles := pageToolStylesheets(r)
	trackingTags, trackingNoscript := trackingMarkup(r)
	consentBanner := consentBannerMarkup(r, csrf)
	overlayFooter := overlayFooterMarkup(r)
	headerThemeToggle := ""
	if r.URL.Path != "/server/preferences" {
		headerThemeToggle = fmt.Sprintf(`<form class="theme-toggle-form" method="post" action="/server/preferences"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="theme" value="%s"><button type="submit" aria-label="%s" title="%s">%s <span>%s</span></button></form>`, template.HTMLEscapeString(csrf), nextTheme, template.HTMLEscapeString(themeLabel), template.HTMLEscapeString(tr(r, "nav.theme")), themeIcon, template.HTMLEscapeString(tr(r, "theme."+nextTheme)))
	}
	_, _ = fmt.Fprintf(&output, `<!doctype html>
<html lang="%s" dir="%s" class="theme-%s" data-theme="%s" data-build="%s" data-base-path="%s">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="theme-color" content="#282a36">
    <meta name="description" content="%s">
    %s
	    <link rel="icon" href="/.well-known/branding/favicon?v=%s">
    <link rel="icon" type="image/png" sizes="16x16" href="/static/icons/favicon-16.png?v=%s">
    <link rel="icon" type="image/png" sizes="32x32" href="/static/icons/favicon-32.png?v=%s">
    <link rel="apple-touch-icon" sizes="180x180" href="/static/icons/icon-180.png?v=%s">
    <meta name="apple-mobile-web-app-capable" content="yes">
    <meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
    <meta name="apple-mobile-web-app-title" content="%s">
    <link rel="apple-touch-startup-image" href="/static/splash/iphone-1179x2556.png?v=%s" media="(device-width: 393px) and (device-height: 852px) and (-webkit-device-pixel-ratio: 3)">
    <link rel="apple-touch-startup-image" href="/static/splash/iphone-1284x2778.png?v=%s" media="(device-width: 428px) and (device-height: 926px) and (-webkit-device-pixel-ratio: 3)">
    %s
    <link rel="manifest" href="/manifest.json?v=%s">
	    <link rel="stylesheet" href="/static/css/common.css?v=%s">
	    <link rel="stylesheet" href="/static/css/components.css?v=%s">
	    <link rel="stylesheet" href="/static/css/public.css?v=%s">
    %s
    %s
    <title>%s</title>
  </head>
	  <body data-toast-duration="%d" data-toast-warning-duration="%d" data-toast-dismiss="%s">%s<script type="application/json" id="offline-strings">%s</script>
	    <a class="skip-link" href="#main-content">%s</a><a class="skip-link skip-link-secondary" href="#navigation">%s</a>%s<input type="checkbox" id="nav-toggle" class="nav-checkbox" aria-label="%s"><header role="banner"><a class="brand" href="/">%s</a><nav class="nav-links" id="navigation" aria-label="%s">%s</nav><label class="nav-toggle" for="nav-toggle" aria-hidden="true">☰</label><div class="header-actions"><a href="/server/preferences">%s</a>%s</div>%s</header>
    <main id="main-content" role="main" tabindex="-1"><div class="container %s"><header class="page-header">%s<h1 class="page-title">%s</h1>%s</header>%s</div></main>
        <nav class="nav-panel" id="nav-panel" aria-label="%s"><label for="nav-toggle" class="nav-close" aria-label="%s">✕</label>%s</nav><label for="nav-toggle" class="nav-overlay" aria-hidden="true"></label>
	        <div id="offline-indicator" class="offline-indicator" role="status" aria-live="polite" hidden>%s</div><div id="toast-container" class="toast-container" data-position="%s" aria-label="%s" aria-live="polite"></div>
        <div id="pwa-update-prompt" class="pwa-update-prompt" role="status" aria-live="polite" hidden><span>%s</span><button id="pwa-update-now" type="button">%s</button></div>
        <section id="pwa-install" class="pwa-install" aria-label="%s" hidden><button id="pwa-install-button" type="button">%s</button><details id="pwa-ios-install" hidden><summary>%s</summary><p>%s</p></details></section>
	        %s<footer role="contentinfo">%s%s · <a href="/server/about">%s</a> · <a href="/server/help">%s</a> · <a href="/server/privacy">%s</a> · <a href="/server/terms">%s</a> · <a href="/server/security">%s</a> · <a href="/server/preferences">%s</a> · <a href="/server/preferences#language" lang="%s">%s</a> · <a href="/server/docs/swagger">%s</a> · <a href="/server/docs/graphql">%s</a></footer>
    <script src="/static/js/app.js?v=%s" defer></script>
  </body>
</html>
	`, locale.Meta.Language, locale.Meta.Direction, theme, theme, template.HTMLEscapeString(stamp), template.HTMLEscapeString(requestBasePath(r)), template.HTMLEscapeString(seoDescription(r)), seoHeadMarkup(r, title), stampURL, stampURL, stampURL, stampURL, template.HTMLEscapeString(seoBrandingTitle()), stampURL, stampURL, robots, stampURL, stampURL, componentStyleURL, stampURL, toolStyles, trackingTags, template.HTMLEscapeString(seoPageTitle(title)), notificationSettings.Duration, notificationSettings.WarningDuration, template.HTMLEscapeString(tr(r, "toast.dismiss")), trackingNoscript, string(offlineStrings), tr(r, "nav.skip"), tr(r, "nav.skip_navigation"), consentBanner, tr(r, "nav.toggle"), brandingHeaderMarkup(r), tr(r, "nav.main"), nav, tr(r, "preferences.title"), headerThemeToggle, customHeader, containerClass, serverPageBreadcrumb(r), template.HTMLEscapeString(title), pageLead, body, tr(r, "nav.main"), tr(r, "nav.close"), nav, tr(r, "pwa.offline_indicator"), notificationSettings.Position, tr(r, "toast.container"), tr(r, "pwa.update_available"), tr(r, "pwa.update_now"), tr(r, "pwa.install_label"), tr(r, "pwa.install_button"), tr(r, "pwa.install_ios_title"), tr(r, "pwa.install_ios_instructions"), customFooter, overlayFooter, template.HTMLEscapeString(seoBrandingTitle()), tr(r, "nav.about"), tr(r, "nav.help"), tr(r, "nav.privacy"), tr(r, "nav.terms"), tr(r, "nav.security"), tr(r, "preferences.title"), locale.Meta.Language, template.HTMLEscapeString(locale.Meta.NativeName), tr(r, "api.rest"), tr(r, "graphql.title"), stampURL)
	pageHTML := prefixHTMLPaths(output.String(), requestBasePath(r))
	if wantsFrontendText(r) {
		writeText(w, http.StatusOK, formatHTMLText(pageHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
}

func overlayFooterMarkup(r *http.Request) string {
	statuses := overlayStatuses()
	var output strings.Builder
	if status, ok := statuses["tor"].(overlay.TorStatus); ok && status.Enabled && status.Running && status.Hostname != "" {
		fmt.Fprintf(&output, `<p class="footer-onion"><a href="/server/help#tor-access" aria-label="%s">🧅</a> <code>%s</code> <button type="button" class="copy-btn" data-copy="%s" aria-live="polite" aria-label="%s">📋</button></p>`, template.HTMLEscapeString(tr(r, "overlay.support.tor")), template.HTMLEscapeString(status.Hostname), template.HTMLEscapeString(status.Hostname), template.HTMLEscapeString(tr(r, "overlay.copy.tor")))
	}
	if status, ok := statuses["i2p"].(overlay.I2PStatus); ok && status.Enabled && status.Running {
		address := status.Hostname
		if address == "" {
			address = status.Address
		}
		if strings.HasSuffix(strings.ToLower(address), ".b32.i2p") {
			fmt.Fprintf(&output, `<p class="footer-i2p"><a href="/server/help#i2p-access" aria-label="%s">🔗</a> <code>%s</code> <button type="button" class="copy-btn" data-copy="%s" aria-live="polite" aria-label="%s">📋</button></p>`, template.HTMLEscapeString(tr(r, "overlay.support.i2p")), template.HTMLEscapeString(address), template.HTMLEscapeString(address), template.HTMLEscapeString(tr(r, "overlay.copy.i2p")))
		}
	}
	if output.Len() == 0 {
		return ""
	}
	return output.String()
}

func serverRootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, BuildURL(r, "/server/about"), http.StatusMovedPermanently)
}

func pageContainerClass(path string) string {
	switch path {
	case "/builder", "/server/contact", "/server/preferences", "/server/preferences/sync", "/server/preferences/tokens":
		return "container-sm"
	default:
		return "container-md"
	}
}

func pageToolStylesheets(r *http.Request) string {
	version := url.QueryEscape(assetStamp())
	switch r.URL.Path {
	case "/server/docs/swagger":
		return `<link rel="stylesheet" href="/static/vendor/swagger-ui/swagger-ui.css?v=` + version + `"><link rel="stylesheet" href="/static/vendor/swagger-ui/theme.css?v=` + version + `">`
	case "/server/docs/graphql":
		return `<link rel="stylesheet" href="/static/vendor/graphiql/graphiql.css?v=` + version + `"><link rel="stylesheet" href="/static/vendor/graphiql/theme.css?v=` + version + `">`
	default:
		return ""
	}
}

func serverPageLead(r *http.Request) string {
	switch r.URL.Path {
	case "/server/about":
		return tr(r, "about.body")
	case "/server/help":
		return tr(r, "help.lead")
	case "/server/help/mcp":
		return tr(r, "mcp.help.description")
	case "/server/docs/swagger":
		return tr(r, "swagger.info.description")
	case "/server/docs/graphql":
		return tr(r, "graphql.description")
	case "/server/privacy":
		return tr(r, "privacy.body")
	case "/server/terms":
		if operatorHTML(r, "terms") != "" {
			return ""
		}
		return tr(r, "terms.body")
	case "/server/preferences":
		return tr(r, "preferences.lead")
	case "/server/preferences/sync":
		return tr(r, "preferences.sync.description")
	case "/server/preferences/tokens":
		return tr(r, "preferences.tokens.description")
	case "/server/healthz":
		return tr(r, "health.ok")
	case "/server/security":
		return tr(r, "security.body")
	case "/server/security/policy":
		return tr(r, "security.body")
	case "/server/security/thanks":
		return tr(r, "security.thanks.intro")
	case "/server/dpo":
		return tr(r, "contact.dpo_lead")
	case "/server/contact":
		return tr(r, "contact.intro")
	default:
		if strings.HasPrefix(r.URL.Path, "/server/security/report/") {
			return tr(r, "contact.status_lead")
		}
		return ""
	}
}

func prefixHTMLPaths(markup, prefix string) string {
	if prefix == "" {
		return markup
	}
	for _, attribute := range []string{`href="`, `src="`, `action="`} {
		searchFrom := 0
		for {
			index := strings.Index(markup[searchFrom:], attribute)
			if index < 0 {
				break
			}
			valueStart := searchFrom + index + len(attribute)
			if valueStart >= len(markup) || markup[valueStart] != '/' || (valueStart+1 < len(markup) && markup[valueStart+1] == '/') {
				searchFrom = valueStart
				continue
			}
			markup = markup[:valueStart] + prefix + markup[valueStart:]
			searchFrom = valueStart + len(prefix) + 1
		}
	}
	return markup
}

func navigationMarkup(r *http.Request) string {
	var links strings.Builder
	for _, item := range []struct{ path, key string }{{"/", "nav.home"}, {"/types", "nav.types"}, {"/templates", "nav.templates"}, {"/builder", "nav.builder"}} {
		current := ""
		active := ""
		if r.URL.Path == item.path {
			current = ` aria-current="page"`
			active = ` class="active"`
		}
		fmt.Fprintf(&links, `<a href="%s"%s%s>%s</a>`, item.path, current, active, tr(r, item.key))
	}
	return links.String()
}

func nextTheme(current string) string {
	switch current {
	case "dark":
		return "light"
	case "light":
		return "auto"
	default:
		return "dark"
	}
}

func themeIconMarkup(theme string) template.HTML {
	var shape string
	switch theme {
	case "light":
		shape = `<circle cx="12" cy="12" r="4"></circle><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42"></path>`
	case "auto":
		shape = `<path d="M12 3a9 9 0 1 0 0 18V3z"></path><circle cx="12" cy="12" r="9"></circle>`
	default:
		shape = `<path d="M20.9 13A9 9 0 0 1 11 3.1 9 9 0 1 0 20.9 13z"></path>`
	}
	return template.HTML(`<svg class="theme-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + shape + `</svg>`)
}

func selectedTheme(r *http.Request) string {
	if privacySignalFromRequest(r) == "" {
		if cookie, err := r.Cookie("theme"); err == nil {
			switch cookie.Value {
			case "auto", "light", "dark":
				return cookie.Value
			}
		}
	}
	defaultThemeMu.RLock()
	theme := defaultTheme
	defaultThemeMu.RUnlock()
	return theme
}

func home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		message := tr(r, "errors.not_found")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", message)
			return
		}
		if wantsFrontendText(r) {
			writeText(w, http.StatusNotFound, message)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		page(w, r, "404", template.HTML(`<section class="section-card" aria-labelledby="error-message"><h2 id="error-message">`+template.HTMLEscapeString(message)+`</h2></section>`))
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	tagline := strings.TrimSpace(config.Get().Server.Branding.Tagline)
	if tagline == "" {
		tagline = tr(r, "home.intro")
	}
	page(w, r, seoBrandingTitle(), template.HTML(`<p class="page-lead">`+template.HTMLEscapeString(tagline)+`</p><p><a href="/builder">`+tr(r, "home.build")+`</a></p><section class="section-card" aria-labelledby="home-browse"><h2 id="home-browse">`+tr(r, "home.browse")+`</h2><p><a href="/types">`+tr(r, "nav.types")+`</a> · <a href="/templates">`+tr(r, "nav.templates")+`</a> · <a href="/server/docs/swagger">`+tr(r, "api.rest")+`</a> · <a href="/server/docs/graphql">`+tr(r, "api.graphql")+`</a></p></section>`))
}

func typesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/types" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	body.WriteString(`<section class="section-card" aria-labelledby="types-catalog"><h2 id="types-catalog">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2>`)
	for _, item := range messages.TypesForLocale(languageCode(r)) {
		fmt.Fprintf(&body, `<article><h3>%s %s</h3><p>%s</p>%s</article>`, template.HTMLEscapeString(item.Emoji), template.HTMLEscapeString(item.Type), template.HTMLEscapeString(item.Description), codeBlockWithCopy(r, item.Example))
	}
	body.WriteString(`</section>`)
	page(w, r, tr(r, "types.title"), template.HTML(body.String()))
}

func templatesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/templates" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	body.WriteString(`<section class="section-card" aria-labelledby="templates-catalog"><h2 id="templates-catalog">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2>`)
	for _, item := range messages.TemplatesForLocale(languageCode(r)) {
		fmt.Fprintf(&body, `<article><h3>%s</h3><p>%s: %s</p>%s</article>`, template.HTMLEscapeString(item.Name), tr(r, "templates.category"), template.HTMLEscapeString(item.Category), codeBlockWithCopy(r, item.Message))
	}
	body.WriteString(`</section>`)
	page(w, r, tr(r, "templates.title"), template.HTML(body.String()))
}

func builderPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			writeHTTPError(w, r, tr(r, "builder.invalid"), http.StatusBadRequest)
			return
		}
		message, err := messages.Build(messages.BuildInput{Type: r.FormValue("type"), Scope: r.FormValue("scope"), Description: r.FormValue("description"), Body: r.FormValue("body"), Breaking: r.FormValue("breaking") == "true", Convention: r.FormValue("convention")})
		if err != nil {
			csrf := csrfTokenForPage(w, r)
			page(w, r, tr(r, "builder.title"), builderForm(r, template.HTMLEscapeString(builderError(r, err)), r.Form, csrf))
			return
		}
		recordMessageBuild()
		page(w, r, tr(r, "builder.result"), template.HTML(`<section class="section-card" aria-labelledby="builder-result-details"><h2 id="builder-result-details">`+template.HTMLEscapeString(tr(r, "page.details"))+`</h2><p>`+tr(r, "builder.copy")+`</p>`+codeBlockWithCopy(r, message)+`<p><a href="/builder">`+tr(r, "builder.another")+`</a></p></section>`))
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeHTTPError(w, r, tr(r, "api.method_not_allowed"), http.StatusMethodNotAllowed)
		return
	}
	csrf := csrfTokenForPage(w, r)
	form := builderForm(r, "", nil, csrf)
	page(w, r, tr(r, "builder.title"), templateHTML(`<section class="section-card" aria-labelledby="builder-form-details"><h2 id="builder-form-details">`+template.HTMLEscapeString(tr(r, "page.details"))+`</h2>`+string(form)+`</section>`))
}

func builderError(r *http.Request, err error) string {
	key := map[string]string{
		"unknown commit type":                                                         "builder.error.unknown_type",
		"description must be a non-empty single line":                                 "builder.error.description_required",
		"description must be 100 characters or fewer":                                 "builder.error.description_too_long",
		"scope must be 1–50 letters, numbers, dots, slashes, underscores, or hyphens": "builder.error.scope_invalid",
		"body must be 5000 characters or fewer":                                       "builder.error.body_too_long",
		"unknown commit convention":                                                   "builder.error.unknown_convention",
	}[err.Error()]
	if key == "" {
		return tr(r, "builder.invalid")
	}
	return tr(r, key)
}

func builderForm(r *http.Request, problem string, values url.Values, csrf string) template.HTML {
	var options strings.Builder
	for _, item := range messages.Types() {
		selected := ""
		if values != nil && values.Get("type") == item.Type {
			selected = " selected"
		}
		fmt.Fprintf(&options, `<option value="%s"%s>%s %s — %s</option>`, template.HTMLEscapeString(item.Type), selected, template.HTMLEscapeString(item.Emoji), template.HTMLEscapeString(item.Type), template.HTMLEscapeString(item.Description))
	}
	problemHTML := ""
	if problem != "" {
		problemHTML = `<p role="alert">` + problem + `</p>`
	}
	return template.HTML(problemHTML + `<form method="post" action="/builder"><input type="hidden" name="csrf_token" value="` + template.HTMLEscapeString(csrf) + `">
  <label for="type">` + tr(r, "builder.type") + `</label><select id="type" name="type" required aria-required="true">` + options.String() + `</select>
  <label for="scope">` + tr(r, "builder.scope") + `</label><input id="scope" name="scope" maxlength="50" pattern="[A-Za-z0-9][A-Za-z0-9._/-]{0,49}">
  <label for="description">` + tr(r, "builder.description") + `</label><input id="description" name="description" required aria-required="true" maxlength="100" minlength="1">
  <label for="body">` + tr(r, "builder.body") + `</label><textarea id="body" name="body" maxlength="5000" rows="6"></textarea>
  <label for="convention">` + tr(r, "builder.convention") + `</label><select id="convention" name="convention"><option value="conventional-commits">Conventional Commits</option><option value="angular">Angular</option></select>
  <label><input type="checkbox" name="breaking" value="true"> ` + tr(r, "builder.breaking") + `</label>
  <button type="submit">` + tr(r, "builder.submit") + `</button>
</form>`)
}

func aboutPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/about" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	tagline, description := aboutCopy(r)
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-description"><h2 id="about-description">%s</h2><p class="page-lead">%s</p><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "about.overview")), template.HTMLEscapeString(tagline), template.HTMLEscapeString(description))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-features"><h2 id="about-features">%s</h2><ul><li>%s</li><li>%s</li><li>%s</li><li>%s</li><li>%s</li><li>%s</li></ul></section>`, template.HTMLEscapeString(tr(r, "about.features")), template.HTMLEscapeString(tr(r, "about.feature.catalog")), template.HTMLEscapeString(tr(r, "about.feature.templates")), template.HTMLEscapeString(tr(r, "about.feature.builder")), template.HTMLEscapeString(tr(r, "about.feature.api")), template.HTMLEscapeString(tr(r, "about.feature.cli")), template.HTMLEscapeString(tr(r, "about.feature.networks")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-version"><h2 id="about-version">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "about.version")), template.HTMLEscapeString(serverVersion))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-links"><h2 id="about-links">%s</h2><ul><li><a href="https://github.com/apimgr/gitmessages">%s</a></li><li><a href="https://apimgr-gitmessages.readthedocs.io/">%s</a></li><li><a href="/server/docs/swagger">%s</a></li><li><a href="/server/docs/graphql">%s</a></li></ul></section>`, template.HTMLEscapeString(tr(r, "about.links")), template.HTMLEscapeString(tr(r, "about.github")), template.HTMLEscapeString(tr(r, "about.documentation")), template.HTMLEscapeString(tr(r, "api.rest")), template.HTMLEscapeString(tr(r, "graphql.title")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-original-content"><h2 id="about-original-content">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "about.original_content")), template.HTMLEscapeString(tr(r, "about.catalog_attribution")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="about-attribution"><h2 id="about-attribution">%s</h2><p><a href="https://db-ip.com/">IP Geolocation by DB-IP</a></p></section>`, template.HTMLEscapeString(tr(r, "about.attribution")))
	body.WriteString(string(operatorCard(r, "about", "operator-about")))
	page(w, r, seoBrandingTitle(), template.HTML(body.String()))
}

func helpPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/help" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	overlays := overlayStatuses()
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="help-start"><h2 id="help-start">%s</h2><p>%s</p>%s</section><section class="section-card" aria-labelledby="help-features"><h2 id="help-features">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "help.getting_started")), template.HTMLEscapeString(tr(r, "help.getting_started_body")), codeBlockWithCopy(r, "curl -q -LSsf "+BuildURL(r, APIBasePath()+"/types")+"\ncurl -q -LSsf "+BuildURL(r, APIBasePath()+"/conventions")+"\ncurl -q -LSsf -X POST -H 'Content-Type: application/json' -d '{\"type\":\"feat\",\"description\":\"add localized commit examples\"}' "+BuildURL(r, APIBasePath()+"/messages")), template.HTMLEscapeString(tr(r, "help.using_catalog")), template.HTMLEscapeString(tr(r, "help.using_catalog_body")))
	projectHelp := strings.TrimSpace(config.Get().Server.Pages.Help.Content)
	if projectHelp == "" {
		projectHelp = config.DefaultConfig().Server.Pages.Help.Content
	}
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="help-project"><h2 id="help-project">%s</h2>%s</section>`, template.HTMLEscapeString(tr(r, "help.features")), renderMarkdown(projectHelp))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="help-resources"><h2 id="help-resources">%s</h2><p>%s <a href="/server/docs/swagger">%s</a> · <a href="/server/docs/graphql">%s</a></p></section>`, template.HTMLEscapeString(tr(r, "help.api_documentation")), template.HTMLEscapeString(tr(r, "help.api.docs")), template.HTMLEscapeString(tr(r, "api.rest")), template.HTMLEscapeString(tr(r, "graphql.title")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="help-faq"><h2 id="help-faq">%s</h2><h3>%s</h3><p>%s</p><h3>%s</h3><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "help.faq")), template.HTMLEscapeString(tr(r, "help.account_question")), template.HTMLEscapeString(tr(r, "help.account_answer")), template.HTMLEscapeString(tr(r, "help.submission_question")), template.HTMLEscapeString(tr(r, "help.submission_answer")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="help-troubleshooting"><h2 id="help-troubleshooting">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "help.troubleshooting")), template.HTMLEscapeString(tr(r, "help.troubleshooting_body")))
	if status, ok := overlays["tor"].(overlay.TorStatus); ok && status.Enabled && status.Running && status.Hostname != "" {
		fmt.Fprintf(&body, `<section class="section-card" id="tor-access" aria-labelledby="tor-access-title"><h2 id="tor-access-title">%s</h2><p>%s</p><h3>%s</h3>%s<h3>%s</h3><ol><li>%s <a href="https://www.torproject.org/download/" target="_blank" rel="noopener noreferrer">%s</a></li><li>%s</li><li>%s</li></ol><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "overlay.tor.title")), template.HTMLEscapeString(tr(r, "overlay.tor.description")), template.HTMLEscapeString(tr(r, "overlay.address")), codeBlockWithCopy(r, status.Hostname), template.HTMLEscapeString(tr(r, "overlay.connect")), template.HTMLEscapeString(tr(r, "overlay.tor.step.download")), template.HTMLEscapeString(tr(r, "overlay.tor.browser")), template.HTMLEscapeString(tr(r, "overlay.tor.step.connect")), template.HTMLEscapeString(tr(r, "overlay.tor.step.open")), template.HTMLEscapeString(tr(r, "overlay.tor.note")))
	}
	if status, ok := overlays["i2p"].(overlay.I2PStatus); ok && status.Enabled && status.Running {
		address := status.Hostname
		if address == "" {
			address = status.Address
		}
		if strings.HasSuffix(strings.ToLower(address), ".b32.i2p") {
			fmt.Fprintf(&body, `<section class="section-card" id="i2p-access" aria-labelledby="i2p-access-title"><h2 id="i2p-access-title">%s</h2><p>%s</p><h3>%s</h3>%s<h3>%s</h3><ol><li>%s <a href="https://geti2p.net/en/download" target="_blank" rel="noopener noreferrer">%s</a> %s <a href="https://i2pd.website/" target="_blank" rel="noopener noreferrer">i2pd</a></li><li>%s</li><li>%s</li></ol><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "overlay.i2p.title")), template.HTMLEscapeString(tr(r, "overlay.i2p.description")), template.HTMLEscapeString(tr(r, "overlay.address")), codeBlockWithCopy(r, address), template.HTMLEscapeString(tr(r, "overlay.connect")), template.HTMLEscapeString(tr(r, "overlay.i2p.step.install")), template.HTMLEscapeString(tr(r, "overlay.i2p.client")), template.HTMLEscapeString(tr(r, "overlay.i2p.or")), template.HTMLEscapeString(tr(r, "overlay.i2p.step.proxy")), template.HTMLEscapeString(tr(r, "overlay.i2p.step.open")), template.HTMLEscapeString(tr(r, "overlay.i2p.note")))
		}
	}
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="more-help"><h2 id="more-help">%s</h2><ul class="hub-list">%s</ul></section>`, template.HTMLEscapeString(tr(r, "help.more")), serverHubList(r, "/server/help"))
	page(w, r, tr(r, "help.title"), template.HTML(body.String()))
}

func mcpHelpPage(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<section class="section-card" id="overview" aria-labelledby="mcp-overview-title"><h2 id="mcp-overview-title">%s</h2><p>%s</p></section>`, tr(r, "mcp.help.overview"), tr(r, "mcp.help.description"))
	fmt.Fprintf(&body, `<section class="section-card" id="endpoint" aria-labelledby="mcp-endpoint-title"><h2 id="mcp-endpoint-title">%s</h2>`, tr(r, "mcp.help.endpoint"))
	for _, endpoint := range []string{"POST " + BuildURL(r, APIBasePath()+"/server/mcp"), BuildURL(r, "/api/mcp"), BuildURL(r, "/mcp")} {
		body.WriteString(codeBlockWithCopy(r, endpoint))
	}
	fmt.Fprintf(&body, `<p>%s</p></section>`, tr(r, "mcp.help.endpoint"))
	fmt.Fprintf(&body, `<section class="section-card" id="authentication" aria-labelledby="mcp-authentication-title"><h2 id="mcp-authentication-title">%s</h2><p>%s</p></section>`, tr(r, "mcp.help.authentication"), tr(r, "mcp.help.auth"))
	mcpURL := BuildURL(r, APIBasePath()+"/server/mcp")
	claudeCommand := `claude mcp add --transport http gitmessages ` + mcpURL + ` --header "Authorization: Bearer $MCP_TOKEN"`
	clientConfig := `{"mcpServers":{"gitmessages":{"type":"http","url":"` + mcpURL + `","headers":{"Authorization":"Bearer ${MCP_TOKEN}"}}}}`
	curlRequest := `curl -q -LSsf -X POST ` + mcpURL + ` -H "Authorization: Bearer $MCP_TOKEN" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "MCP-Protocol-Version: 2026-07-28" -H "Mcp-Method: tools/list" --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}'`
	fmt.Fprintf(&body, `<section class="section-card" id="connect" aria-labelledby="mcp-connect-title"><h2 id="mcp-connect-title">%s</h2><p>%s</p><h3>%s</h3>%s<h3>%s</h3>%s<h3>%s</h3>%s</section>`, tr(r, "mcp.help.connect"), tr(r, "mcp.help.connect"), tr(r, "mcp.help.claude"), codeBlockWithCopy(r, claudeCommand), tr(r, "mcp.help.generic"), codeBlockWithCopy(r, clientConfig), tr(r, "mcp.help.verify"), codeBlockWithCopy(r, curlRequest))
	fmt.Fprintf(&body, `<section class="section-card" id="tools" aria-labelledby="mcp-tools-title"><h2 id="mcp-tools-title">%s</h2><div class="table-wrapper"><table class="data-table"><caption>%s</caption><thead><tr><th scope="col">%s</th><th scope="col">%s</th></tr></thead><tbody>`, tr(r, "mcp.help.tools"), tr(r, "mcp.help.tools"), tr(r, "mcp.help.tool_name"), tr(r, "mcp.help.tool_description"))
	for _, tool := range mcpTools(r) {
		fmt.Fprintf(&body, `<tr><td><code>%s</code></td><td>%s</td></tr>`, template.HTMLEscapeString(tool["name"].(string)), renderMarkdown(tool["description"].(string)))
	}
	body.WriteString(`</tbody></table></div></section>`)
	projectHelp := strings.TrimSpace(config.Get().Server.Pages.Help.MCP)
	if projectHelp == "" {
		projectHelp = config.DefaultConfig().Server.Pages.Help.MCP
	}
	fmt.Fprintf(&body, `<section class="section-card" id="limits" aria-labelledby="mcp-limits-title"><h2 id="mcp-limits-title">%s</h2><p>%s</p></section><section class="section-card" id="troubleshooting" aria-labelledby="mcp-troubleshooting-title"><h2 id="mcp-troubleshooting-title">%s</h2><p>%s</p><div class="table-wrapper"><table class="data-table"><caption>%s</caption><thead><tr><th scope="col">%s</th><th scope="col">%s</th></tr></thead><tbody><tr><td>200</td><td>%s</td></tr><tr><td>400</td><td>%s</td></tr><tr><td>401</td><td>%s</td></tr><tr><td>403</td><td>%s</td></tr><tr><td>405, 406, 415</td><td>%s</td></tr><tr><td>429</td><td>%s</td></tr></tbody></table></div></section><section class="section-card" id="project" aria-labelledby="mcp-project-title"><h2 id="mcp-project-title">%s</h2>%s</section>`, tr(r, "mcp.help.limits"), tr(r, "mcp.help.limits"), tr(r, "mcp.help.troubleshooting"), tr(r, "mcp.help.troubleshooting"), tr(r, "mcp.help.status_title"), tr(r, "mcp.help.status"), tr(r, "mcp.help.meaning"), tr(r, "mcp.help.status_200"), tr(r, "mcp.help.status_400"), tr(r, "mcp.help.status_401"), tr(r, "mcp.help.status_403"), tr(r, "mcp.help.status_headers"), tr(r, "mcp.help.status_429"), tr(r, "mcp.help.project"), renderMarkdown(projectHelp))
	page(w, r, tr(r, "mcp.title"), template.HTML(body.String()))
}

func codeBlockWithCopy(r *http.Request, value string) string {
	id := "copy-code-" + strconv.FormatUint(copyCodeSequence.Add(1), 10)
	label := template.HTMLEscapeString(tr(r, "tokens.copy"))
	copied := template.HTMLEscapeString(tr(r, "copied"))
	button := `<button type="button" class="copy-btn" data-copy="` + template.HTMLEscapeString(value) + `" data-copy-text="` + template.HTMLEscapeString(value) + `" data-copy-target="` + id + `" data-copy-label="` + label + `" data-copy-icon="📋" data-copied-label="` + copied + `" aria-label="` + label + `"><span class="copy-icon" aria-hidden="true">📋</span><span class="copy-text" aria-live="polite">` + label + `</span></button>`
	return `<div class="code-block-multi"><div class="code-header"><span class="code-lang">text</span>` + button + `</div><pre><code id="` + id + `">` + template.HTMLEscapeString(value) + `</code></pre></div>`
}

func privacyPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/privacy" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	privacy := config.Get().Server.Privacy
	var body strings.Builder
	if signal := privacySignalFromRequest(r); signal != "" {
		fmt.Fprintf(&body, `<section class="section-card" role="status"><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.signal."+signal)))
	}
	soldMessage := tr(r, "privacy.not_sold")
	if privacy.Data.Sold {
		soldMessage = tr(r, "privacy.sold")
	}
	storageMessage := tr(r, "privacy.not_stored")
	if privacy.Data.StoredOnServer {
		storageMessage = tr(r, "privacy.stored")
	}
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-summary"><h2 id="privacy-summary">%s</h2><p>%s</p><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.summary")), template.HTMLEscapeString(storageMessage), template.HTMLEscapeString(soldMessage))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-cookies"><h2 id="privacy-cookies">%s</h2><p>%s</p>`, template.HTMLEscapeString(tr(r, "privacy.cookies_title")), template.HTMLEscapeString(tr(r, "privacy.cookies_body")))
	for _, category := range []struct {
		title string
		value config.PrivacyCookieCategory
	}{{"preferences.essential_cookies", privacy.Cookies.Essential}, {"preferences.preference_cookies", privacy.Cookies.Preferences}} {
		fmt.Fprintf(&body, `<h3>%s</h3><p>%s</p>`, template.HTMLEscapeString(tr(r, category.title)), template.HTMLEscapeString(category.value.Description))
	}
	fmt.Fprintf(&body, `<h3>%s</h3>`, template.HTMLEscapeString(tr(r, "preferences.analytics_cookies")))
	if privacy.Cookies.Analytics.Enabled && trackingConfigured() {
		fmt.Fprintf(&body, `<p>%s</p><p>%s</p>`, template.HTMLEscapeString(privacy.GetAnalyticsDescription()), template.HTMLEscapeString(i18n.Format(languageCode(r), "privacy.analytics_provider", map[string]string{"provider": trackingTypeName(config.Get().Server.Tracking)})))
	} else {
		fmt.Fprintf(&body, `<p>%s</p>`, template.HTMLEscapeString(tr(r, "privacy.no_analytics")))
	}
	csrf := csrfTokenForPage(w, r)
	consentState := readCookieConsent(r)
	preferencesChecked, analyticsChecked := "", ""
	if consentState.Preferences {
		preferencesChecked = " checked"
	}
	if consentState.Analytics {
		analyticsChecked = " checked"
	}
	fmt.Fprintf(&body, `<p><button type="button" data-action="cookie-preferences">%s</button> <a href="/server/preferences#privacy-preferences">%s</a></p><dialog id="cookie-preferences-dialog" aria-labelledby="cookie-preferences-title"><form method="post" action="/server/consent"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="choice" value="save"><input type="hidden" name="redirect" value="/server/privacy"><h2 id="cookie-preferences-title">%s</h2><label><input type="checkbox" checked disabled> %s</label><label><input type="checkbox" name="preferences"%s> %s</label><label><input type="checkbox" name="analytics"%s> %s</label><button type="submit">%s</button><button type="button" data-action="cookie-preferences-close">%s</button></form></dialog></section>`, template.HTMLEscapeString(tr(r, "privacy.manage_preferences")), template.HTMLEscapeString(tr(r, "privacy.manage_preferences")), template.HTMLEscapeString(csrf), template.HTMLEscapeString(tr(r, "preferences.title")), template.HTMLEscapeString(tr(r, "preferences.essential_cookies")), preferencesChecked, template.HTMLEscapeString(tr(r, "preferences.preference_cookies")), analyticsChecked, template.HTMLEscapeString(tr(r, "preferences.analytics_cookies")), template.HTMLEscapeString(tr(r, "preferences.save")), template.HTMLEscapeString(tr(r, "preferences.cancel")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-collection"><h2 id="privacy-collection">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.collection_title")), template.HTMLEscapeString(tr(r, "privacy.collection_body")))
	usageKey := "privacy.usage_not_sold"
	if privacy.Data.Sold {
		usageKey = "privacy.usage_if_sold"
	}
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-usage"><h2 id="privacy-usage">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.usage_title")), template.HTMLEscapeString(tr(r, usageKey)))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-security"><h2 id="privacy-security">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.security_title")), template.HTMLEscapeString(tr(r, "privacy.security_body")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-storage"><h2 id="privacy-storage">%s</h2>`, template.HTMLEscapeString(tr(r, "privacy.storage_title")))
	sharing := privacySharing(privacy, config.Get().Server.Tracking)
	if len(sharing) == 0 {
		fmt.Fprintf(&body, `<p>%s</p>`, template.HTMLEscapeString(tr(r, "privacy.third_party_none")))
	} else {
		body.WriteString(`<ul>`)
		for _, item := range sharing {
			fmt.Fprintf(&body, `<li><strong>%s</strong>: %s — %s</li>`, template.HTMLEscapeString(item.Condition), template.HTMLEscapeString(item.When), template.HTMLEscapeString(item.Data))
		}
		body.WriteString(`</ul>`)
	}
	body.WriteString(`</section>`)
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-retention"><h2 id="privacy-retention">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.retention_title")), template.HTMLEscapeString(privacy.Retention.PeriodForLanguage(languageCode(r))))
	thirdPartyServices := privacyThirdPartyServices(privacy, config.Get().Server.Tracking)
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-third-party"><h2 id="privacy-third-party">%s</h2>`, template.HTMLEscapeString(tr(r, "privacy.third_party_title")))
	if len(thirdPartyServices) == 0 {
		fmt.Fprintf(&body, `<p>%s</p>`, template.HTMLEscapeString(tr(r, "privacy.third_party_none")))
	} else {
		fmt.Fprintf(&body, `<div class="table-wrapper"><table class="data-table"><caption>%s</caption><thead><tr><th scope="col">%s</th><th scope="col">%s</th><th scope="col">%s</th><th scope="col">%s</th></tr></thead><tbody>`, template.HTMLEscapeString(tr(r, "privacy.third_party_title")), template.HTMLEscapeString(tr(r, "privacy.service")), template.HTMLEscapeString(tr(r, "privacy.purpose")), template.HTMLEscapeString(tr(r, "privacy.data_sent")), template.HTMLEscapeString(tr(r, "privacy.policy_link")))
		for _, service := range thirdPartyServices {
			fmt.Fprintf(&body, `<tr><td>%s</td><td>%s</td><td>%s</td><td>`, template.HTMLEscapeString(service.Name), template.HTMLEscapeString(service.Purpose), template.HTMLEscapeString(service.DataSent))
			if policyURL, err := url.Parse(service.PolicyURL); err == nil && (policyURL.Scheme == "https" || policyURL.Scheme == "http") && policyURL.Host != "" {
				if requestUsesTorAddress(r) {
					body.WriteString(template.HTMLEscapeString(policyURL.String()))
				} else {
					fmt.Fprintf(&body, `<a href="%s">%s</a>`, template.HTMLEscapeString(policyURL.String()), template.HTMLEscapeString(tr(r, "privacy.policy_link")))
				}
			}
			body.WriteString(`</td></tr>`)
		}
		body.WriteString(`</tbody></table></div>`)
	}
	body.WriteString(`</section>`)
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-rights"><h2 id="privacy-rights">%s</h2><ul><li>%s</li>`, template.HTMLEscapeString(tr(r, "privacy.rights_title")), template.HTMLEscapeString(tr(r, "privacy.access_right")))
	if privacy.Retention.ExportAvailable {
		fmt.Fprintf(&body, `<li>%s</li>`, template.HTMLEscapeString(tr(r, "privacy.export_right")))
	}
	if privacy.Retention.DeletionAvailable {
		fmt.Fprintf(&body, `<li>%s</li>`, template.HTMLEscapeString(tr(r, "privacy.deletion_right")))
	}
	body.WriteString(`</ul></section>`)
	if privacy.Data.Sold {
		csrf := csrfTokenForPage(w, r)
		fmt.Fprintf(&body, `<section class="section-card" id="ccpa-opt-out" aria-labelledby="privacy-ccpa"><h2 id="privacy-ccpa">%s</h2><p>%s</p>`, template.HTMLEscapeString(tr(r, "privacy.ccpa_title")), template.HTMLEscapeString(tr(r, "privacy.ccpa_body")))
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			fmt.Fprintf(&body, `<p role="status">%s</p><form method="post" action="/server/ccpa"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="choice" value="opt-in"><button type="submit">%s</button></form>`, template.HTMLEscapeString(tr(r, "privacy.ccpa.opted_out")), template.HTMLEscapeString(csrf), template.HTMLEscapeString(tr(r, "privacy.ccpa.opt_in")))
		} else {
			fmt.Fprintf(&body, `<form method="post" action="/server/ccpa"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="choice" value="opt-out"><button type="submit">%s</button></form>`, template.HTMLEscapeString(csrf), template.HTMLEscapeString(tr(r, "preferences.ccpa_optout")))
		}
		body.WriteString(`</section>`)
	}
	body.WriteString(string(operatorCard(r, "privacy", "operator-privacy")))
	dpo := config.Get().Server.Contact.DPO
	contact := `<a href="/server/security">` + template.HTMLEscapeString(tr(r, "security.title")) + `</a>`
	if dpo.Email != "" {
		contact = `<a href="mailto:` + template.HTMLEscapeString(dpo.Email) + `">` + template.HTMLEscapeString(dpo.Email) + `</a>`
	}
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="privacy-contact"><h2 id="privacy-contact">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.contact_title")), contact)
	page(w, r, tr(r, "privacy.title"), template.HTML(body.String()))
}

func ccpaOptOutPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/ccpa" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodPost) {
		return
	}
	if !config.Get().Server.Privacy.Data.Sold {
		http.NotFound(w, r)
		return
	}
	if !validCSRF(r) {
		writeHTTPError(w, r, "CSRF token validation failed", http.StatusForbidden)
		return
	}
	value := ""
	maxAge := -1
	switch r.FormValue("choice") {
	case "opt-out":
		value = "true"
		maxAge = preferenceCookieAge
	case "opt-in":
	default:
		writeHTTPError(w, r, "Invalid privacy choice", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: value, Path: "/", MaxAge: maxAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/server/privacy#ccpa-opt-out", http.StatusSeeOther)
}

func termsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/terms" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	if content := operatorCard(r, "terms", "operator-terms"); content != "" {
		page(w, r, tr(r, "terms.title"), content)
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="terms-acceptance"><h2 id="terms-acceptance">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "terms.acceptance")), template.HTMLEscapeString(tr(r, "terms.acceptance_body")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="terms-use"><h2 id="terms-use">%s</h2><p>%s</p><p><a href="/server/security/policy">%s</a></p></section>`, template.HTMLEscapeString(tr(r, "terms.acceptable_use")), template.HTMLEscapeString(tr(r, "terms.acceptable_use_body")), template.HTMLEscapeString(tr(r, "security.policy")))
	fmt.Fprintf(&body, `<section class="section-card" aria-labelledby="terms-liability"><h2 id="terms-liability">%s</h2><p>%s</p></section><section class="section-card" aria-labelledby="terms-changes"><h2 id="terms-changes">%s</h2><p>%s</p></section><section class="section-card" aria-labelledby="terms-law"><h2 id="terms-law">%s</h2><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "terms.liability")), template.HTMLEscapeString(tr(r, "terms.liability_body")), template.HTMLEscapeString(tr(r, "terms.changes")), template.HTMLEscapeString(tr(r, "terms.changes_body")), template.HTMLEscapeString(tr(r, "terms.governing_law")), template.HTMLEscapeString(tr(r, "terms.governing_law_body")))
	page(w, r, tr(r, "terms.title"), template.HTML(body.String()))
}

func preferencesPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !validCSRF(r) {
			writeHTTPError(w, r, "CSRF token validation failed", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/server/preferences", http.StatusSeeOther)
			return
		}
		if r.FormValue("action") == "reset" {
			for _, name := range []string{"theme", "lang", "cookie_consent", "ccpa_opt_out", "gitmessages_build"} {
				http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
			}
			http.SetCookie(w, &http.Cookie{Name: tokenFlashCookieName, Value: "", Path: "/server/preferences/tokens", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteStrictMode, Secure: requestIsSecure(r)})
			w.Header().Set("Clear-Site-Data", `"storage"`)
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/server/preferences", http.StatusSeeOther)
			return
		}
		if privacySignalFromRequest(r) == "" && r.Form.Get("lang") == "auto" && preferenceCategoryAllowed(r) {
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
		}
		if r.Form.Get("consent_form") == "1" {
			preferences := r.Form.Get("consent_preferences") == "on" && config.Get().Server.Privacy.Cookies.Preferences.Enabled
			analytics := r.Form.Get("consent_analytics") == "on" && config.Get().Server.Privacy.Cookies.Analytics.Enabled
			if privacySignalFromRequest(r) != "" {
				preferences, analytics = false, false
			}
			consentJSON := fmt.Sprintf(`{"essential":true,"preferences":%t,"analytics":%t,"timestamp":%d}`, preferences, analytics, time.Now().Unix())
			consent := url.PathEscape(consentJSON)
			previousConsent := readCookieConsent(r)
			if hasConsentCookie(r) && (previousConsent.Preferences && !preferences || previousConsent.Analytics && !analytics) {
				w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
			}
			http.SetCookie(w, &http.Cookie{Name: "cookie_consent", Value: consent, Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
			r.AddCookie(&http.Cookie{Name: "cookie_consent", Value: consent})
			recordConsentUpdated(r.Context(), r, "preferences", preferences)
			recordConsentUpdated(r.Context(), r, "analytics", analytics)
			if config.Get().Server.Privacy.Data.Sold && privacySignalFromRequest(r) == "" {
				if r.Form.Get("ccpa_opt_out") == "on" {
					http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "true", Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
				} else {
					http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
				}
			}
		}
		_, _, consentRequired := applyPreferenceValues(w, r, r.Form)
		w.Header().Set("Cache-Control", "no-store")
		redirect := preferenceRedirect(r, "/server/preferences")
		if consentRequired {
			redirect = addQueryParam(redirect, "consent_required", "1")
		}
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return
	}
	var body strings.Builder
	csrf := ensureCSRFCookie(w, r)
	if signal := privacySignalFromRequest(r); signal != "" {
		fmt.Fprintf(&body, `<section class="section-card" role="status"><p>%s</p></section>`, template.HTMLEscapeString(tr(r, "privacy.signal."+signal)))
	}
	fmt.Fprintf(&body, `<form method="post" action="/server/preferences"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="consent_form" value="1"><section class="section-card" id="theme" aria-labelledby="theme-title"><h2 id="theme-title">%s</h2><label for="preference-theme">%s</label><select id="preference-theme" name="theme">`, csrf, tr(r, "nav.theme"), tr(r, "nav.theme"))
	for _, option := range []struct{ value, key string }{{"auto", "theme.auto"}, {"light", "theme.light"}, {"dark", "theme.dark"}} {
		selected := ""
		if selectedTheme(r) == option.value {
			selected = " selected"
		}
		fmt.Fprintf(&body, `<option value="%s"%s>%s</option>`, option.value, selected, tr(r, option.key))
	}
	consent := readCookieConsent(r)
	if privacySignalFromRequest(r) != "" {
		consent.Preferences, consent.Analytics = false, false
	}
	preferenceChecked, analyticsChecked := "", ""
	if consent.Preferences {
		preferenceChecked = " checked"
	}
	if consent.Analytics {
		analyticsChecked = " checked"
	}
	fmt.Fprintf(&body, `</select></section><section class="section-card" id="language" aria-labelledby="language-title"><h2 id="language-title">%s</h2><label for="preference-language">%s</label><select id="preference-language" name="lang">%s</select></section><section class="section-card" aria-labelledby="privacy-preferences"><h2 id="privacy-preferences">%s</h2><label><input type="checkbox" checked disabled> %s</label><label><input type="checkbox" name="consent_preferences"%s> %s</label><label><input type="checkbox" name="consent_analytics"%s> %s</label>`, tr(r, "language.select"), tr(r, "language.select"), preferenceLanguageOptions(r, languageCode(r)), tr(r, "preferences.privacy"), tr(r, "preferences.essential_cookies"), preferenceChecked, tr(r, "preferences.preference_cookies"), analyticsChecked, tr(r, "preferences.analytics_cookies"))
	if config.Get().Server.Privacy.Data.Sold {
		ccpaChecked := ""
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			ccpaChecked = " checked"
		}
		fmt.Fprintf(&body, `<label><input type="checkbox" name="ccpa_opt_out"%s> %s</label>`, ccpaChecked, tr(r, "preferences.ccpa_optout"))
	}
	cancelTarget := template.HTMLEscapeString(preferenceRedirect(r, "/"))
	fmt.Fprintf(&body, `</section><div class="form-actions"><a class="btn btn-secondary" href="%s">%s</a><button class="btn btn-secondary" type="button" popovertarget="reset-preferences">%s</button><button class="btn btn-primary" type="submit">%s</button></div><div id="reset-preferences" popover><p>%s</p><button type="button" popovertarget="reset-preferences" popovertargetaction="hide">%s</button><button type="submit" name="action" value="reset">%s</button></div></form><section class="section-card" aria-labelledby="more-preferences"><h2 id="more-preferences">%s</h2><ul class="hub-list">%s</ul></section>`, cancelTarget, tr(r, "preferences.cancel"), tr(r, "preferences.reset"), tr(r, "preferences.save"), tr(r, "preferences.erase_warning"), tr(r, "preferences.keep"), tr(r, "preferences.erase_everything"), tr(r, "preferences.more"), serverHubList(r, "/server/preferences"))
	page(w, r, tr(r, "preferences.title"), template.HTML(body.String()))
}

type cookieConsentState struct {
	Essential   bool  `json:"essential"`
	Preferences bool  `json:"preferences"`
	Analytics   bool  `json:"analytics"`
	Timestamp   int64 `json:"timestamp"`
}

func readCookieConsent(r *http.Request) cookieConsentState {
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return cookieConsentState{Essential: true}
	}
	var state cookieConsentState
	value, decodeErr := url.PathUnescape(cookie.Value)
	if decodeErr != nil || json.Unmarshal([]byte(value), &state) != nil {
		return cookieConsentState{Essential: true}
	}
	state.Essential = true
	return state
}

func preferenceLanguageOptions(r *http.Request, current string) string {
	var options strings.Builder
	autoSelected := ""
	cookie, cookieErr := r.Cookie("lang")
	explicit := privacySignalFromRequest(r) == "" && cookieErr == nil && i18n.IsSupported(cookie.Value) || i18n.IsSupported(r.URL.Query().Get("lang"))
	if !explicit {
		autoSelected = " selected"
	}
	autoLabel := strings.ReplaceAll(tr(r, "language.automatic"), "{language}", i18n.Get(current).Meta.NativeName)
	fmt.Fprintf(&options, `<option value="auto" lang="%s"%s>%s</option>`, current, autoSelected, template.HTMLEscapeString(autoLabel))
	for _, code := range i18n.Supported() {
		locale := i18n.Get(code)
		selected := ""
		if code == current {
			selected = " selected"
		}
		fmt.Fprintf(&options, `<option value="%s" lang="%s"%s>%s</option>`, code, code, selected, template.HTMLEscapeString(locale.Meta.NativeName))
	}
	return options.String()
}

func healthPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/healthz" && (r.URL.Path != "/healthz" || !config.Get().Server.Healthz.Root.Enabled) {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	health, statusCode := buildHealthResponse()
	if wantsFrontendText(r) {
		writeText(w, statusCode, humanHealthText(r, health))
		return
	}
	if statusCode != http.StatusOK {
		w.WriteHeader(statusCode)
	}
	page(w, r, tr(r, "health.title"), template.HTML(healthHTMLContent(r, health)))
}

func isTopLevelHTMLNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	_, destinationSet := r.Header["Sec-Fetch-Dest"]
	_, modeSet := r.Header["Sec-Fetch-Mode"]
	if !destinationSet && !modeSet {
		return true
	}
	return strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document") && strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate")
}

func offlinePage(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	content := `<section class="section-card" aria-labelledby="offline-title"><h2 id="offline-title">` + template.HTMLEscapeString(tr(r, "common.offline")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "common.offline_body")) + `</p><p><a href="` + template.HTMLEscapeString(BuildURL(r, "/")) + `">` + template.HTMLEscapeString(tr(r, "offline.reload")) + `</a></p></section>`
	page(w, r, tr(r, "common.offline"), template.HTML(content))
}

func manifest(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, false) {
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	basePath := requestBasePath(r)
	stamp := url.QueryEscape(assetStamp())
	icon := func(name string) string { return basePath + "/static/icons/" + name + "?v=" + stamp }
	icons := make([]map[string]string, 0, len(pwaIconSizes))
	for _, size := range []int{16, 32, 72, 96, 128, 144, 152, 180, 192, 384, 512} {
		name := "icon-" + strconv.Itoa(size) + ".png"
		if size == 16 || size == 32 {
			name = "favicon-" + strconv.Itoa(size) + ".png"
		}
		icons = append(icons, map[string]string{"src": icon(name), "sizes": strconv.Itoa(size) + "x" + strconv.Itoa(size), "type": "image/png"})
	}
	icons = append(icons,
		map[string]string{"src": icon("icon-maskable-192.png"), "sizes": "192x192", "type": "image/png", "purpose": "maskable"},
		map[string]string{"src": icon("icon-maskable-512.png"), "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
	)
	manifestData := map[string]any{
		"name": seoBrandingTitle(), "short_name": seoBrandingTitle(), "description": seoDescription(r),
		"start_url": basePath + "/?source=pwa", "scope": basePath + "/", "display": "standalone", "orientation": "any", "categories": []string{"utilities"},
		"background_color": "#282a36", "theme_color": "#bd93f9",
		"icons": icons,
	}
	encoded, err := json.Marshal(manifestData)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	_, _ = w.Write(append(encoded, '\n'))
}

func icon(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = fmt.Fprintln(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#282a36"/><path fill="#bd93f9" d="M104 128h304v48H104zm0 104h240v48H104zm0 104h192v48H104z"/></svg>`)
}

func styles(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	content, err := webAssets.ReadFile("assets/styles.css")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(content)
}

func stylesheetAsset(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/static/css/")
	if name != "common.css" && name != "components.css" && name != "public.css" {
		http.NotFound(w, r)
		return
	}
	content, err := documentationAssets.ReadFile("static/css/" + name)
	if err == nil && name == "components.css" {
		settings := config.Get().Server.Notifications.WebUI
		stamp := fmt.Sprintf("%s-%d-%d", assetStamp(), settings.Duration, settings.WarningDuration)
		if !setAssetHeaders(w, r, stamp, true) {
			return
		}
		content = bytes.ReplaceAll(content, []byte("--toast-duration: 3s"), []byte(fmt.Sprintf("--toast-duration: %ds", settings.Duration)))
		content = bytes.ReplaceAll(content, []byte("--toast-duration-warning: 5s"), []byte(fmt.Sprintf("--toast-duration-warning: %ds", settings.WarningDuration)))
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(content)
		return
	}
	serveEmbeddedAsset(w, r, content, err, "text/css; charset=utf-8")
}

func serveEmbeddedAsset(w http.ResponseWriter, r *http.Request, content []byte, err error, contentType string) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	if err != nil {
		writeError(w, r, http.StatusNotFound, "ASSET_NOT_FOUND", tr(r, "errors.not_found"))
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(content)
}

func swaggerStaticAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/vendor/swagger-ui/")
	if name == "theme.css" {
		content, err := swaggerapi.ThemeCSS()
		serveEmbeddedAsset(w, r, content, err, "text/css; charset=utf-8")
		return
	}
	content, err := documentationAssets.ReadFile("static/vendor/swagger-ui/" + name)
	contentType := "application/javascript; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css; charset=utf-8"
	} else if strings.HasSuffix(name, ".md") {
		contentType = "text/markdown; charset=utf-8"
	}
	serveEmbeddedAsset(w, r, content, err, contentType)
}

func graphQLStaticAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/vendor/graphiql/")
	if name == "theme.css" {
		content, err := graphqlapi.ThemeCSS()
		serveEmbeddedAsset(w, r, content, err, "text/css; charset=utf-8")
		return
	}
	content, err := documentationAssets.ReadFile("static/vendor/graphiql/" + name)
	contentType := "application/javascript; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css; charset=utf-8"
	} else if strings.HasSuffix(name, ".md") {
		contentType = "text/markdown; charset=utf-8"
	}
	serveEmbeddedAsset(w, r, content, err, contentType)
}

func documentationScript(w http.ResponseWriter, r *http.Request) {
	var content []byte
	var err error
	switch r.URL.Path {
	case "/static/js/docs-swagger.js":
		content, err = documentationAssets.ReadFile("static/js/docs-swagger.js")
	case "/static/js/docs-graphql.js":
		content, err = documentationAssets.ReadFile("static/js/docs-graphql.js")
	case "/static/js/graphiql-compat.js":
		content, err = documentationAssets.ReadFile("static/js/graphiql-compat.js")
	case "/static/js/app.js":
		content, err = documentationAssets.ReadFile("static/js/app.js")
	default:
		http.NotFound(w, r)
		return
	}
	serveEmbeddedAsset(w, r, content, err, "application/javascript; charset=utf-8")
}

func localeJSON(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	code := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/locales/"), ".json")
	if !strings.HasSuffix(r.URL.Path, ".json") || strings.Contains(code, "/") {
		writeError(w, r, http.StatusNotFound, "LOCALE_NOT_FOUND", tr(r, "api.locale_not_found"))
		return
	}
	data, ok := i18n.JSON(code)
	if !ok {
		writeError(w, r, http.StatusNotFound, "LOCALE_NOT_FOUND", tr(r, "api.locale_not_found"))
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func serviceWorker(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, false) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	offlineTitle := template.HTMLEscapeString(tr(r, "common.offline"))
	offlineBody := template.HTMLEscapeString(tr(r, "common.offline_body"))
	offlineReload := template.HTMLEscapeString(tr(r, "offline.reload"))
	offlineURL := template.HTMLEscapeString(BuildURL(r, "/"))
	notificationSettings := config.Get().Server.Notifications.WebUI
	componentStamp := fmt.Sprintf("%s-%d-%d", assetStamp(), notificationSettings.Duration, notificationSettings.WarningDuration)
	offlineHTML, _ := json.Marshal(fmt.Sprintf(`<!doctype html><html lang="%s"><title>%s</title><main><h1>%s</h1><p>%s</p><p><a href="%s">%s</a></p></main></html>`, template.HTMLEscapeString(languageCode(r)), offlineTitle, offlineTitle, offlineBody, offlineURL, offlineReload))
	_, _ = fmt.Fprintf(w, `const BASE=%q;
const appURL=value=>BASE+value;
const appPath=value=>value===BASE||value.startsWith(BASE+'/')?(value.slice(BASE.length)||'/'):null;
const CACHE_PREFIX='gitmessages-cache-';
const CACHE_NAME=CACHE_PREFIX+%q;
const MAX_CACHE_BYTES=50*1024*1024;
const MAX_AGE_MS=7*24*60*60*1000;
const NAV_TIMEOUT_MS=8000;
const PRECACHE=[appURL('/static/css/common.css?v=%s'),appURL('/static/css/components.css?v=%s'),appURL('/static/css/public.css?v=%s'),appURL('/styles.css?v=%s'),appURL('/static/js/app.js?v=%s'),appURL('/manifest.json?v=%s'),appURL('/icon.svg?v=%s'),appURL('/static/icons/icon-192.png?v=%s'),appURL('/static/icons/icon-512.png?v=%s'),appURL('/static/vendor/swagger-ui/swagger-ui-bundle.js?v=%s'),appURL('/static/vendor/swagger-ui/swagger-ui-standalone-preset.js?v=%s'),appURL('/static/vendor/swagger-ui/swagger-ui.css?v=%s'),appURL('/static/vendor/swagger-ui/theme.css?v=%s'),appURL('/static/js/docs-swagger.js?v=%s'),appURL('/static/vendor/graphiql/react.production.min.js?v=%s'),appURL('/static/vendor/graphiql/react-dom.production.min.js?v=%s'),appURL('/static/js/graphiql-compat.js?v=%s'),appURL('/static/vendor/graphiql/graphiql.min.js?v=%s'),appURL('/static/vendor/graphiql/graphiql.css?v=%s'),appURL('/static/vendor/graphiql/theme.css?v=%s'),appURL('/static/js/docs-graphql.js?v=%s'),appURL('/offline.html')];
const PUBLIC_PAGES=new Set(['/','/types','/templates','/server/about','/server/help','/server/help/mcp','/server/privacy','/server/terms','/server/security','/offline.html']);
const STATIC_PATH=path=>path==='/styles.css'||path.startsWith('/static/css/')||path==='/static/js/app.js'||path==='/manifest.json'||path==='/icon.svg'||path.startsWith('/locales/')||path.startsWith('/static/icons/')||path.startsWith('/static/splash/')||path.startsWith('/static/js/')||path.startsWith('/static/vendor/');

async function cacheResponse(request,response){
  if(!response.ok||response.redirected||response.type==='opaque')return;
  const body=await response.clone().arrayBuffer();
  if(body.byteLength>MAX_CACHE_BYTES)return;
  const storage=await self.navigator.storage?.estimate?.().catch(()=>null);
  if(storage&&storage.quota&&storage.usage+body.byteLength>storage.quota*0.9)return;
  const cache=await caches.open(CACHE_NAME);
  const entries=await cache.keys();
  const cached=[];
  let total=0;
  for(const key of entries){
    const value=await cache.match(key);
    const age=Number(value?.headers.get('X-GitMessages-Cached-At')||0);
    if(!value||!age||Date.now()-age>MAX_AGE_MS){await cache.delete(key);continue;}
    const size=Number(value.headers.get('Content-Length')||0);
    total+=size;
    cached.push({key,age,size});
  }
  cached.sort((left,right)=>left.age-right.age);
  while(total+body.byteLength>MAX_CACHE_BYTES&&cached.length){
    const oldest=cached.shift();
    total-=oldest.size;
    await cache.delete(oldest.key);
  }
  const headers=new Headers(response.headers);
  headers.set('X-GitMessages-Cached-At',String(Date.now()));
  headers.set('Content-Length',String(body.byteLength));
  await cache.put(request,new Response(body,{status:response.status,statusText:response.statusText,headers}));
}

async function cachedResponse(request){
  const response=await caches.match(request)||await caches.match(new URL(request.url).pathname);
  if(!response)return null;
  const age=Number(response.headers.get('X-GitMessages-Cached-At')||0);
  if(!age||Date.now()-age>MAX_AGE_MS){await caches.delete(request);return null;}
  return response;
}

function withTimeout(promise,milliseconds){
  return new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error('navigation timeout')),milliseconds);
    promise.then(value=>{clearTimeout(timer);resolve(value);},error=>{clearTimeout(timer);reject(error);});
  });
}

function offlineFallback(){
  return new Response(%s,{status:503,statusText:'Service Unavailable',headers:{'Content-Type':'text/html; charset=utf-8'}});
}

self.addEventListener('install',event=>event.waitUntil((async()=>{
  const cache=await caches.open(CACHE_NAME);
  for(const path of PRECACHE){
    try{
      const request=new Request(path,{cache:'reload'});
      const response=await fetch(request);
      if(response.ok&&!response.redirected)await cacheResponse(request,response);
    }catch(error){}
  }
  if(!self.registration.active)await self.skipWaiting();
})()));

self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith(CACHE_PREFIX)&&key!==CACHE_NAME).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('message',event=>{if(event.data?.type==='SKIP_WAITING')self.skipWaiting();});

self.addEventListener('fetch',event=>{
  const request=event.request;
  const url=new URL(request.url);
  const pathname=appPath(url.pathname);
  if(request.method!=='GET'||url.origin!==self.location.origin||!pathname||pathname.startsWith('/api/'))return;
  if(request.mode==='navigate'){
    event.respondWith(withTimeout(fetch(request),NAV_TIMEOUT_MS).then(async response=>{
      if(response.redirected)throw new Error('redirected navigation');
      if(response.ok&&PUBLIC_PAGES.has(pathname))await cacheResponse(request,response.clone());
      return response;
    }).catch(async()=>await cachedResponse(request)||offlineFallback()).catch(()=>offlineFallback()));
    return;
  }
  if(STATIC_PATH(pathname)){
    event.respondWith((async()=>{
      try{
        const cached=await cachedResponse(request);
        if(cached)return cached;
        const response=await fetch(request);
        if(response.ok&&!response.redirected)await cacheResponse(request,response.clone());
        return response;
      }catch(error){return new Response('',{status:504,statusText:'Gateway Timeout'});}
    })());
  }
	});`, requestBasePath(r), assetStamp(), url.QueryEscape(assetStamp()), url.QueryEscape(componentStamp), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), string(offlineHTML))
}

func swaggerJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	document, err := swaggerapi.Load(languageCode(r))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "OPENAPI_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "OPENAPI_INVALID", tr(r, "api.encode_failed"))
		return
	}
	for path, operations := range paths {
		if path == "/api/v1" || strings.HasPrefix(path, "/api/v1/") {
			delete(paths, path)
			rebased := APIBasePath() + strings.TrimPrefix(path, "/api/v1")
			paths[rebased] = operations
		}
	}
	info, _ := document["info"].(map[string]any)
	info["title"] = seoBrandingTitle() + " API"
	info["version"] = serverVersion
	document["servers"] = []map[string]string{{"url": BuildURL(r, "")}}
	metricsConfig := config.Get().Server.Metrics
	for path, rawOperations := range paths {
		if !strings.Contains(path, "metrics") {
			continue
		}
		operations, _ := rawOperations.(map[string]any)
		for _, raw := range operations {
			operation, _ := raw.(map[string]any)
			if metricsConfig.Auth.AllowUnauthenticated {
				delete(operation, "security")
			} else {
				operation["security"] = []map[string][]string{{"BearerAuth": {}}}
			}
		}
	}
	document = swaggerapi.FilterMounted(document, map[string]bool{
		"server.metrics.enabled":      metricsConfig.Enabled,
		"server.metrics.root.enabled": metricsConfig.Enabled && metricsConfig.Root.Enabled,
		"debug_enabled":               mode.IsDebugEnabled(),
	})
	writeJSON(w, r, http.StatusOK, document)
}

// BuildOpenAPISuperset produces the full localized route document for build-time embedding.
func apiOperationResponseModel(id string) any {
	switch id {
	case "getApiIndex":
		return map[string]any{"name": "", "version": "", "resources": []string{}, "server": []string{}}
	case "getApiAutodiscover":
		return map[string]any{"name": "", "version": "", "api": map[string]string{"current_version": ""}, "urls": map[string]string{}, "cli_versions": []string{}, "cli_min_version": ""}
	case "listCommitTypes":
		return []messages.CommitType{}
	case "listConventions":
		return []messages.Convention{}
	case "listTemplates":
		return []messages.Template{}
	case "getHealth", "getApiHealthz":
		return healthResponse{}
	case "buildCommitMessage":
		return map[string]string{"message": ""}
	case "getRandomTemplate":
		return messages.Template{}
	case "getRandomExample":
		return map[string]string{"type": "", "message": ""}
	case "getAbout", "getTerms":
		return map[string]string{"title": "", "content": "", "format": ""}
	case "getHelp":
		return map[string]any{"title": "", "content": "", "format": "", "pages": []map[string]string{{"title": "", "url": ""}}}
	case "getMcpHelp":
		return map[string]any{"enabled": false, "url": "", "aliases": []string{}, "auth": map[string]string{"scheme": ""}, "tools": []map[string]string{{"name": "", "description": ""}}, "project": ""}
	case "getPrivacy":
		return map[string]any{"summary": map[string]any{"data_stored_on_server": false, "data_sold": false, "user_control": false}, "cookies": map[string]any{"essential": map[string]any{"enabled": false, "description": ""}, "preferences": map[string]any{"enabled": false, "description": ""}, "analytics": map[string]any{"enabled": false, "description": ""}}, "data": map[string]any{"sold": false, "stored_on_server": false, "sharing": []map[string]string{{"condition": "", "when": "", "data": ""}}}, "tracking": map[string]any{"enabled": false, "type": "", "type_name": ""}, "retention": map[string]any{"period": "", "export_available": false, "deletion_available": false}, "third_party": map[string]any{"services": []map[string]string{{"name": "", "purpose": "", "data_sent": "", "policy_url": ""}}}, "ccpa": map[string]any{"applicable": false, "opt_out_url": "", "user_opted_out": false}, "content": map[string]string{"consent_message": "", "data_collection": "", "data_usage": "", "data_security": ""}}
	case "getSecurity":
		return map[string]any{"contacts": []map[string]string{{"type": "", "value": ""}}, "expires": "", "pgp": map[string]any{"available": false, "url": "", "fingerprint": ""}, "policy_url": "", "thanks_url": "", "report_url": ""}
	case "getSecurityPolicy":
		return map[string]string{"policy": ""}
	case "getSecurityThanks":
		return map[string]any{"entries": []thanksEntry{}}
	case "getDataProtectionContact":
		return map[string]string{"name": "", "email": "", "privacy_url": ""}
	case "getContactForm":
		return map[string]any{"fields": []map[string]any{{"name": "", "label": "", "type": "", "required": false}}, "captcha": map[string]any{"challenge": "", "expires_at": ""}, "form_token": "", "types": []string{}, "security_url": "", "abuse_email": ""}
	case "submitContactReport":
		return map[string]any{"message": "", "review": map[string]string{"type": "", "email": "", "subject": ""}, "review_token": "", "confirm_after_seconds": 0, "tracking_id": ""}
	case "getPreferences", "savePreferences":
		return map[string]any{"lang": "", "theme": "", "prefs": map[string]string{}, "applied": []string{}, "ignored": []string{}, "consent_required": false}
	case "getPreferencesSync":
		return map[string]any{"theme": "", "lang": "", "prefs": map[string]string{}, "url": "", "code": ""}
	case "importPreferencesSync":
		return map[string]any{"theme": "", "lang": "", "prefs": map[string]string{}, "applied": []string{}, "ignored": []string{}, "consent_required": false}
	case "getOpenAPI", "getApiSwagger":
		return map[string]any{"openapi": "", "info": map[string]string{}, "servers": []map[string]string{}, "components": map[string]any{}, "paths": map[string]any{}}
	case "graphqlQuery", "graphqlQueryApiGraphql":
		return map[string]any{"data": map[string]any{}, "errors": []map[string]string{{"message": ""}}}
	case "listOwnerTokens":
		token := map[string]any{"id": "", "name": "", "kind": "", "token_masked": "", "created_at": "", "last_used_at": nil, "expires_at": nil, "status": ""}
		return map[string]any{"primary": token, "linked": []map[string]any{token}, "max_linked": 0}
	case "createLinkedToken", "revealLinkedToken", "rotateLinkedToken":
		return map[string]any{"token": map[string]any{"id": "", "name": "", "kind": "", "token_masked": "", "created_at": "", "last_used_at": nil, "expires_at": nil, "status": ""}, "linked_token": ""}
	case "issuePrimaryToken", "revealPrimaryToken", "rotatePrimaryToken":
		return map[string]string{"owner_token": ""}
	case "revokePrimaryToken":
		return map[string]int64{"tokens": 0, "resources": 0}
	case "updateLinkedToken":
		return map[string]any{"id": "", "name": "", "kind": "", "token_masked": "", "created_at": "", "last_used_at": nil, "expires_at": nil, "status": ""}
	case "revokeLinkedToken":
		return map[string]any{"id": "", "status": ""}
	default:
		return map[string]any{}
	}
}

func apiOperationBareResponse(id string) bool {
	switch id {
	case "getHealth", "getApiHealthz", "getOpenAPI", "getApiSwagger", "graphqlQuery", "graphqlQueryApiGraphql", "mcpEndpoint", "mcpEndpointApiMcp", "mcpEndpointMcp":
		return true
	default:
		return false
	}
}

func BuildOpenAPISuperset(r *http.Request) map[string]any {
	response := func(description string) map[string]any {
		return map[string]any{"200": map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": swaggerapi.SchemaFromModel(apiOperationResponseModel("graphqlQuery"))}}}}
	}
	mcpRequestSchema := map[string]any{
		"type":     "object",
		"required": []string{"jsonrpc", "id", "method", "params"},
		"properties": map[string]any{
			"jsonrpc": map[string]any{"type": "string", "enum": []string{"2.0"}},
			"id":      map[string]any{"oneOf": []map[string]string{{"type": "string"}, {"type": "number"}}},
			"method":  map[string]any{"type": "string"},
			"params": map[string]any{"type": "object", "properties": map[string]any{
				"_meta": map[string]any{"type": "object", "required": []string{"io.modelcontextprotocol/protocolVersion"}, "properties": map[string]any{
					"io.modelcontextprotocol/protocolVersion": map[string]any{"type": "string"},
				}},
			}},
		},
	}
	mcpOperation := map[string]any{"post": map[string]any{
		"operationId": "mcpEndpoint",
		"summary":     tr(r, "mcp.title"),
		"description": tr(r, "mcp.help.description"),
		"parameters": []map[string]any{
			{"name": "Accept", "in": "header", "required": true, "schema": map[string]any{"type": "string", "example": "application/json, text/event-stream"}},
			{"name": "MCP-Protocol-Version", "in": "header", "required": true, "schema": map[string]any{"type": "string", "enum": []string{mcpProtocolVersion}}},
			{"name": "Mcp-Method", "in": "header", "required": true, "schema": map[string]any{"type": "string"}},
			{"name": "Mcp-Name", "in": "header", "required": false, "description": "Required for tools/call and must match params.name.", "schema": map[string]any{"type": "string"}},
		},
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": mcpRequestSchema}}},
		"security":    []map[string][]string{{"BearerAuth": {}}},
		"responses": map[string]any{
			"200": map[string]any{"description": tr(r, "mcp.help.description"), "content": map[string]any{"application/json": map[string]any{"schema": swaggerapi.SchemaFromModel(mcpResponse{})}, "text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}},
			"400": map[string]any{"description": "Invalid JSON-RPC request, unsupported version, or header mismatch.", "content": map[string]any{"application/json": map[string]any{"schema": swaggerapi.SchemaFromModel(mcpResponse{})}}},
			"403": map[string]any{"description": "Origin is not allowed."}, "406": map[string]any{"description": "Accept must include application/json and text/event-stream."},
			"413": map[string]any{"description": "Request body exceeds 1 MiB."}, "415": map[string]any{"description": "Content-Type must be application/json."}, "429": map[string]any{"description": tr(r, "api.rate_limited")},
		},
	}}
	apiErrorSchema := map[string]any{"type": "object", "required": []string{"ok", "error", "message"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean", "const": false}, "error": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "details": map[string]any{"type": "object"}}}
	operations := make([]swaggerapi.Operation, 0, len(apiOperationDefinitions(r))+24)
	for _, definition := range apiOperationDefinitions(r) {
		if !definition.Listed || definition.Internal {
			continue
		}
		responseSchema := swaggerapi.SchemaFromModel(definition.Response)
		if !definition.BareResponse {
			responseSchema = map[string]any{"type": "object", "required": []string{"ok", "data"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean", "const": true}, "data": responseSchema}}
		}
		responses := map[string]any{"200": map[string]any{"description": definition.Description, "content": map[string]any{"application/json": map[string]any{"schema": responseSchema}}}}
		responses["default"] = map[string]any{"description": tr(r, "api.error"), "content": map[string]any{"application/json": map[string]any{"schema": apiErrorSchema}}}
		if definition.Method == http.MethodPost {
			responses["400"] = map[string]any{"description": tr(r, "swagger.build_message.error"), "content": map[string]any{"application/json": map[string]any{"schema": apiErrorSchema}}}
		}
		if definition.OperationID == "createLinkedToken" || definition.OperationID == "issuePrimaryToken" {
			responses["201"] = responses["200"]
			delete(responses, "200")
		}
		parameters := make([]swaggerapi.Parameter, 0)
		for _, segment := range strings.Split(definition.Path, "/") {
			if len(segment) > 2 && segment[0] == '{' && segment[len(segment)-1] == '}' {
				parameters = append(parameters, swaggerapi.Parameter{Name: segment[1 : len(segment)-1], Location: "path", Required: true, Schema: map[string]any{"type": "string"}})
			}
		}
		var requestBody map[string]any
		requestSchema := routeRequestSchema(definition.Request)
		if definition.Method != http.MethodGet && requestSchema != nil {
			if properties, _ := requestSchema["properties"].(map[string]any); len(properties) > 0 {
				requestBody = requestSchema
			} else if required, _ := requestSchema["required"].([]string); len(required) > 0 {
				requestBody = requestSchema
			}
		}
		var security []string
		if definition.Auth != "" && definition.Auth != "none" {
			security = []string{"BearerAuth"}
		}
		operations = append(operations, swaggerapi.Operation{
			Path: definition.Path, Method: definition.Method, ID: definition.OperationID, Summary: definition.Summary,
			Description: definition.Description, DebugOnly: definition.DebugOnly, RequestBody: requestBody, Responses: responses,
			Parameters: parameters, Security: security,
		})
	}
	cliBinarySecurity := []string(nil)
	if config.Get().CLI.BinaryDownload.RequireAuth {
		cliBinarySecurity = []string{"BearerAuth"}
	}
	operations = append(operations, swaggerapi.Operation{
		Path: "/cli/binaries/{binary}", Method: http.MethodGet, ID: "downloadCliBinary",
		Summary: tr(r, "api.rest"), Description: tr(r, "api.rest"),
		Parameters: []swaggerapi.Parameter{{Name: "binary", Location: "path", Required: true, Schema: map[string]any{"type": "string"}}},
		Responses:  map[string]any{"200": map[string]any{"description": tr(r, "api.rest"), "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "404": map[string]any{"description": tr(r, "contact.error.invalid")}},
		Security:   cliBinarySecurity,
	})
	additionalPaths := make(map[string]any)
	for _, definition := range apiOperationDefinitions(r) {
		if strings.HasPrefix(definition.OperationID, "mcpEndpoint") {
			additionalPaths[definition.Path] = withOperationID(mcpOperation, "post", definition.OperationID)
		}
	}
	graphqlPost := map[string]any{"operationId": "graphqlQuery", "summary": tr(r, "graphql.title"), "description": tr(r, "graphql.description"), "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": graphqlInputSchema(r)}}}, "responses": response(tr(r, "graphql.description"))}
	for _, definition := range apiOperationDefinitions(r) {
		if definition.Path == "/api/graphql" {
			additionalPaths[definition.Path] = map[string]any{"post": withOperationID(map[string]any{"post": graphqlPost}, "post", definition.OperationID)["post"]}
		}
	}
	document := swaggerapi.Document{
		Title: seoBrandingTitle() + " API", Version: serverVersion, Description: tr(r, "swagger.info.description"), ServerURL: BuildURL(r, ""),
		Operations: operations, AdditionalPaths: additionalPaths,
	}
	return swaggerapi.Build(document)
}

func swaggerOperationID(prefix, path string) string {
	var identifier strings.Builder
	separator := false
	for _, character := range path {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			identifier.WriteRune(character)
			separator = false
		} else if identifier.Len() > 0 && !separator {
			identifier.WriteByte('_')
			separator = true
		}
	}
	parts := strings.FieldsFunc(prefix+"_"+identifier.String(), func(character rune) bool { return character == '_' })
	if len(parts) == 0 {
		return ""
	}
	var operationID strings.Builder
	operationID.WriteString(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		operationID.WriteString(strings.ToUpper(part[:1]))
		operationID.WriteString(part[1:])
	}
	return operationID.String()
}

func withOperationID(paths map[string]any, method, id string) map[string]any {
	result := make(map[string]any, len(paths))
	for path, raw := range paths {
		if path != method {
			result[path] = raw
			continue
		}
		operation, ok := raw.(map[string]any)
		if !ok {
			result[path] = raw
			continue
		}
		copy := make(map[string]any, len(operation)+1)
		for key, value := range operation {
			copy[key] = value
		}
		copy["operationId"] = id
		result[path] = copy
	}
	return result
}

func swaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/docs/swagger" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	jsonURL := APIBasePath() + "/server/swagger"
	stamp := template.HTMLEscapeString(url.QueryEscape(assetStamp()))
	body := `<section class="section-card" aria-labelledby="swagger-explorer"><h2 id="swagger-explorer">` + template.HTMLEscapeString(tr(r, "swagger.interactive")) + `</h2><div id="swagger-ui" data-spec-url="` + template.HTMLEscapeString(jsonURL) + `" aria-live="polite"></div><noscript><p>` + template.HTMLEscapeString(tr(r, "swagger.nojs")) + ` <a href="` + template.HTMLEscapeString(jsonURL) + `">` + template.HTMLEscapeString(tr(r, "api.openapi")) + `</a>.</p></noscript></section><section class="section-card" aria-labelledby="swagger-resources"><h2 id="swagger-resources">` + template.HTMLEscapeString(tr(r, "api.resources")) + `</h2><p><a href="/server/help/mcp">` + template.HTMLEscapeString(tr(r, "mcp.title")) + `</a></p></section><script src="/static/vendor/swagger-ui/swagger-ui-bundle.js?v=` + stamp + `" defer></script><script src="/static/vendor/swagger-ui/swagger-ui-standalone-preset.js?v=` + stamp + `" defer></script><script src="/static/js/docs-swagger.js?v=` + stamp + `" defer></script>`
	page(w, r, tr(r, "api.rest"), template.HTML(body))
}

func graphQLHandler(w http.ResponseWriter, r *http.Request) {
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if r.Method != http.MethodPost || contentType != "application/x-www-form-urlencoded" || !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
		graphqlapi.Handler().ServeHTTP(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeHTTPError(w, r, "Invalid GraphQL form", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]string{"query": r.PostForm.Get("query")})
	if err != nil {
		writeHTTPError(w, r, "Invalid GraphQL form", http.StatusBadRequest)
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.ContentLength = int64(len(payload))
	request.Header = r.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	graphqlapi.Handler().ServeHTTP(response, request)
	body := `<section class="section-card" aria-labelledby="graphql-result"><h2 id="graphql-result">` + template.HTMLEscapeString(tr(r, "graphql.title")) + `</h2><p><a href="/server/docs/graphql">` + template.HTMLEscapeString(tr(r, "graphql.title")) + `</a></p><pre><code>` + template.HTMLEscapeString(response.Body.String()) + `</code></pre></section>`
	page(w, r, tr(r, "graphql.title"), template.HTML(body))
}

func graphQLUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	csrf := ensureCSRFCookie(w, r)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	endpoint := APIBasePath() + "/server/graphql"
	stamp := template.HTMLEscapeString(url.QueryEscape(assetStamp()))
	body := `<section class="section-card graphiql-ui" aria-labelledby="graphql-query-heading"><h2 id="graphql-query-heading">` + template.HTMLEscapeString(tr(r, "graphql.query")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "graphql.description")) + `</p><div id="graphiql" data-endpoint="` + template.HTMLEscapeString(endpoint) + `" aria-live="polite"></div><noscript><p>` + template.HTMLEscapeString(tr(r, "graphql.nojs")) + ` <a href="` + template.HTMLEscapeString(endpoint) + `">` + template.HTMLEscapeString(endpoint) + `</a></p></noscript></section><noscript><section class="section-card" aria-labelledby="graphql-nojs-heading"><h2 id="graphql-nojs-heading">` + template.HTMLEscapeString(tr(r, "graphql.nojs_title")) + `</h2><form method="post" action="` + template.HTMLEscapeString(endpoint) + `"><input type="hidden" name="csrf_token" value="` + template.HTMLEscapeString(csrf) + `"><label for="query">` + template.HTMLEscapeString(tr(r, "graphql.query")) + `</label><textarea id="query" name="query" rows="8">{ types { type description emoji example breaking } }</textarea><button type="submit">` + template.HTMLEscapeString(tr(r, "graphql.run")) + `</button></form></section></noscript><script src="/static/vendor/graphiql/react.production.min.js?v=` + stamp + `" defer></script><script src="/static/vendor/graphiql/react-dom.production.min.js?v=` + stamp + `" defer></script><script src="/static/js/graphiql-compat.js?v=` + stamp + `" defer></script><script src="/static/vendor/graphiql/graphiql.min.js?v=` + stamp + `" defer></script><script src="/static/js/docs-graphql.js?v=` + stamp + `" defer></script>`
	page(w, r, tr(r, "graphql.title"), template.HTML(body))
}
