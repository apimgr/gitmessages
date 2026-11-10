package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	graphqlapi "github.com/apimgr/gitmessages/src/graphql"
	"github.com/apimgr/gitmessages/src/messages"
	htmlnode "golang.org/x/net/html"
	languagetags "golang.org/x/text/language"
)

const apiBase = "/api/v1"

var serverVersion = "dev"

// Handler returns the complete public web and API surface.
func Handler(version string) http.Handler {
	if version != "" {
		serverVersion = version
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", home)
	mux.HandleFunc("/types", typesPage)
	mux.HandleFunc("/templates", templatesPage)
	mux.HandleFunc("/builder", builderPage)
	mux.HandleFunc("/server/about", aboutPage)
	mux.HandleFunc("/server/help", helpPage)
	mux.HandleFunc("/server/privacy", privacyPage)
	mux.HandleFunc("/server/terms", termsPage)
	mux.HandleFunc("/server/healthz", healthPage)
	mux.HandleFunc("/manifest.json", manifest)
	mux.HandleFunc("/icon.svg", icon)
	mux.HandleFunc("/sw.js", serviceWorker)
	mux.HandleFunc(apiBase, apiRoot)
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
	mux.HandleFunc(apiBase+"/server/swagger", swaggerJSON)
	mux.HandleFunc("/api/swagger", swaggerJSON)
	mux.HandleFunc("/server/docs/swagger", swaggerUI)
	mux.Handle("/graphql", graphqlapi.Handler())
	mux.Handle(apiBase+"/server/graphql", graphqlapi.Handler())
	mux.Handle("/api/graphql", graphqlapi.Handler())
	mux.HandleFunc("/server/docs/graphql", graphQLUI)
	mux.HandleFunc("/graphql.js", graphQLScript)
	mux.HandleFunc("/theme.js", themeScript)
	return secureHeaders(cors(rateLimit(language(mux))))
}

func language(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := ""
		if query := r.URL.Query().Get("lang"); query != "" && i18n.IsSupported(query) {
			lang = query
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, HttpOnly: true})
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

func localizedRandomTemplate(lang string) messages.Template {
	items := messages.TemplatesForLocale(lang)
	return items[rand.IntN(len(items))]
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
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
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
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
	writeJSON(w, r, status, map[string]any{"ok": false, "error": code, "message": message, "details": map[string]any{}})
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
	writeSuccess(w, r, map[string]any{"name": "Git Messages API", "version": serverVersion, "resources": []string{"types", "conventions", "templates", "messages"}})
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
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "healthy", "version": serverVersion, "service": "gitmessages"})
}

func apiAbout(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]string{"name": "Git Messages API", "description": "A curated reference and builder for Conventional Commits and Angular commit messages."})
}

func apiHelp(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeSuccess(w, r, map[string]any{"documentation": "/server/help", "api_docs": "/server/docs/swagger", "graphql_docs": "/server/docs/graphql"})
}

func page(w http.ResponseWriter, r *http.Request, title string, body template.HTML) {
	locale := i18n.Get(r.Header.Get("X-GitMessages-Language"))
	var output bytes.Buffer
	_, _ = fmt.Fprintf(&output, `<!doctype html>
<html lang="%s" dir="%s" class="theme-dark">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="theme-color" content="#282a36">
    <meta name="description" content="A curated reference and builder for git commit messages.">
    <link rel="manifest" href="/manifest.json">
    <title>%s · Git Messages</title>
    <style>
      :root { color-scheme: dark light; --bg: #282a36; --fg: #f8f8f2; --surface: #2b2d3a; --border: #44475a; --primary: #bd93f9; --muted: #b9bfd0; }
      :root[data-theme="light"] { --bg: #ffffff; --fg: #1f2328; --surface: #f6f8fa; --border: #d1d9e0; --primary: #0969da; --muted: #59636e; color-scheme: light; }
      @media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { --bg: #ffffff; --fg: #1f2328; --surface: #f6f8fa; --border: #d1d9e0; --primary: #0969da; --muted: #59636e; color-scheme: light; } }
      * { box-sizing: border-box; }
      body { margin: 0; background: var(--bg); color: var(--fg); font: 1rem/1.6 system-ui, sans-serif; }
      main, header, footer { width: 90%%; max-width: 1100px; margin: auto; }
      header { padding: 1.25rem 0; display: flex; gap: 1rem; flex-wrap: wrap; align-items: center; justify-content: space-between; }
      nav { display: flex; gap: 1rem; flex-wrap: wrap; }
      a { color: var(--primary); }
      main { min-height: 65vh; padding: 2rem 0; }
      article, .card { background: var(--surface); border: 1px solid var(--border); border-radius: .75rem; padding: 1.25rem; margin: 1rem 0; }
      label { display: block; margin: 1rem 0 .25rem; font-weight: 650; }
      input, select, textarea, button { font: inherit; color: var(--fg); background: var(--bg); border: 1px solid var(--border); border-radius: .4rem; padding: .65rem; }
      input, select, textarea { display: block; width: 100%%; }
      button { cursor: pointer; background: var(--primary); color: #171321; font-weight: 700; min-height: 44px; }
      pre { white-space: pre-wrap; overflow-wrap: anywhere; padding: 1rem; border-radius: .5rem; background: var(--bg); }
      footer { padding: 2rem 0; text-align: center; color: var(--muted); }
      :focus-visible { outline: 3px solid var(--primary); outline-offset: 3px; }
      .skip-link { position: absolute; inset-inline-start: 1rem; top: -5rem; padding: .5rem 1rem; background: var(--surface); z-index: 10; }
      .skip-link:focus { top: 1rem; }
      [dir="rtl"] nav { direction: rtl; }
      @media (max-width: 767px) { main, header, footer { width: 98%%; } }
    </style>
  </head>
  <body>
    <a class="skip-link" href="#main">%s</a><header><a href="/"><strong>Git Messages</strong></a><nav aria-label="%s"><a href="/">%s</a><a href="/types">%s</a><a href="/templates">%s</a><a href="/builder">%s</a><a href="/server/help">%s</a></nav><form method="get" action=""><label for="lang">%s</label><select id="lang" name="lang">%s</select><button type="submit">%s</button></form><fieldset class="theme-picker"><legend>%s</legend><button type="button" data-theme-value="auto" aria-pressed="true">%s</button><button type="button" data-theme-value="light" aria-pressed="false">%s</button><button type="button" data-theme-value="dark" aria-pressed="false">%s</button></fieldset></header>
    <main id="main" tabindex="-1"><h1>%s</h1>%s</main>
    <footer>Curated git commit message conventions · <a href="/server/about">%s</a> · <a href="/server/privacy">%s</a> · <a href="/server/terms">%s</a></footer>
    <script src="/theme.js" defer></script>
  </body>
</html>
	`, locale.Meta.Language, locale.Meta.Direction, template.HTMLEscapeString(title), tr(r, "nav.skip"), tr(r, "nav.main"), tr(r, "nav.home"), tr(r, "nav.types"), tr(r, "nav.templates"), tr(r, "nav.builder"), tr(r, "nav.help"), tr(r, "language.select"), languageOptions(locale.Meta.Language), tr(r, "language.go"), tr(r, "nav.theme"), tr(r, "theme.auto"), tr(r, "theme.light"), tr(r, "theme.dark"), template.HTMLEscapeString(title), body, tr(r, "nav.about"), tr(r, "nav.privacy"), tr(r, "nav.terms"))
	if wantsFrontendText(r) {
		writeText(w, http.StatusOK, formatHTMLText(output.String()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(output.Bytes())
}

func languageOptions(current string) string {
	var options strings.Builder
	for _, code := range i18n.Supported() {
		locale := i18n.Get(code)
		selected := ""
		if code == current {
			selected = " selected"
		}
		fmt.Fprintf(&options, `<option value="%s"%s>%s</option>`, code, selected, template.HTMLEscapeString(locale.Meta.NativeName))
	}
	return options.String()
}

func home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page(w, r, tr(r, "home.title"), template.HTML(`<p>`+tr(r, "home.intro")+`</p><p><a href="/builder">`+tr(r, "home.build")+`</a></p><section class="card"><h2>`+tr(r, "home.browse")+`</h2><p><a href="/types">`+tr(r, "nav.types")+`</a> · <a href="/templates">`+tr(r, "nav.templates")+`</a> · <a href="/server/docs/swagger">REST API</a> · <a href="/server/docs/graphql">GraphQL</a></p></section>`))
}

func typesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/types" {
		http.NotFound(w, r)
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
			page(w, r, tr(r, "builder.title"), builderForm(r, template.HTMLEscapeString(err.Error()), r.Form))
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
  <label for="type">` + tr(r, "builder.type") + `</label><select id="type" name="type" required>` + options.String() + `</select>
  <label for="scope">` + tr(r, "builder.scope") + `</label><input id="scope" name="scope" maxlength="50" pattern="[A-Za-z0-9][A-Za-z0-9._/-]{0,49}">
  <label for="description">` + tr(r, "builder.description") + `</label><input id="description" name="description" required maxlength="100" minlength="1">
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
	page(w, r, tr(r, "about.title"), template.HTML(`<p>`+tr(r, "about.body")+`</p><p>`+tr(r, "about.detail")+`</p>`))
}

func helpPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/help" {
		http.NotFound(w, r)
		return
	}
	page(w, r, tr(r, "help.title"), template.HTML(`<p>Use the <a href="/builder">`+tr(r, "help.builder")+`</a> or browse the <a href="/types">`+tr(r, "help.types")+`</a>.</p><h2>`+tr(r, "help.api")+`</h2><ul><li><code>GET /api/v1/types</code></li><li><code>GET /api/v1/conventions</code></li><li><code>GET /api/v1/templates</code></li><li><code>GET /api/v1/messages/random</code></li><li><code>POST /api/v1/messages</code></li></ul><p>`+tr(r, "help.api.docs")+` <a href="/server/docs/swagger">OpenAPI</a> and <a href="/server/docs/graphql">GraphQL</a>.</p>`))
}

func privacyPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/privacy" {
		http.NotFound(w, r)
		return
	}
	page(w, r, tr(r, "privacy.title"), template.HTML(`<p>`+tr(r, "privacy.body")+`</p>`))
}

func termsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/terms" {
		http.NotFound(w, r)
		return
	}
	page(w, r, tr(r, "terms.title"), template.HTML(`<p>`+tr(r, "terms.body")+`</p>`))
}

func healthPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/healthz" {
		http.NotFound(w, r)
		return
	}
	page(w, r, tr(r, "health.title"), template.HTML(`<p role="status">`+tr(r, "health.ok")+`</p><p>`+tr(r, "health.version")+`: `+template.HTMLEscapeString(serverVersion)+`</p>`))
}

func manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	_, _ = fmt.Fprintln(w, `{"name":"Git Messages","short_name":"Git Messages","description":"A curated reference and builder for git commit messages.","start_url":"/","display":"standalone","background_color":"#282a36","theme_color":"#bd93f9","icons":[{"src":"/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"any maskable"}]}`)
}

func icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = fmt.Fprintln(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#282a36"/><path fill="#bd93f9" d="M104 128h304v48H104zm0 104h240v48H104zm0 104h192v48H104z"/></svg>`)
}

func serviceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintln(w, `const CACHE='gitmessages-v1';
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(['/','/types','/templates','/builder','/server/help'])).then(()=>self.skipWaiting())));
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('gitmessages-')&&key!==CACHE).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',event=>{const request=event.request;const url=new URL(request.url);if(request.method!=='GET'||url.origin!==self.location.origin||url.pathname.startsWith('/api/'))return;if(request.mode==='navigate'){event.respondWith(fetch(request).then(response=>{if(response.ok){const copy=response.clone();caches.open(CACHE).then(cache=>cache.put(request,copy));}return response;}).catch(()=>caches.match(request).then(cached=>cached||new Response('<!doctype html><title>Offline</title><h1>Offline</h1><p>This page is not available offline.</p>',{status:503,headers:{'Content-Type':'text/html; charset=utf-8'}})).catch(()=>new Response('Offline',{status:503}))))}});`)
}

func swaggerJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"openapi": "3.1.0", "info": map[string]string{"title": "Git Messages API", "version": serverVersion, "description": "Curated git commit conventions and message builder."}, "paths": map[string]any{
		"/api/v1/types":            map[string]any{"get": map[string]any{"summary": "List commit types", "responses": map[string]any{"200": map[string]string{"description": "Commit type catalog"}}}},
		"/api/v1/conventions":      map[string]any{"get": map[string]any{"summary": "List conventions", "responses": map[string]any{"200": map[string]string{"description": "Convention catalog"}}}},
		"/api/v1/templates":        map[string]any{"get": map[string]any{"summary": "List message templates", "responses": map[string]any{"200": map[string]string{"description": "Message templates"}}}},
		"/api/v1/templates/random": map[string]any{"get": map[string]any{"summary": "Get a random template", "responses": map[string]any{"200": map[string]string{"description": "Random template"}}}},
		"/api/v1/messages/random":  map[string]any{"get": map[string]any{"summary": "Get a random example message", "responses": map[string]any{"200": map[string]string{"description": "Random message"}}}},
		"/api/v1/messages":         map[string]any{"post": map[string]any{"summary": "Build a commit message", "responses": map[string]any{"200": map[string]string{"description": "Built message"}, "400": map[string]string{"description": "Invalid input"}}}},
	}})
}

func swaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/docs/swagger" {
		http.NotFound(w, r)
		return
	}
	page(w, r, "REST API", template.HTML(`<p>OpenAPI: <a href="/api/v1/server/swagger">JSON</a></p><h2>Resources</h2><ul><li><code>GET /api/v1/types</code></li><li><code>GET /api/v1/conventions</code></li><li><code>GET /api/v1/templates</code></li><li><code>GET /api/v1/messages/random</code></li><li><code>POST /api/v1/messages</code></li></ul>`))
}

func graphQLUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, r, http.MethodGet)
		return
	}
	page(w, r, tr(r, "graphql.title"), template.HTML(`<p>`+tr(r, "graphql.description")+`</p><form method="get" action="/graphql"><label for="query">`+tr(r, "graphql.query")+`</label><textarea id="query" name="query" rows="8">{ types { type description emoji example breaking } }</textarea><button type="submit">`+tr(r, "graphql.run")+`</button></form><pre id="response" aria-live="polite">`+tr(r, "graphql.result")+`</pre><script src="/graphql.js" defer></script>`))
}

func themeScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintln(w, `(()=>{const root=document.documentElement;const buttons=[...document.querySelectorAll('[data-theme-value]')];const supported=['auto','light','dark'];let choice='auto';try{const saved=localStorage.getItem('gitmessages-theme');if(supported.includes(saved))choice=saved}catch{}const apply=value=>{choice=value;root.dataset.theme=value;for(const button of buttons)button.setAttribute('aria-pressed',String(button.dataset.themeValue===value));try{localStorage.setItem('gitmessages-theme',value)}catch{}};apply(choice);for(const button of buttons)button.addEventListener('click',()=>apply(button.dataset.themeValue));if('serviceWorker'in navigator)navigator.serviceWorker.register('/sw.js').catch(()=>{})})();`)
}

func graphQLScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, `document.querySelector('form[action="/graphql"]').addEventListener('submit',async function(event){event.preventDefault();const query=document.getElementById('query').value;const response=document.getElementById('response');try{const result=await fetch('/graphql',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query})});response.textContent=JSON.stringify(await result.json(),null,2)}catch(error){response.textContent=%q}});`, tr(r, "common.request_failed"))
}
