package web

import (
	"bytes"
	cryptorand "crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	graphqlapi "github.com/apimgr/gitmessages/src/graphql"
	"github.com/apimgr/gitmessages/src/messages"
	"github.com/apimgr/gitmessages/src/metrics"
	htmlnode "golang.org/x/net/html"
	languagetags "golang.org/x/text/language"
)

const apiBase = "/api/v1"

var serverVersion = "dev"
var serverCommit = "unknown"
var overlayStatusMu sync.RWMutex
var overlayStatusProvider func() map[string]any
var corsOriginsMu sync.RWMutex
var corsOrigins = []string{"*"}
var defaultThemeMu sync.RWMutex
var defaultTheme = "dark"

//go:embed assets/*.css
var webAssets embed.FS

// Handler returns the complete public web and API surface.
func Handler(version string, commit ...string) http.Handler {
	if version != "" {
		serverVersion = version
	}
	serverCommit = "unknown"
	if len(commit) > 0 && commit[0] != "" {
		serverCommit = commit[0]
	}
	mux := http.NewServeMux()
	registerTokenRoutes(mux)
	metricsConfig := config.Get().Server.Metrics
	var exporter *metrics.Exporter
	if metricsConfig.Enabled {
		buildDate := "N/A"
		if len(commit) > 1 && commit[1] != "" {
			buildDate = commit[1]
		}
		exporter = metrics.New(metricsConfig, serverVersion, serverCommit, buildDate)
		registerMetricsRoutes(mux, metricsConfig, exporter)
	}
	mux.HandleFunc("/", home)
	mux.HandleFunc("/types", typesPage)
	mux.HandleFunc("/templates", templatesPage)
	mux.HandleFunc("/builder", builderPage)
	mux.HandleFunc("/server/about", aboutPage)
	mux.HandleFunc("/server/help", helpPage)
	mux.HandleFunc("/server/help/mcp", mcpHelpPage)
	mux.HandleFunc("/server/privacy", privacyPage)
	mux.HandleFunc("/server/terms", termsPage)
	mux.HandleFunc("/server/preferences", preferencesPage)
	mux.HandleFunc("/server/preferences/theme", updateThemePreference)
	registerPreferenceSyncRoutes(mux)
	mux.HandleFunc("/server/healthz", healthPage)
	mux.HandleFunc("/manifest.json", manifest)
	mux.HandleFunc("/icon.svg", icon)
	mux.HandleFunc("/sw.js", serviceWorker)
	mux.HandleFunc("/styles.css", styles)
	mux.HandleFunc("/locales/", localeJSON)
	mux.HandleFunc(apiBase, apiRoot)
	mux.HandleFunc(apiBase+".txt", apiTextExtension)
	mux.HandleFunc(apiBase+"/", apiTextExtension)
	mux.HandleFunc(apiBase+"/types", apiTypes)
	mux.HandleFunc(apiBase+"/conventions", apiConventions)
	mux.HandleFunc(apiBase+"/templates", apiTemplates)
	mux.HandleFunc(apiBase+"/templates/random", apiRandomTemplate)
	mux.HandleFunc(apiBase+"/messages", apiMessages)
	mux.HandleFunc(apiBase+"/messages/random", apiRandom)
	mux.HandleFunc(apiBase+"/server/healthz", apiHealth)
	mux.HandleFunc(apiBase+"/server/about", apiAbout)
	mux.HandleFunc(apiBase+"/server/help", apiHelp)
	mux.HandleFunc(apiBase+"/server/help/mcp", apiMCPHelp)
	mux.HandleFunc(apiBase+"/server/privacy", apiPrivacy)
	mux.HandleFunc(apiBase+"/server/terms", apiTerms)
	registerContactRoutes(mux)
	mux.HandleFunc(apiBase+"/server/preferences", apiPreferences)
	mux.HandleFunc(apiBase+"/server/mcp", mcpHandler)
	mux.HandleFunc("/api/mcp", mcpHandler)
	mux.HandleFunc("/mcp", mcpHandler)
	mux.HandleFunc(apiBase+"/server/swagger", swaggerJSON)
	mux.HandleFunc("/api/swagger", swaggerJSON)
	mux.HandleFunc("/server/docs/swagger", swaggerUI)
	mux.Handle("/graphql", graphqlapi.Handler())
	mux.Handle(apiBase+"/server/graphql", graphqlapi.Handler())
	mux.Handle("/api/graphql", graphqlapi.Handler())
	mux.HandleFunc("/server/docs/graphql", graphQLUI)
	mux.HandleFunc("/graphql.js", graphQLScript)
	mux.HandleFunc("/theme.js", themeScript)
	handler := normalizeURL(secureHeaders(cors(rateLimit(language(mux)))))
	if exporter != nil {
		handler = exporter.Middleware(handler)
	}
	return handler
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
			if r.URL.RawQuery != "" {
				canonical += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, canonical, http.StatusMovedPermanently)
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
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: lang, Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
		}
		if lang == "" {
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
	stamp := assetStamp()
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
		for _, domain := range domainList(os.Getenv("DOMAIN"), config.Get().Server.FQDN) {
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
		csp := "default-src 'self'; style-src 'self'; script-src " + scriptSources + "; frame-src " + frameSources + "; connect-src " + connectSources + "; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"
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
		ip := requestClientIP(r)
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
		var output []byte
		var err error
		output, err = json.MarshalIndent(value, "", "  ")
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
		writeText(w, http.StatusOK, plainData(data))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if wantsText(r) {
		writeText(w, status, message)
		return
	}
	writeJSON(w, r, status, map[string]any{"ok": false, "error": code, "message": message, "details": map[string]any{}, "request_id": w.Header().Get("X-Request-ID")})
}

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

func apiTextExtension(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, ".txt") {
		http.NotFound(w, r)
		return
	}
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = strings.TrimSuffix(urlCopy.Path, ".txt")
	clone.URL = &urlCopy
	clone.Header.Set("X-GitMessages-Text-Response", "true")
	switch urlCopy.Path {
	case apiBase:
		apiRoot(w, clone)
	case apiBase + "/types":
		apiTypes(w, clone)
	case apiBase + "/conventions":
		apiConventions(w, clone)
	case apiBase + "/templates":
		apiTemplates(w, clone)
	case apiBase + "/templates/random":
		apiRandomTemplate(w, clone)
	case apiBase + "/messages/random":
		apiRandom(w, clone)
	case apiBase + "/messages":
		apiMessages(w, clone)
	case apiBase + "/server/healthz":
		apiHealth(w, clone)
	case apiBase + "/server/about":
		apiAbout(w, clone)
	case apiBase + "/server/help":
		apiHelp(w, clone)
	case apiBase + "/server/help/mcp":
		apiMCPHelp(w, clone)
	case apiBase + "/server/privacy":
		apiPrivacy(w, clone)
	case apiBase + "/server/terms":
		apiTerms(w, clone)
	case apiBase + "/server/security":
		apiContactSecurity(w, clone)
	case apiBase + "/server/contact":
		apiContact(w, clone)
	case apiBase + "/server/security/policy":
		apiSecurityPolicy(w, clone)
	case apiBase + "/server/security/thanks":
		apiSecurityThanks(w, clone)
	case apiBase + "/server/dpo":
		apiDPO(w, clone)
	case apiBase + "/server/preferences":
		apiPreferences(w, clone)
	case apiBase + "/server/preferences/sync":
		preferenceSyncAPI(w, clone)
	case apiBase + "/server/preferences/sync/import":
		preferenceSyncImportAPI(w, clone)
	case apiBase + "/server/swagger":
		swaggerJSON(w, clone)
	default:
		http.NotFound(w, r)
	}
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
	if r.URL.Path != apiBase || !method(w, r, http.MethodGet) {
		if r.URL.Path != apiBase {
			http.NotFound(w, r)
		}
		return
	}
	writeSuccess(w, r, map[string]any{"name": "Git Messages API", "version": serverVersion, "resources": []string{"types", "conventions", "templates", "messages"}, "server": []string{"healthz", "about", "help", "help/mcp", "privacy", "terms", "security", "preferences", "swagger", "mcp"}})
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
	writeSuccess(w, r, map[string]string{"message": result})
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
	features := overlayStatuses()
	if features == nil {
		features = map[string]any{}
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "healthy", "version": serverVersion, "service": "gitmessages", "features": features})
}

func apiAbout(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]string{"title": tr(r, "about.title"), "content": tr(r, "about.body") + "\n\n" + tr(r, "about.detail"), "format": "text"})
}

func apiHelp(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]any{
		"title":   tr(r, "help.title"),
		"content": tr(r, "help.builder") + ", " + tr(r, "help.types") + ". " + tr(r, "help.api") + ": /api/v1/types, /api/v1/conventions, /api/v1/templates, /api/v1/messages/random, POST /api/v1/messages.",
		"format":  "text",
		"pages":   serverHubPages(r, "/server/help"),
	})
}

func apiMCPHelp(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]any{
		"title":    tr(r, "mcp.title"),
		"content":  tr(r, "mcp.help.description") + " " + tr(r, "mcp.help.auth"),
		"format":   "text",
		"endpoint": "/api/v1/server/mcp",
		"aliases":  []string{"/api/mcp", "/mcp"},
		"tools":    mcpTools(r),
	})
}

func apiPrivacy(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]string{"title": tr(r, "privacy.title"), "content": tr(r, "privacy.body"), "format": "text"})
}

func apiTerms(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]string{"title": tr(r, "terms.title"), "content": tr(r, "terms.body"), "format": "text"})
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
	if old, err := r.Cookie("gitmessages_build"); err == nil && old.Value != stamp {
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	}
	http.SetCookie(w, &http.Cookie{Name: "gitmessages_build", Value: stamp, Path: "/", MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
	locale := i18n.Get(r.Header.Get("X-GitMessages-Language"))
	csrf := ensureCSRFCookie(w, r)
	theme := selectedTheme(r)
	var output bytes.Buffer
	stampURL := template.HTMLEscapeString(url.QueryEscape(stamp))
	nav := navigationMarkup(r)
	nextTheme := "dark"
	if theme == "dark" {
		nextTheme = "light"
	}
	robots := ""
	if r.URL.Path == "/server/contact" || strings.HasPrefix(r.URL.Path, "/server/security/report/") {
		robots = `<meta name="robots" content="noindex,nofollow">`
	}
	_, _ = fmt.Fprintf(&output, `<!doctype html>
<html lang="%s" dir="%s" class="theme-%s" data-theme="%s">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="theme-color" content="#282a36">
    <meta name="description" content="A curated reference and builder for git commit messages.">
    %s
    <link rel="manifest" href="/manifest.json?v=%s">
    <link rel="stylesheet" href="/styles.css?v=%s">
    <title>%s · Git Messages</title>
  </head>
  <body>
    <a class="skip-link" href="#main-content">%s</a><a class="skip-link skip-link-secondary" href="#navigation">%s</a><header role="banner"><a class="brand" href="/"><strong>Git Messages</strong></a><input class="nav-toggle" type="checkbox" id="nav-toggle"><nav class="nav-links" id="navigation" aria-label="%s">%s</nav><label class="nav-toggle-label" for="nav-toggle" aria-label="%s">☰</label><div class="header-actions"><a href="/server/preferences">%s</a><form method="post" action="/server/preferences/theme"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="theme" value="%s"><input type="hidden" name="return_to" value="%s"><button type="submit" aria-label="%s">%s</button></form></div></header>
    <main id="main-content" role="main" tabindex="-1"><div class="container container-md"><header class="page-header">%s<h1 class="page-title">%s</h1><p class="page-lead">%s</p></header>%s</div></main>
        <footer role="contentinfo">%s · <a href="/server/about">%s</a> · <a href="/server/help">%s</a> · <a href="/server/privacy">%s</a> · <a href="/server/terms">%s</a> · <a href="/server/security">%s</a> · <a href="/server/preferences">%s</a> · <a href="/server/preferences#language" lang="%s">%s</a> · <a href="/server/docs/swagger">%s</a> · <a href="/server/docs/graphql">%s</a></footer>
    <script src="/theme.js?v=%s" defer></script>
  </body>
</html>
	`, locale.Meta.Language, locale.Meta.Direction, theme, theme, robots, stampURL, stampURL, template.HTMLEscapeString(title), tr(r, "nav.skip"), tr(r, "nav.skip_navigation"), tr(r, "nav.main"), nav, tr(r, "nav.main"), tr(r, "preferences.title"), csrf, nextTheme, template.HTMLEscapeString(r.URL.Path), tr(r, "nav.theme"), tr(r, "theme."+nextTheme), serverPageBreadcrumb(r), template.HTMLEscapeString(title), template.HTMLEscapeString(title), body, tr(r, "footer.tagline"), tr(r, "nav.about"), tr(r, "nav.help"), tr(r, "nav.privacy"), tr(r, "nav.terms"), tr(r, "nav.security"), tr(r, "preferences.title"), locale.Meta.Language, template.HTMLEscapeString(locale.Meta.NativeName), tr(r, "api.rest"), tr(r, "graphql.title"), stampURL)
	pageHTML := prefixHTMLPaths(output.String(), requestBasePath(r))
	if wantsFrontendText(r) {
		writeText(w, http.StatusOK, formatHTMLText(pageHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
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

func updateThemePreference(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "CSRF token validation failed", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_THEME", tr(r, "api.invalid_request"))
		return
	}
	theme := r.FormValue("theme")
	if theme != "light" && theme != "dark" {
		writeError(w, r, http.StatusBadRequest, "INVALID_THEME", tr(r, "api.invalid_request"))
		return
	}
	scheme, _, _ := GetURLVars(r)
	http.SetCookie(w, &http.Cookie{Name: "theme", Value: theme, Path: "/", MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode, Secure: scheme == "https"})
	returnTo := r.FormValue("return_to")
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || strings.ContainsAny(returnTo, "\r\n") {
		returnTo = "/"
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func selectedTheme(r *http.Request) string {
	if cookie, err := r.Cookie("theme"); err == nil {
		switch cookie.Value {
		case "auto", "light", "dark":
			return cookie.Value
		}
	}
	defaultThemeMu.RLock()
	theme := defaultTheme
	defaultThemeMu.RUnlock()
	return theme
}

func home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	page(w, r, tr(r, "home.title"), template.HTML(`<p>`+tr(r, "home.intro")+`</p><p><a href="/builder">`+tr(r, "home.build")+`</a></p><section class="card"><h2>`+tr(r, "home.browse")+`</h2><p><a href="/types">`+tr(r, "nav.types")+`</a> · <a href="/templates">`+tr(r, "nav.templates")+`</a> · <a href="/server/docs/swagger">`+tr(r, "api.rest")+`</a> · <a href="/server/docs/graphql">`+tr(r, "api.graphql")+`</a></p></section>`))
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
	for _, item := range messages.TypesForLocale(languageCode(r)) {
		fmt.Fprintf(&body, `<article><h2>%s %s</h2><p>%s</p><pre>%s</pre></article>`, template.HTMLEscapeString(item.Emoji), template.HTMLEscapeString(item.Type), template.HTMLEscapeString(item.Description), template.HTMLEscapeString(item.Example))
	}
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
	for _, item := range messages.TemplatesForLocale(languageCode(r)) {
		fmt.Fprintf(&body, `<article><h2>%s</h2><p>%s: %s</p><pre>%s</pre></article>`, template.HTMLEscapeString(item.Name), tr(r, "templates.category"), template.HTMLEscapeString(item.Category), template.HTMLEscapeString(item.Message))
	}
	page(w, r, tr(r, "templates.title"), template.HTML(body.String()))
}

func builderPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, tr(r, "builder.invalid"), http.StatusBadRequest)
			return
		}
		message, err := messages.Build(messages.BuildInput{Type: r.FormValue("type"), Scope: r.FormValue("scope"), Description: r.FormValue("description"), Body: r.FormValue("body"), Breaking: r.FormValue("breaking") == "true", Convention: r.FormValue("convention")})
		if err != nil {
			page(w, r, tr(r, "builder.title"), builderForm(r, template.HTMLEscapeString(builderError(r, err)), r.Form))
			return
		}
		page(w, r, tr(r, "builder.result"), template.HTML(`<p>`+tr(r, "builder.copy")+`</p><pre>`+template.HTMLEscapeString(message)+`</pre><p><a href="/builder">`+tr(r, "builder.another")+`</a></p>`))
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, tr(r, "api.method_not_allowed"), http.StatusMethodNotAllowed)
		return
	}
	page(w, r, tr(r, "builder.title"), builderForm(r, "", nil))
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

func builderForm(r *http.Request, problem string, values url.Values) template.HTML {
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
	return template.HTML(problemHTML + `<form method="post" action="/builder">
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
	page(w, r, tr(r, "about.title"), template.HTML(`<p>`+tr(r, "about.body")+`</p><p>`+tr(r, "about.detail")+`</p>`))
}

func helpPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/help" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	body := `<section class="section-card" aria-labelledby="help-resources"><h2 id="help-resources">` + tr(r, "help.api") + `</h2><p>` + tr(r, "help.builder") + ` · ` + tr(r, "help.types") + `</p><p>` + tr(r, "help.api.docs") + ` <a href="/server/docs/swagger">` + tr(r, "api.rest") + `</a> · <a href="/server/docs/graphql">` + tr(r, "graphql.title") + `</a></p></section><section class="section-card" aria-labelledby="more-help"><h2 id="more-help">` + tr(r, "help.more") + `</h2><ul class="hub-list">` + serverHubList(r, "/server/help") + `</ul></section>`
	page(w, r, tr(r, "help.title"), template.HTML(body))
}

func mcpHelpPage(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<section class="section-card" id="overview"><h2>%s</h2><p>%s</p></section>`, tr(r, "mcp.help.overview"), tr(r, "mcp.help.description"))
	fmt.Fprintf(&body, `<section class="section-card" id="endpoint"><h2>%s</h2><div class="code-block"><code>POST /api/v1/server/mcp</code><button type="button" data-copy-text="POST /api/v1/server/mcp" aria-label="%s">%s</button></div><div class="code-block"><code>/api/mcp</code><button type="button" data-copy-text="/api/mcp" aria-label="%s">%s</button></div><div class="code-block"><code>/mcp</code><button type="button" data-copy-text="/mcp" aria-label="%s">%s</button></div><p>%s</p></section>`, tr(r, "mcp.help.endpoint"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.endpoint"))
	fmt.Fprintf(&body, `<section class="section-card" id="authentication"><h2>%s</h2><p>%s</p></section>`, tr(r, "mcp.help.authentication"), tr(r, "mcp.help.auth"))
	fmt.Fprintf(&body, `<section class="section-card" id="connect"><h2>%s</h2><p>%s</p><div class="code-block"><code>Content-Type: application/json<br>Accept: application/json, text/event-stream<br>MCP-Protocol-Version: 2026-07-28<br>Mcp-Method: tools/list</code><button type="button" data-copy-text="Content-Type: application/json&#10;Accept: application/json, text/event-stream&#10;MCP-Protocol-Version: 2026-07-28&#10;Mcp-Method: tools/list" aria-label="%s">%s</button></div></section>`, tr(r, "mcp.help.connect"), tr(r, "mcp.help.connect"), tr(r, "mcp.help.copy"), tr(r, "mcp.help.copy"))
	fmt.Fprintf(&body, `<section class="section-card" id="tools"><h2>%s</h2><div class="table-wrapper"><table><thead><tr><th>%s</th><th>%s</th></tr></thead><tbody>`, tr(r, "mcp.help.tools"), tr(r, "mcp.help.tool_name"), tr(r, "mcp.help.tool_description"))
	for _, tool := range mcpTools(r) {
		fmt.Fprintf(&body, `<tr><td><code>%s</code></td><td>%s</td></tr>`, template.HTMLEscapeString(tool["name"].(string)), template.HTMLEscapeString(tool["description"].(string)))
	}
	body.WriteString(`</tbody></table></div></section>`)
	fmt.Fprintf(&body, `<section class="section-card" id="limits"><h2>%s</h2><p>%s</p></section><section class="section-card" id="troubleshooting"><h2>%s</h2><p>%s</p></section><section class="section-card" id="project"><h2>%s</h2><p>%s</p></section>`, tr(r, "mcp.help.limits"), tr(r, "mcp.help.limits"), tr(r, "mcp.help.troubleshooting"), tr(r, "mcp.help.troubleshooting"), tr(r, "mcp.help.project"), tr(r, "mcp.help.project"))
	page(w, r, tr(r, "mcp.title"), template.HTML(body.String()))
}

func privacyPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/privacy" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	page(w, r, tr(r, "privacy.title"), template.HTML(`<p>`+tr(r, "privacy.body")+`</p>`))
}

func termsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/terms" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	page(w, r, tr(r, "terms.title"), template.HTML(`<p>`+tr(r, "terms.body")+`</p>`))
}

func preferencesPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !validCSRF(r) {
			http.Error(w, "CSRF token validation failed", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
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
		applyPreferenceValues(w, r, r.Form)
		if r.Form.Get("lang") == "auto" {
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
		}
		if r.Form.Get("consent_form") == "1" {
			consentJSON := fmt.Sprintf(`{"essential":true,"preferences":%t,"analytics":%t,"timestamp":%d}`, r.Form.Get("consent_preferences") == "on", r.Form.Get("consent_analytics") == "on", time.Now().Unix())
			consent := url.QueryEscape(consentJSON)
			http.SetCookie(w, &http.Cookie{Name: "cookie_consent", Value: consent, Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
			if config.Get().Server.Privacy.Data.Sold {
				if r.Form.Get("ccpa_opt_out") == "on" {
					http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "true", Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
				} else {
					http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, preferenceRedirect(r, "/server/preferences"), http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return
	}
	var body strings.Builder
	csrf := ensureCSRFCookie(w, r)
	fmt.Fprintf(&body, `<form method="post" action="/server/preferences"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="consent_form" value="1"><section class="section-card" id="language"><h2>%s</h2><label for="preference-language">%s</label><select id="preference-language" name="lang">%s</select></section><section class="section-card" id="theme"><h2>%s</h2><label for="preference-theme">%s</label><select id="preference-theme" name="theme">`, csrf, tr(r, "language.select"), tr(r, "language.select"), preferenceLanguageOptions(r, languageCode(r)), tr(r, "nav.theme"), tr(r, "nav.theme"))
	for _, option := range []struct{ value, key string }{{"auto", "theme.auto"}, {"light", "theme.light"}, {"dark", "theme.dark"}} {
		selected := ""
		if selectedTheme(r) == option.value {
			selected = " selected"
		}
		fmt.Fprintf(&body, `<option value="%s"%s>%s</option>`, option.value, selected, tr(r, option.key))
	}
	consent := readCookieConsent(r)
	preferenceChecked, analyticsChecked := "", ""
	if consent.Preferences {
		preferenceChecked = " checked"
	}
	if consent.Analytics {
		analyticsChecked = " checked"
	}
	fmt.Fprintf(&body, `</select></section><section class="section-card" aria-labelledby="privacy-preferences"><h2 id="privacy-preferences">%s</h2><label><input type="checkbox" checked disabled> %s</label><label><input type="checkbox" name="consent_preferences"%s> %s</label><label><input type="checkbox" name="consent_analytics"%s> %s</label>`, tr(r, "preferences.privacy"), tr(r, "preferences.essential_cookies"), preferenceChecked, tr(r, "preferences.preference_cookies"), analyticsChecked, tr(r, "preferences.analytics_cookies"))
	if config.Get().Server.Privacy.Data.Sold {
		ccpaChecked := ""
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			ccpaChecked = " checked"
		}
		fmt.Fprintf(&body, `<label><input type="checkbox" name="ccpa_opt_out"%s> %s</label>`, ccpaChecked, tr(r, "preferences.ccpa_optout"))
	}
	fmt.Fprintf(&body, `</section><div class="form-actions"><a class="btn btn-secondary" href="/server/preferences">%s</a><button class="btn btn-secondary" type="button" popovertarget="reset-preferences">%s</button><button class="btn btn-primary" type="submit">%s</button></div><div id="reset-preferences" popover><p>%s</p><button type="button" popovertarget="reset-preferences" popovertargetaction="hide">%s</button><button type="submit" name="action" value="reset">%s</button></div></form><section class="section-card" aria-labelledby="more-preferences"><h2 id="more-preferences">%s</h2><ul class="hub-list">%s</ul></section>`, tr(r, "preferences.cancel"), tr(r, "preferences.reset"), tr(r, "preferences.save"), tr(r, "preferences.erase_warning"), tr(r, "preferences.keep"), tr(r, "preferences.erase_everything"), tr(r, "preferences.more"), serverHubList(r, "/server/preferences"))
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
	value, decodeErr := url.QueryUnescape(cookie.Value)
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
	explicit := cookieErr == nil && i18n.IsSupported(cookie.Value) || i18n.IsSupported(r.URL.Query().Get("lang"))
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
	if r.URL.Path != "/server/healthz" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	content := `<p role="status">` + tr(r, "health.ok") + `</p><p>` + tr(r, "health.version") + `: ` + template.HTMLEscapeString(serverVersion) + `</p>`
	if features := overlayStatuses(); features != nil {
		for name, status := range features {
			encoded, _ := json.Marshal(status)
			var value struct {
				Status  string `json:"status"`
				Address string `json:"hostname"`
				I2P     string `json:"address"`
			}
			_ = json.Unmarshal(encoded, &value)
			address := value.Address
			if address == "" {
				address = value.I2P
			}
			content += `<p><strong>` + template.HTMLEscapeString(strings.ToUpper(name)) + `:</strong> ` + template.HTMLEscapeString(value.Status)
			if address != "" && value.Status == "healthy" {
				content += ` <code>` + template.HTMLEscapeString(address) + `</code>`
			}
			content += `</p>`
		}
	}
	page(w, r, tr(r, "health.title"), template.HTML(content))
}

func manifest(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, false) {
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	_, _ = fmt.Fprintf(w, `{"name":"Git Messages","short_name":"Git Messages","description":"A curated reference and builder for git commit messages.","start_url":"/","display":"standalone","background_color":"#282a36","theme_color":"#bd93f9","icons":[{"src":"/icon.svg?v=%s","sizes":"any","type":"image/svg+xml","purpose":"any maskable"}]}`+"\n", url.QueryEscape(assetStamp()))
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
	offlineHTML, _ := json.Marshal(fmt.Sprintf(`<!doctype html><html lang="%s"><title>%s</title><h1>%s</h1><p>%s</p></html>`, template.HTMLEscapeString(languageCode(r)), offlineTitle, offlineTitle, offlineBody))
	offlineText, _ := json.Marshal(tr(r, "common.offline"))
	_, _ = fmt.Fprintf(w, `const CACHE='gitmessages-%s';
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(['/','/types','/templates','/builder','/server/help','/styles.css?v=%s','/icon.svg?v=%s'])).then(()=>self.skipWaiting())));
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('gitmessages-')&&key!==CACHE).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',event=>{const request=event.request;const url=new URL(request.url);if(request.method!=='GET'||url.origin!==self.location.origin||url.pathname.startsWith('/api/'))return;if(request.mode==='navigate'){event.respondWith(fetch(request).then(response=>{if(response.ok){const copy=response.clone();caches.open(CACHE).then(cache=>cache.put(request,copy));}return response;}).catch(()=>caches.match(request).then(cached=>cached||new Response(%s,{status:503,headers:{'Content-Type':'text/html; charset=utf-8'}})).catch(()=>new Response(%s,{status:503}))))}});`, assetStamp(), url.QueryEscape(assetStamp()), url.QueryEscape(assetStamp()), string(offlineHTML), string(offlineText))
}

func swaggerJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	response := func(description string) map[string]any {
		return map[string]any{"200": map[string]any{"description": description}}
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
		"operationId": "mcp_endpoint",
		"summary":     tr(r, "mcp.title"),
		"description": tr(r, "mcp.help.description"),
		"parameters": []map[string]any{
			{"name": "Accept", "in": "header", "required": true, "schema": map[string]any{"type": "string", "example": "application/json, text/event-stream"}},
			{"name": "MCP-Protocol-Version", "in": "header", "required": true, "schema": map[string]any{"type": "string", "enum": []string{mcpProtocolVersion}}},
			{"name": "Mcp-Method", "in": "header", "required": true, "schema": map[string]any{"type": "string"}},
			{"name": "Mcp-Name", "in": "header", "required": false, "description": "Required for tools/call and must match params.name.", "schema": map[string]any{"type": "string"}},
		},
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": mcpRequestSchema}}},
		"responses":   map[string]any{"200": map[string]any{"description": tr(r, "mcp.help.description")}, "400": map[string]any{"description": "Invalid JSON-RPC request, unsupported version, or header mismatch."}, "403": map[string]any{"description": "Origin is not allowed."}, "406": map[string]any{"description": "Accept must include application/json and text/event-stream."}, "413": map[string]any{"description": "Request body exceeds 1 MiB."}, "415": map[string]any{"description": "Content-Type must be application/json."}, "429": map[string]any{"description": tr(r, "api.rate_limited")}},
	}}
	paths := make(map[string]any, len(apiOperationDefinitions(r))+6)
	for _, definition := range apiOperationDefinitions(r) {
		responses := response(definition.description)
		operation := map[string]any{
			"operationId": definition.name,
			"summary":     definition.summary,
			"description": definition.description,
			"responses":   responses,
		}
		if definition.method == http.MethodPost {
			operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": definition.inputSchema}}}
			operation["responses"] = map[string]any{"200": map[string]any{"description": definition.description}, "400": map[string]any{"description": tr(r, "swagger.build_message.error")}}
		}
		operations, _ := paths[definition.path].(map[string]any)
		if operations == nil {
			operations = make(map[string]any)
		}
		operations[strings.ToLower(definition.method)] = operation
		paths[definition.path] = operations
	}
	paths[apiBase+"/server/mcp"] = mcpOperation
	paths["/api/mcp"] = mcpOperation
	paths["/mcp"] = mcpOperation
	graphqlRead := map[string]any{"operationId": "graphql_query_get", "summary": tr(r, "graphql.title"), "description": tr(r, "graphql.description"), "responses": response(tr(r, "graphql.description"))}
	for _, path := range []string{"/graphql", "/api/graphql", apiBase + "/server/graphql"} {
		operations, _ := paths[path].(map[string]any)
		if operations == nil {
			operations = map[string]any{}
		}
		operations["get"] = graphqlRead
		paths[path] = operations
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"openapi": "3.1.0", "info": map[string]string{"title": "Git Messages API", "version": serverVersion, "description": tr(r, "swagger.info.description")}, "servers": []map[string]string{{"url": BuildURL(r, "")}}, "paths": paths})
}

func swaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/docs/swagger" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	page(w, r, tr(r, "api.rest"), template.HTML(`<p>`+tr(r, "api.openapi")+`: <a href="/api/v1/server/swagger">JSON</a></p><h2>`+tr(r, "api.resources")+`</h2><ul><li><code>GET /api/v1</code></li><li><code>GET /api/v1/types</code></li><li><code>GET /api/v1/conventions</code></li><li><code>GET /api/v1/templates</code></li><li><code>GET /api/v1/templates/random</code></li><li><code>GET /api/v1/messages/random</code></li><li><code>POST /api/v1/messages</code></li><li><code>GET /api/v1/server/healthz</code></li><li><code>GET /api/v1/server/about</code></li><li><code>GET /api/v1/server/help</code></li><li><code>GET /api/v1/server/help/mcp</code></li><li><code>GET /api/v1/server/privacy</code></li><li><code>GET /api/v1/server/terms</code></li><li><code>GET /api/v1/server/security</code></li><li><code>GET /api/v1/server/preferences</code></li><li><code>GET /api/v1/server/swagger</code></li><li><code>POST /api/v1/server/mcp</code></li><li><code>GET, POST /graphql</code></li></ul><p><a href="/server/help/mcp">`+tr(r, "mcp.title")+`</a></p>`))
}

func graphQLUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	page(w, r, tr(r, "graphql.title"), template.HTML(`<p>`+tr(r, "graphql.description")+`</p><form method="get" action="/graphql"><label for="query">`+tr(r, "graphql.query")+`</label><textarea id="query" name="query" rows="8">{ types { type description emoji example breaking } }</textarea><button type="submit">`+tr(r, "graphql.run")+`</button></form><pre id="response" aria-live="polite">`+tr(r, "graphql.result")+`</pre><script src="/graphql.js?v=`+template.HTMLEscapeString(url.QueryEscape(assetStamp()))+`" defer></script>`))
}

func themeScript(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintln(w, `(()=>{const root=document.documentElement;document.querySelectorAll('[data-copy-text]').forEach(button=>button.addEventListener('click',()=>navigator.clipboard?.writeText(button.dataset.copyText||'')));fetch('/locales/'+encodeURIComponent(root.lang)+'.json').then(response=>response.ok?response.json():Promise.reject()).then(locale=>{window.GitMessagesLocale=locale}).catch(()=>{});if('serviceWorker'in navigator)navigator.serviceWorker.register('/sw.js').catch(()=>{})})();`)
}

func graphQLScript(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, `document.querySelector('form[action="/graphql"]').addEventListener('submit',async function(event){event.preventDefault();const query=document.getElementById('query').value;const response=document.getElementById('response');try{const result=await fetch('/graphql',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query})});response.textContent=JSON.stringify(await result.json(),null,2)}catch(error){response.textContent=%q}});`, tr(r, "common.request_failed"))
}
