package web

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
	swaggerapi "github.com/apimgr/gitmessages/src/swagger"
	"golang.org/x/net/html"
)

var webTestRequestSequence atomic.Uint32

func TestHTMLServerErrorsUseThemedLayoutWithoutDetails(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHTTPError(w, r, "database password must never be exposed", http.StatusInternalServerError)
	})
	request := webTestRequest(http.MethodGet, "/server/error", nil)
	request.Header.Set("Accept", "text/html")
	request.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `class="theme-light"`) || !strings.Contains(response.Body.String(), "<main") {
		t.Fatalf("server error did not use a themed HTML page: status=%d", response.Code)
	}
	if strings.Contains(response.Body.String(), "database password") || !strings.Contains(response.Body.String(), "Something went wrong") {
		t.Fatal("server error page exposed internal details or omitted its generic message")
	}
}

func TestOfflinePageRendersThemedLocalizedContent(t *testing.T) {
	request := webTestRequest(http.MethodGet, "/offline", nil)
	request.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	response := httptest.NewRecorder()
	offlinePage(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `class="theme-dark"`) || !strings.Contains(response.Body.String(), `<main`) {
		t.Fatalf("offline page did not render the themed layout: status=%d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "You are offline") || !strings.Contains(response.Body.String(), "not available offline") {
		t.Fatalf("offline page omitted its translated content: %s", response.Body.String())
	}
}

func TestOfflinePageRejectsNonGet(t *testing.T) {
	response := httptest.NewRecorder()
	offlinePage(response, webTestRequest(http.MethodPost, "/offline", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("offline page method handling = %d, Allow %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestReadCookieConsentDefaultsAndParses(t *testing.T) {
	withoutCookie := readCookieConsent(webTestRequest(http.MethodGet, "/", nil))
	if !withoutCookie.Essential || withoutCookie.Preferences || withoutCookie.Analytics {
		t.Fatalf("missing cookie consent did not default to essential only: %#v", withoutCookie)
	}

	request := webTestRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: "%7B%22preferences%22%3Atrue%2C%22analytics%22%3Atrue%2C%22timestamp%22%3A123%7D"})
	parsed := readCookieConsent(request)
	if !parsed.Essential || !parsed.Preferences || !parsed.Analytics || parsed.Timestamp != 123 {
		t.Fatalf("cookie consent was not decoded: %#v", parsed)
	}

	request = webTestRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: "%zz"})
	malformed := readCookieConsent(request)
	if !malformed.Essential || malformed.Preferences || malformed.Analytics {
		t.Fatalf("malformed cookie consent did not default safely: %#v", malformed)
	}
}

func TestBuilderErrorMapsKnownAndUnknownErrors(t *testing.T) {
	request := webTestRequest(http.MethodGet, "/builder", nil)
	known := builderError(request, fmt.Errorf("unknown commit type"))
	if known == "" || known == tr(request, "builder.invalid") {
		t.Fatalf("known builder error used fallback translation: %q", known)
	}
	unknown := builderError(request, fmt.Errorf("internal validation detail"))
	if unknown != tr(request, "builder.invalid") || strings.Contains(unknown, "internal validation detail") {
		t.Fatalf("unknown builder error exposed details or missed generic translation: %q", unknown)
	}
}

func webTestRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	sequence := webTestRequestSequence.Add(1)
	request.RemoteAddr = fmt.Sprintf("198.18.%d.%d:1234", (sequence>>8)&0xff, sequence&0xff)
	return request
}

func TestHelpPageUsesRequestHostForAPIExamples(t *testing.T) {
	handler := Handler("test-version")
	request := webTestRequest(http.MethodGet, "/server/help", nil)
	request.Host = "docs.example.test"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	wantURL := BuildURL(request, APIBasePath()+"/types")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), wantURL) {
		t.Fatalf("help page returned %d or omitted request-specific API URL %q", response.Code, wantURL)
	}
	if strings.Contains(response.Body.String(), "localhost") {
		t.Fatal("help page contains a hardcoded localhost URL")
	}
}

func TestAboutPageIncludesRequiredGeoIPAttribution(t *testing.T) {
	handler := Handler("test-version")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/server/about", nil))
	const attribution = `<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>`
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), attribution) || !strings.Contains(page.Body.String(), tr(webTestRequest(http.MethodGet, "/server/about", nil), "about.catalog_attribution")) {
		t.Fatalf("About page returned %d or omitted GeoIP attribution", page.Code)
	}

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, webTestRequest(http.MethodGet, APIBasePath()+"/server/about", nil))
	var response struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if api.Code != http.StatusOK || json.Unmarshal(api.Body.Bytes(), &response) != nil || !strings.Contains(response.Data.Content, attribution) || !strings.Contains(response.Data.Content, "project-authored") {
		t.Fatalf("About API returned %d or omitted GeoIP attribution", api.Code)
	}
}

func TestPublicPagesAndAPI(t *testing.T) {
	SetCORSOrigins([]string{"*"})
	handler := Handler("test-version")
	for _, path := range []string{"/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/help/mcp", "/server/healthz", "/server/privacy", "/server/terms", "/server/security", "/server/preferences", "/.well-known/security.txt", "/server/docs/swagger", "/server/docs/graphql", "/manifest.json", "/icon.svg", "/styles.css", "/locales/en.json", "/sw.js", "/static/js/docs-swagger.js", "/static/js/docs-graphql.js", "/static/vendor/swagger-ui/swagger-ui-bundle.js", "/static/js/graphiql-compat.js", "/static/vendor/graphiql/graphiql.min.js", "/static/js/app.js", "/api/healthz", "/api/v1", "/api/v1/types", "/api/v1/conventions", "/api/v1/templates", "/api/v1/templates/random", "/api/v1/messages/random", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/help/mcp", "/api/v1/server/privacy", "/api/v1/server/terms", "/api/v1/server/security", "/api/v1/server/preferences", "/api/v1/server/swagger"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, webTestRequest(http.MethodGet, path, nil))
		if recorder.Code == http.StatusNotFound {
			t.Errorf("GET %s returned 404", path)
		}
		if recorder.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("GET %s missing CORS header", path)
		}
	}
}

func TestAutodiscoverAndConfiguredAPIVersion(t *testing.T) {
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  api_version: v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load v2 API config: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})

	handler := Handler("test-version")
	discovery := httptest.NewRecorder()
	handler.ServeHTTP(discovery, webTestRequest(http.MethodGet, "/api/autodiscover", nil))
	if discovery.Code != http.StatusOK {
		t.Fatalf("autodiscover status = %d: %s", discovery.Code, discovery.Body.String())
	}
	var response struct {
		Data struct {
			API struct {
				CurrentVersion string `json:"current_version"`
			} `json:"api"`
			Name          string         `json:"name"`
			Version       string         `json:"version"`
			URLs          []string       `json:"urls"`
			CLIVersions   map[string]any `json:"cli_versions"`
			CLIMinVersion string         `json:"cli_min_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(discovery.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode autodiscover response: %v", err)
	}
	if response.Data.API.CurrentVersion != "v2" || response.Data.Name != paths.ProjectName || response.Data.Version != "test-version" || len(response.Data.URLs) == 0 {
		t.Fatalf("autodiscover metadata is incomplete: %#v", response.Data)
	}
	if response.Data.CLIVersions == nil || response.Data.CLIMinVersion != config.Get().Server.Update.CLIMinVersion {
		t.Fatalf("autodiscover omitted CLI version metadata: %#v", response.Data)
	}

	versioned := httptest.NewRecorder()
	handler.ServeHTTP(versioned, webTestRequest(http.MethodGet, "/api/v2/types", nil))
	if versioned.Code != http.StatusOK {
		t.Fatalf("configured API route returned %d: %s", versioned.Code, versioned.Body.String())
	}
	unconfigured := httptest.NewRecorder()
	handler.ServeHTTP(unconfigured, webTestRequest(http.MethodGet, "/api/v1/types", nil))
	if unconfigured.Code != http.StatusNotFound {
		t.Fatalf("non-configured API version returned %d, want 404", unconfigured.Code)
	}
}

func TestDiscoveryOriginsForTorRequestsExcludeClearnet(t *testing.T) {
	current := config.Get()
	previousOnion, previousFQDN := current.Server.Tor.OnionAddress, current.Server.FQDN
	onion := strings.Repeat("a", 56) + ".onion"
	current.Server.Tor.OnionAddress = onion
	current.Server.FQDN = "clearnet.example.test"
	t.Setenv("DOMAIN", "clearnet.example.test,www.example.test")
	t.Cleanup(func() {
		current.Server.Tor.OnionAddress = previousOnion
		current.Server.FQDN = previousFQDN
	})
	request := httptest.NewRequest(http.MethodGet, "http://"+onion+"/api/autodiscover", nil)
	if got := discoveryOrigins(request); len(got) != 1 || got[0] != "http://"+onion {
		t.Fatalf("Tor discovery origins = %v, want only onion origin", got)
	}
}

func TestConfiguredBrandingIsUsedAcrossPublicSurfaces(t *testing.T) {
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	contents := "server:\n  branding:\n    title: Example Messages\n    tagline: Curated commit guidance\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load branding test configuration: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})
	handler := Handler("test-version")
	for _, test := range []struct {
		path string
		want string
	}{{"/", "Curated commit guidance"}, {"/manifest.json", `"name":"Example Messages"`}, {"/api/v1", "Example Messages API"}, {"/api/autodiscover", "gitmessages"}, {"/api/v1/server/swagger", "Example Messages API"}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, webTestRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("GET %s returned %d or omitted branding %q: %s", test.path, response.Code, test.want, response.Body.String())
		}
	}
}

func TestUnversionedHealthAliasIsPublicJSON(t *testing.T) {
	handler := Handler("test-version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, webTestRequest(http.MethodGet, "/api/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /api/healthz returned %d with %q: %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Status != "healthy" {
		t.Fatalf("GET /api/healthz status = %q, decode error = %v", body.Status, err)
	}
}

func TestOptionalRootHealthAlias(t *testing.T) {
	current := config.Get()
	previous := current.Server.Healthz.Root.Enabled
	t.Cleanup(func() { current.Server.Healthz.Root.Enabled = previous })
	current.Server.Healthz.Root.Enabled = false
	handler := Handler("test-version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, webTestRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled /healthz returned %d, want 404", response.Code)
	}
	current.Server.Healthz.Root.Enabled = true
	handler = Handler("test-version")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, webTestRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `class="page-title"`) {
		t.Fatalf("enabled /healthz returned %d: %s", response.Code, response.Body.String())
	}
}

func TestServerPageShellAndRegisteredPages(t *testing.T) {
	tokenStore := tokenRouteStore(t)
	contactDatabase, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "pages.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	ConfigureContact(contactDatabase)
	t.Cleanup(func() {
		ConfigureContact(nil)
		_ = contactDatabase.Close()
	})
	handler := Handler("test-version")
	paths := []string{
		"/", "/types", "/templates", "/builder",
		"/server/about", "/server/help", "/server/help/mcp", "/server/privacy", "/server/terms",
		"/server/preferences", "/server/preferences/sync", "/server/preferences/tokens", "/server/healthz",
		"/server/security", "/server/security/policy", "/server/security/thanks", "/server/dpo",
		"/server/contact", "/server/docs/swagger", "/server/docs/graphql",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, webTestRequest(http.MethodGet, path, nil))
			body := recorder.Body.String()
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("GET %s returned %d with %q", path, recorder.Code, recorder.Header().Get("Content-Type"))
			}
			if strings.Count(body, "<main ") != 1 || strings.Count(body, `<h1 class="page-title">`) != 1 {
				t.Errorf("GET %s must render exactly one main and one page title", path)
			}
			wantWidth := "container-md"
			switch path {
			case "/builder", "/server/contact", "/server/preferences", "/server/preferences/sync", "/server/preferences/tokens":
				wantWidth = "container-sm"
			}
			if !strings.Contains(body, `class="container `+wantWidth+`"`) {
				t.Errorf("GET %s must use %s", path, wantWidth)
			}
			assertPageConformance(t, body)
			if path == "/server/preferences" {
				if strings.Contains(body, `class="theme-toggle-form"`) {
					t.Error("preferences page must use the theme control in its single form")
				}
			} else if !strings.Contains(body, `class="theme-toggle-form" method="post" action="/server/preferences"`) {
				t.Errorf("GET %s omitted the header theme form", path)
			}
			if path == "/server/security/thanks" && !strings.Contains(body, tr(webTestRequest(http.MethodGet, path, nil), "security.thanks.empty")) {
				t.Error("security acknowledgments page omitted its empty state")
			}
			if !strings.Contains(body, `<a class="skip-link" href="#main-content">`) || !strings.Contains(body, `class="skip-link skip-link-secondary" href="#navigation"`) {
				t.Errorf("GET %s omitted the shared skip links", path)
			}
			if !strings.Contains(body, `class="section-card"`) || !strings.Contains(body, `<h2`) {
				t.Errorf("GET %s has no labelled section card", path)
			}
			if strings.Contains(path, "/server/preferences/") || strings.Contains(path, "/server/help/") {
				if strings.Count(body, `class="breadcrumb"`) != 1 {
					t.Errorf("GET %s must render exactly one hub breadcrumb", path)
				}
				hub := "/server/preferences"
				if strings.Contains(path, "/server/help/") {
					hub = "/server/help"
				}
				if strings.Count(body, `<nav class="breadcrumb" aria-label="Breadcrumb"><a href="`+hub+`"`) != 1 {
					t.Errorf("GET %s breadcrumb must point to %s", path, hub)
				}
			} else if strings.Contains(body, `class="breadcrumb"`) {
				t.Errorf("hub or top-level page %s must not render a breadcrumb", path)
			}
		})
	}

	for _, hub := range []string{"/server/preferences", "/server/help"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, webTestRequest(http.MethodGet, hub, nil))
		for _, entry := range serverPageRegistry(hub) {
			if got := strings.Count(recorder.Body.String(), `class="hub-link" href="`+entry.Path+`"`); got != 1 {
				t.Errorf("hub %s links registered page %s %d times, want once", hub, entry.Path, got)
			}
		}
	}
	primary, identity, err := tokenStore.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokenStore.CreateLinked(t.Context(), identity.OwnerID, "page-conformance", nil); err != nil {
		t.Fatal(err)
	}
	tokenPageRequest := webTestRequest(http.MethodGet, "/server/preferences/tokens", nil)
	tokenPageRequest.AddCookie(&http.Cookie{Name: ownerCookieName, Value: primary})
	tokenPageResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenPageResponse, tokenPageRequest)
	if tokenPageResponse.Code != http.StatusOK {
		t.Fatalf("authenticated token page returned %d", tokenPageResponse.Code)
	}
	assertPageConformance(t, tokenPageResponse.Body.String())
	if !strings.Contains(tokenPageResponse.Body.String(), `<ul><li id="token-`) {
		t.Error("linked tokens are not rendered as a semantic list")
	}
	helpAPI := httptest.NewRecorder()
	handler.ServeHTTP(helpAPI, webTestRequest(http.MethodGet, "/api/v1/server/help", nil))
	var helpResponse struct {
		Data struct {
			Pages []struct {
				Path string `json:"path"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(helpAPI.Body.Bytes(), &helpResponse); err != nil {
		t.Fatalf("decode help API response: %v", err)
	}
	for _, entry := range serverPageRegistry("/server/help") {
		found := false
		for _, page := range helpResponse.Data.Pages {
			found = found || page.Path == entry.Path
		}
		if !found {
			t.Errorf("help API omitted registered page %s", entry.Path)
		}
	}
}

func TestThemedErrorPagesHaveOnePageTitle(t *testing.T) {
	handler := Handler("test-version")
	for _, testCase := range []struct {
		name   string
		path   string
		status int
	}{
		{name: "not found", path: "/missing-page", status: http.StatusNotFound},
		{name: "bad request", path: "/server/error", status: http.StatusBadRequest},
		{name: "unauthorized", path: "/server/error", status: http.StatusUnauthorized},
		{name: "forbidden", path: "/server/error", status: http.StatusForbidden},
		{name: "method not allowed", path: "/server/error", status: http.StatusMethodNotAllowed},
		{name: "conflict", path: "/server/error", status: http.StatusConflict},
		{name: "unprocessable entity", path: "/server/error", status: http.StatusUnprocessableEntity},
		{name: "too many requests", path: "/server/error", status: http.StatusTooManyRequests},
		{name: "server error", path: "/server/error", status: http.StatusInternalServerError},
		{name: "service unavailable", path: "/server/error", status: http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := webTestRequest(http.MethodGet, testCase.path, nil)
			request.Header.Set("Accept", "text/html")
			response := httptest.NewRecorder()
			if testCase.status != http.StatusNotFound {
				writeHTTPError(response, request, "internal detail", testCase.status)
			} else {
				handler.ServeHTTP(response, request)
			}
			body := response.Body.String()
			if response.Code != testCase.status || strings.Count(body, `<h1 class="page-title">`) != 1 || strings.Count(body, `<main `) != 1 {
				t.Fatalf("%s page returned status %d with invalid page shell", testCase.name, response.Code)
			}
			assertPageConformance(t, body)
		})
	}
}

func TestBrandingHeaderFragmentIsLastHeaderChild(t *testing.T) {
	current := config.Get()
	previous := current.Server.Branding.Content.Header
	current.Server.Branding.Content.Header = config.OperatorContent{Format: "html", Body: `<span id="operator-header">Operator</span>`}
	t.Cleanup(func() { current.Server.Branding.Content.Header = previous })
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, webTestRequest(http.MethodGet, "/server/about", nil))
	document, err := html.Parse(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var header *html.Node
	var find func(*html.Node)
	find = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "header" && attrValue(node, "role") == "banner" {
			header = node
			return
		}
		for child := node.FirstChild; child != nil && header == nil; child = child.NextSibling {
			find(child)
		}
	}
	find(document)
	if header == nil {
		t.Fatal("page has no banner header")
	}
	children := elementChildren(header)
	if len(children) != 5 || (children[4].Data != "span" && !hasDescendant(children[4], "span")) || attrValue(findByID(children[4], "operator-header"), "id") != "operator-header" {
		t.Fatalf("operator fragment is not the last header child: %d elements", len(children))
	}
	if children[0].Data != "a" || !hasClass(children[1], "nav-links") || !hasClass(children[2], "nav-toggle") || !hasClass(children[3], "header-actions") {
		t.Fatal("operator fragment changed the standard header elements")
	}
}

func findByID(node *html.Node, id string) *html.Node {
	if node.Type == html.ElementNode && attrValue(node, "id") == id {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func assertPageConformance(t *testing.T, source string) {
	t.Helper()
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("parse server page: %v", err)
	}
	var nodes []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		nodes = append(nodes, node)
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	count := func(tag string) int {
		total := 0
		for _, node := range nodes {
			if node.Type == html.ElementNode && node.Data == tag {
				total++
			}
		}
		return total
	}
	if count("main") != 1 || count("h1") != 1 {
		t.Errorf("page must contain exactly one main and one h1, got %d and %d", count("main"), count("h1"))
	}
	containerWidths := 0
	for _, node := range nodes {
		if node.Type != html.ElementNode || node.Data != "div" || !hasClass(node, "container") {
			continue
		}
		widths := 0
		for _, width := range []string{"container-sm", "container-md", "container-lg"} {
			if hasClass(node, width) {
				widths++
			}
		}
		if widths != 1 {
			t.Errorf(".container must have exactly one width modifier, got %d", widths)
		}
		containerWidths++
	}
	if containerWidths != 1 {
		t.Errorf("page must have exactly one shared container, got %d", containerWidths)
	}
	focusable := make([]*html.Node, 0)
	bannerCount := 0
	for _, node := range nodes {
		if node.Type != html.ElementNode {
			continue
		}
		attrs := make(map[string]string, len(node.Attr))
		for _, attr := range node.Attr {
			attrs[attr.Key] = attr.Val
			if strings.EqualFold(attr.Key, "style") || strings.HasPrefix(strings.ToLower(attr.Key), "on") {
				t.Errorf("page contains forbidden %s attribute on <%s>", attr.Key, node.Data)
			}
		}
		if node.Data == "style" {
			t.Error("page contains an inline style element")
		}
		if node.Data == "script" && attrs["type"] != "application/ld+json" && strings.TrimSpace(nodeText(node)) != "" {
			t.Error("page contains an executable inline script")
		}
		if node.Data == "img" {
			if _, ok := attrs["alt"]; !ok {
				t.Error("image is missing its alt attribute")
			}
		}
		if node.Data == "a" || node.Data == "button" || node.Data == "input" || node.Data == "select" || node.Data == "textarea" {
			if attrs["type"] != "hidden" && attrs["tabindex"] != "-1" {
				focusable = append(focusable, node)
			}
		}
		if node.Data == "input" || node.Data == "select" || node.Data == "textarea" {
			inputName := attrs["type"] == "submit" || attrs["type"] == "button" || attrs["type"] == "reset"
			accessibleName := attrs["aria-label"] != "" || attrs["aria-labelledby"] != "" || hasAssociatedLabel(node, attrs["id"], nodes) || inputName && attrs["value"] != ""
			if attrs["type"] != "hidden" && !accessibleName {
				t.Errorf("<%s> control is missing a label", node.Data)
			}
		}
		if node.Data == "button" && attrs["aria-label"] == "" && attrs["aria-labelledby"] == "" && strings.TrimSpace(nodeText(node)) == "" {
			t.Error("button is missing an accessible name")
		}
		if node.Data == "form" && strings.EqualFold(attrs["method"], "post") && !hasNamedDescendant(node, "input", "name", "csrf_token") {
			t.Error("POST form is missing its csrf_token")
		}
		if node.Data == "table" {
			if !hasDescendant(node, "thead") || node.Parent == nil || node.Parent.Type != html.ElementNode || node.Parent.Data != "div" || !hasClass(node.Parent, "table-wrapper") {
				t.Error("table must have a thead and be inside .table-wrapper")
			}
			if !hasDescendant(node, "caption") && !hasSectionHeading(node) {
				t.Error("table must have a caption or a heading in its section")
			}
		}
		if node.Data == "section" && hasClass(node, "section-card") {
			labelledBy := attrs["aria-labelledby"]
			if labelledBy == "" || !hasHeadingID(node, labelledBy) {
				t.Error(".section-card must reference its h2 with aria-labelledby")
			}
			headingCount := 0
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && child.Data == "h2" {
					headingCount++
				}
			}
			if headingCount != 1 {
				t.Errorf(".section-card must have exactly one direct h2, got %d", headingCount)
			}
			for parent := node.Parent; parent != nil; parent = parent.Parent {
				if parent.Type == html.ElementNode && parent.Data == "section" && hasClass(parent, "section-card") {
					t.Error(".section-card must not be nested inside another .section-card")
					break
				}
			}
		}
		if node.Data == "header" && attrs["role"] == "banner" {
			bannerCount++
		}
	}
	if len(focusable) < 2 || !hasClass(focusable[0], "skip-link") || !hasClass(focusable[1], "skip-link-secondary") {
		t.Error("the two skip links must be the first focusable elements")
	}
	for _, node := range nodes {
		if node.Type != html.ElementNode || node.Data != "header" || attrValue(node, "role") != "banner" {
			continue
		}
		children := make([]*html.Node, 0, 4)
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode {
				children = append(children, child)
			}
		}
		if len(children) != 4 || children[0].Data != "a" || !hasClass(children[0], "brand") || children[1].Data != "nav" || !hasClass(children[1], "nav-links") || children[2].Data != "label" || !hasClass(children[2], "nav-toggle") || children[3].Data != "div" || !hasClass(children[3], "header-actions") {
			t.Error("header elements do not match the required brand, navigation, menu, and actions order")
		}
		if len(children) == 4 {
			actions := elementChildren(children[3])
			if len(actions) < 1 || len(actions) > 2 || actions[0].Data != "a" || attrValue(actions[0], "href") != "/server/preferences" {
				t.Error("header actions must start with the preferences link")
			} else if len(actions) == 2 && (actions[1].Data != "form" || !hasClass(actions[1], "theme-toggle-form")) {
				t.Error("the optional second header action must be the theme form")
			}
		}
	}
	if bannerCount != 1 {
		t.Errorf("page must contain exactly one site banner, got %d", bannerCount)
	}
}

func attrValue(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func elementChildren(node *html.Node) []*html.Node {
	children := make([]*html.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

func hasClass(node *html.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" && strings.Contains(" "+attr.Val+" ", " "+class+" ") {
			return true
		}
	}
	return false
}

func hasDescendant(node *html.Node, tag string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag || hasDescendant(child, tag) {
			return true
		}
	}
	return false
}

func hasSectionHeading(node *html.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "section" {
			return hasDescendant(parent, "h2")
		}
	}
	return false
}

func hasHeadingID(node *html.Node, id string) bool {
	if node.Type == html.ElementNode && node.Data == "h2" && attrValue(node, "id") == id {
		return true
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasHeadingID(child, id) {
			return true
		}
	}
	return false
}

func hasNamedDescendant(node *html.Node, tag, attrName, attrValue string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			for _, attr := range child.Attr {
				if attr.Key == attrName && attr.Val == attrValue {
					return true
				}
			}
		}
		if hasNamedDescendant(child, tag, attrName, attrValue) {
			return true
		}
	}
	return false
}

func hasAssociatedLabel(node *html.Node, id string, all []*html.Node) bool {
	if node.Parent != nil && node.Parent.Type == html.ElementNode && node.Parent.Data == "label" {
		return true
	}
	if id == "" {
		return false
	}
	for _, candidate := range all {
		if candidate.Type != html.ElementNode || candidate.Data != "label" {
			continue
		}
		for _, attr := range candidate.Attr {
			if attr.Key == "for" && attr.Val == id {
				return true
			}
		}
	}
	return false
}

func TestMCPPublicProtocolAndAliases(t *testing.T) {
	store := tokenRouteStore(t)
	token, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	SetCORSOrigins([]string{"*"})
	SetServerFQDN("messages.example.test")
	t.Cleanup(func() { SetServerFQDN("") })
	handler := Handler("test-version")
	for _, path := range []string{"/api/v1/server/mcp", "/api/mcp", "/mcp"} {
		request := webTestRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
		request.Header.Set("Mcp-Method", "server/discover")
		request.Header.Set("Origin", "https://messages.example.test")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resultType":"complete"`) {
			t.Errorf("POST %s MCP discovery returned %d: %s", path, response.Code, response.Body.String())
		}
	}
	listRequest := webTestRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	listRequest.Header.Set("Authorization", "Bearer "+token)
	listRequest.Header.Set("Content-Type", "application/json")
	listRequest.Header.Set("Accept", "application/json, text/event-stream")
	listRequest.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	listRequest.Header.Set("Mcp-Method", "tools/list")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "build_commit_message") {
		t.Fatalf("MCP tool listing returned %d: %s", listResponse.Code, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), "graphql_query_get") || !strings.Contains(listResponse.Body.String(), "graphql_query") {
		t.Fatalf("MCP tool listing omitted GraphQL operations: %s", listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), "issue_primary_token") || strings.Contains(listResponse.Body.String(), "create_linked_token") {
		t.Fatalf("MCP tool listing exposed token management: %s", listResponse.Body.String())
	}
	callRequest := webTestRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"build_commit_message","arguments":{"type":"feat","description":"add mcp builder"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	callRequest.Header.Set("Authorization", "Bearer "+token)
	callRequest.Header.Set("Content-Type", "application/json")
	callRequest.Header.Set("Accept", "application/json, text/event-stream")
	callRequest.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	callRequest.Header.Set("Mcp-Method", "tools/call")
	callRequest.Header.Set("Mcp-Name", "build_commit_message")
	callResponse := httptest.NewRecorder()
	handler.ServeHTTP(callResponse, callRequest)
	if callResponse.Code != http.StatusOK || !strings.Contains(callResponse.Body.String(), "feat: add mcp builder") {
		t.Fatalf("MCP builder call returned %d: %s", callResponse.Code, callResponse.Body.String())
	}
	graphqlRequest := webTestRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"graphql","method":"tools/call","params":{"name":"graphql_query","arguments":{"query":"{ missingField }"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	graphqlRequest.Header.Set("Authorization", "Bearer "+token)
	graphqlRequest.Header.Set("Content-Type", "application/json")
	graphqlRequest.Header.Set("Accept", "application/json, text/event-stream")
	graphqlRequest.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	graphqlRequest.Header.Set("Mcp-Method", "tools/call")
	graphqlRequest.Header.Set("Mcp-Name", "graphql_query")
	graphqlResponse := httptest.NewRecorder()
	handler.ServeHTTP(graphqlResponse, graphqlRequest)
	if graphqlResponse.Code != http.StatusOK || !strings.Contains(graphqlResponse.Body.String(), `"isError":true`) {
		t.Fatalf("MCP GraphQL errors were not reported as tool errors: %d %s", graphqlResponse.Code, graphqlResponse.Body.String())
	}
	badHeader := webTestRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	badHeader.Header.Set("Authorization", "Bearer "+token)
	badHeader.Header.Set("Content-Type", "application/json")
	badHeader.Header.Set("Accept", "application/json, text/event-stream")
	badHeader.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	badHeader.Header.Set("Mcp-Method", "tools/list")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, badHeader)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Header mismatch") {
		t.Fatalf("mismatched MCP headers returned %d: %s", response.Code, response.Body.String())
	}
	blockedOrigin := webTestRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	blockedOrigin.Header.Set("Authorization", "Bearer "+token)
	blockedOrigin.Header.Set("Origin", "https://attacker.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, blockedOrigin)
	if response.Code != http.StatusForbidden {
		t.Fatalf("untrusted MCP origin returned %d", response.Code)
	}
}

func TestSwaggerAndBrowserReferenceCoverCurrentRoutes(t *testing.T) {
	metrics := &config.Get().Server.Metrics
	previousMetrics := *metrics
	metrics.Enabled = true
	metrics.Root.Enabled = true
	t.Cleanup(func() { *metrics = previousMetrics })
	handler := Handler("test-version")
	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, webTestRequest(http.MethodGet, "/server/docs/swagger", nil))
	if ui.Code != http.StatusOK || !strings.Contains(ui.Body.String(), `id="swagger-ui"`) || !strings.Contains(ui.Body.String(), `src="/static/js/docs-swagger.js?`) || strings.Contains(ui.Body.String(), "unpkg.com") || strings.Contains(ui.Body.String(), "cdn.jsdelivr.net") {
		t.Fatalf("Swagger page omitted its local interactive explorer: status %d", ui.Code)
	}
	script := httptest.NewRecorder()
	handler.ServeHTTP(script, webTestRequest(http.MethodGet, "/static/js/docs-swagger.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("Swagger UI initializer did not load: status %d", script.Code)
	}

	spec := httptest.NewRecorder()
	handler.ServeHTTP(spec, webTestRequest(http.MethodGet, "/api/v1/server/swagger", nil))
	for _, path := range []string{"/api/autodiscover", "/api/healthz", "/api/swagger", "/api/v1", "/api/v1/server/preferences", "/api/v1/server/security", "/api/v1/server/mcp", "/api/mcp", "/mcp"} {
		if !strings.Contains(spec.Body.String(), path) {
			t.Errorf("OpenAPI JSON omitted %s", path)
		}
	}
	var schema struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec.Body.Bytes(), &schema); err != nil {
		t.Fatalf("decode OpenAPI JSON: %v", err)
	}
	for _, path := range []string{
		"/api/v1/server/preferences/tokens", "/api/v1/server/preferences/tokens/primary",
		"/api/v1/server/preferences/tokens/primary/reveal", "/api/v1/server/preferences/tokens/primary/rotate",
		"/api/v1/server/preferences/tokens/{id}", "/api/v1/server/preferences/tokens/{id}/reveal",
		"/api/v1/server/preferences/tokens/{id}/rotate", "/api/v1/server/metrics",
		"/api/v1/server/metrics/prometheus", "/api/metrics/grafana", "/metrics/loki",
	} {
		if _, exists := schema.Paths[path]; !exists {
			t.Errorf("OpenAPI JSON omitted registered API path %s", path)
		}
	}
	operationIDs := make(map[string]string)
	for path, methods := range schema.Paths {
		for method, raw := range methods {
			var operation map[string]any
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("decode %s %s operation: %v", method, path, err)
			}
			operationID, _ := operation["operationId"].(string)
			if operationID == "" {
				t.Errorf("OpenAPI operation %s %s omitted operationId", method, path)
			} else if previousPath, exists := operationIDs[operationID]; exists {
				t.Errorf("OpenAPI operationId %q is shared by %s and %s", operationID, previousPath, path)
			} else {
				operationIDs[operationID] = path
			}
			security, hasSecurity := operation["security"].([]any)
			if strings.Contains(path, "tokens") && (!hasSecurity || len(security) == 0) && (path != "/api/v1/server/preferences/tokens/primary" || method != "post") {
				t.Errorf("protected token operation %s %s has no Bearer security", method, path)
			}
		}
	}
	if _, exists := schema.Paths["/graphql"]; exists {
		t.Fatal("OpenAPI JSON exposed the removed root GraphQL endpoint")
	}
	for _, path := range []string{"/api/graphql", "/api/v1/server/graphql"} {
		operations := schema.Paths[path]
		if _, exists := operations["post"]; !exists {
			t.Errorf("OpenAPI JSON omitted GraphQL POST at %s", path)
		}
		if _, exists := operations["get"]; exists {
			t.Errorf("OpenAPI JSON exposed unsupported GraphQL GET at %s", path)
		}
	}
	metrics.Auth.AllowUnauthenticated = true
	publicMetricsSpec := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(publicMetricsSpec, webTestRequest(http.MethodGet, "/api/v1/server/swagger", nil))
	var publicMetricsSchema struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(publicMetricsSpec.Body.Bytes(), &publicMetricsSchema); err != nil {
		t.Fatalf("decode unauthenticated metrics OpenAPI: %v", err)
	}
	if security, exists := publicMetricsSchema.Paths["/api/v1/server/metrics"]["get"]["security"]; exists {
		if requirements, ok := security.([]any); ok && len(requirements) > 0 {
			t.Fatal("OpenAPI requires Bearer authentication for publicly mounted metrics")
		}
	}
	removedGraphQL := httptest.NewRecorder()
	handler.ServeHTTP(removedGraphQL, webTestRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ types { type } }"}`)))
	if removedGraphQL.Code != http.StatusNotFound {
		t.Fatalf("removed root GraphQL route returned %d", removedGraphQL.Code)
	}
	graphQLPage := httptest.NewRecorder()
	handler.ServeHTTP(graphQLPage, webTestRequest(http.MethodGet, "/server/docs/graphql", nil))
	if !strings.Contains(graphQLPage.Body.String(), `id="graphiql"`) || !strings.Contains(graphQLPage.Body.String(), `src="/static/js/docs-graphql.js?`) || !strings.Contains(graphQLPage.Body.String(), `method="post" action="/api/v1/server/graphql"`) {
		t.Fatal("GraphiQL page omitted its interactive tool or no-JavaScript API form")
	}
	graphqlScript := httptest.NewRecorder()
	handler.ServeHTTP(graphqlScript, webTestRequest(http.MethodGet, "/static/js/docs-graphql.js", nil))
	if graphqlScript.Code != http.StatusOK || !strings.Contains(graphqlScript.Body.String(), "window.GraphiQL") {
		t.Fatal("GraphiQL initializer did not load")
	}
}

func TestOpenAPIRoutesMatchIDEAContractAndRouter(t *testing.T) {
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  api_version: v1\n  metrics:\n    enabled: true\n    root:\n      enabled: true\n    auth:\n      allow_unauthenticated: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load API route test config: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})

	idea, err := os.ReadFile("../../IDEA.md")
	if err != nil {
		t.Fatalf("read IDEA.md API contract: %v", err)
	}
	contract, err := parseIDEAAPIRoutes(string(idea), config.Get().Server.APIVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(contract) == 0 {
		t.Fatal("IDEA.md API route inventory is empty")
	}

	handler := Handler("test-version")
	specResponse := httptest.NewRecorder()
	handler.ServeHTTP(specResponse, webTestRequest(http.MethodGet, APIBasePath()+"/server/swagger", nil))
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(specResponse.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI route inventory: %v", err)
	}

	for _, route := range contract {
		if _, exists := document.Paths[route.path][strings.ToLower(route.method)]; !exists {
			t.Errorf("IDEA.md route %s %s is missing from OpenAPI", route.method, route.path)
		}
		body := io.Reader(nil)
		if route.method == http.MethodPost || route.method == http.MethodPatch || route.method == http.MethodPut {
			body = strings.NewReader("{")
		}
		request := webTestRequest(route.method, route.routerPath, body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusNotFound {
			t.Errorf("IDEA.md route %s %s is not registered in the router", route.method, route.path)
		}
	}
}

func TestEmbeddedOpenAPISupersetMatchesSourceDefinitions(t *testing.T) {
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	configuration := []byte("server:\n  api_version: v1\n  metrics:\n    enabled: true\n    root:\n      enabled: true\n    auth:\n      allow_unauthenticated: false\n")
	if err := os.WriteFile(configPath, configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load OpenAPI generation config: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})
	Handler("")
	request := httptest.NewRequest(http.MethodGet, "http://openapi.invalid/api/v1/server/swagger", nil)
	request.Header.Set("X-GitMessages-Language", "en")
	generated := BuildOpenAPISuperset(request)
	embedded, err := swaggerapi.Load("en")
	if err != nil {
		t.Fatalf("load embedded OpenAPI document: %v", err)
	}
	generatedPaths, err := json.Marshal(generated["paths"])
	if err != nil {
		t.Fatal(err)
	}
	embeddedPaths, err := json.Marshal(embedded["paths"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generatedPaths, embeddedPaths) {
		t.Fatal("embedded OpenAPI routes are stale; run go generate ./src/swagger")
	}
}

type ideaAPIRoute struct {
	method     string
	path       string
	routerPath string
}

func parseIDEAAPIRoutes(specification, apiVersion string) ([]ideaAPIRoute, error) {
	const heading = "### API routes"
	start := strings.Index(specification, heading)
	if start < 0 {
		return nil, fmt.Errorf("IDEA.md has no %q subsection", heading)
	}
	section := specification[start+len(heading):]
	if next := strings.Index(section, "\n### "); next >= 0 {
		section = section[:next]
	}
	routes := make([]ideaAPIRoute, 0)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		columns := strings.Split(line, "|")
		if len(columns) < 4 || strings.TrimSpace(columns[1]) == "Method" || strings.Contains(columns[1], "---") {
			continue
		}
		methods := strings.Split(strings.TrimSpace(columns[1]), ",")
		paths := strings.Split(strings.TrimSpace(columns[2]), ",")
		for _, rawPath := range paths {
			path := strings.Trim(strings.TrimSpace(rawPath), "`")
			path = strings.ReplaceAll(path, "{api_version}", apiVersion)
			routerPath := strings.ReplaceAll(path, "{id}", "sample-id")
			variants := []string{path}
			routerVariants := []string{routerPath}
			if strings.Contains(path, "[/{service}]") {
				base := strings.Replace(path, "[/{service}]", "", 1)
				variants = []string{base, base + "/prometheus", base + "/grafana", base + "/loki"}
				routerVariants = variants
			}
			for _, method := range methods {
				for index, variant := range variants {
					routes = append(routes, ideaAPIRoute{method: strings.TrimSpace(method), path: variant, routerPath: routerVariants[index]})
				}
			}
		}
	}
	return routes, nil
}

func TestMCPHelpPageAndAPIStayAligned(t *testing.T) {
	handler := Handler("test-version")
	request := webTestRequest(http.MethodGet, "/server/help/mcp", nil)
	request.Host = "docs.example.test"
	mcpURL := BuildURL(request, APIBasePath()+"/server/mcp")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, request)
	for _, expected := range []string{`id="overview"`, `id="endpoint"`, `id="authentication"`, `id="connect"`, `id="tools"`, `id="limits"`, `id="troubleshooting"`, `id="project"`, "build_commit_message", "claude mcp add --transport http gitmessages " + mcpURL, "Mcp-Method: tools/list", `data-copy-text="` + BuildURL(request, "/mcp") + `"`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Errorf("MCP help page missing %q: %d", expected, page.Code)
		}
	}
	if strings.Contains(page.Body.String(), "%!") {
		t.Fatalf("MCP help page contains a formatting error: %s", page.Body.String())
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, webTestRequest(http.MethodGet, "/api/v1/server/help/mcp", nil))
	for _, expected := range []string{"/api/v1/server/mcp", "list_commit_types", "build_commit_message", `"scheme": "Bearer"`, `"project"`} {
		if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), expected) {
			t.Errorf("MCP help API missing %q: %d", expected, api.Code)
		}
	}
}

func TestServerRootRedirectsToAbout(t *testing.T) {
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, webTestRequest(http.MethodGet, "/server", nil))
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil || response.Code != http.StatusMovedPermanently || location.Path != "/server/about" {
		t.Fatalf("/server redirect = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestCCPAOptOutAndOptInForm(t *testing.T) {
	tokenRouteStore(t)
	privacy := &config.Get().Server.Privacy
	original := privacy.Data.Sold
	privacy.Data.Sold = true
	t.Cleanup(func() { privacy.Data.Sold = original })

	pageRequest := webTestRequest(http.MethodGet, "/server/privacy", nil)
	page := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/server/ccpa"`) || !strings.Contains(page.Body.String(), `name="choice" value="opt-out"`) {
		t.Fatalf("privacy page omitted the CCPA opt-out form: %d %s", page.Code, page.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil {
		t.Fatal("privacy page did not set its CSRF cookie")
	}

	for _, choice := range []string{"opt-out", "opt-in"} {
		form := url.Values{"csrf_token": {csrfCookie.Value}, "choice": {choice}}
		request := webTestRequest(http.MethodPost, "/server/ccpa", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(csrfCookie)
		response := httptest.NewRecorder()
		Handler("test-version").ServeHTTP(response, request)
		cookie := response.Result().Cookies()
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/server/privacy#ccpa-opt-out" || len(cookie) == 0 || cookie[0].Name != "ccpa_opt_out" {
			t.Fatalf("CCPA %s response = %d %q cookies=%#v", choice, response.Code, response.Header().Get("Location"), cookie)
		}
		if (choice == "opt-out") != (cookie[0].Value == "true") {
			t.Errorf("CCPA %s cookie value = %q", choice, cookie[0].Value)
		}
	}
}

func TestSecurityPageMirrorAndSecurityTxt(t *testing.T) {
	handler := Handler("test-version")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/server/security?lang=es", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), i18n.T("es", "security.body")) {
		t.Fatalf("localized security page returned %d: %s", page.Code, page.Body.String())
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, webTestRequest(http.MethodGet, "/api/v1/server/security?lang=es", nil))
	if api.Code != http.StatusOK {
		t.Fatalf("security API mirror returned %d: %s", api.Code, api.Body.String())
	}
	for _, field := range []string{`"contacts"`, `"policy_url"`, `"thanks_url"`, `/server/security/policy`} {
		if !bytes.Contains(api.Body.Bytes(), []byte(field)) {
			t.Errorf("security API mirror omitted %s: %s", field, api.Body.String())
		}
	}
	text := httptest.NewRecorder()
	handler.ServeHTTP(text, webTestRequest(http.MethodGet, "/.well-known/security.txt", nil))
	for _, expected := range []string{"Contact: https://github.com/apimgr/gitmessages/security/advisories/new", "Expires:", "/server/security/policy"} {
		if text.Code != http.StatusOK || !strings.Contains(text.Body.String(), expected) {
			t.Errorf("security.txt missing %q: %s", expected, text.Body.String())
		}
	}
	method := httptest.NewRecorder()
	handler.ServeHTTP(method, webTestRequest(http.MethodPost, "/.well-known/security.txt", nil))
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("security.txt POST returned %d with Allow %q", method.Code, method.Header().Get("Allow"))
	}
}

func TestTextAPIRepresentations(t *testing.T) {
	handler := Handler("test-version")
	routes := []struct {
		path   string
		method string
		body   string
	}{
		{"/api/v1.txt", http.MethodGet, ""},
		{"/api/v1/types.txt", http.MethodGet, ""},
		{"/api/v1/conventions.txt", http.MethodGet, ""},
		{"/api/v1/templates.txt", http.MethodGet, ""},
		{"/api/v1/templates/random.txt", http.MethodGet, ""},
		{"/api/v1/messages/random.txt", http.MethodGet, ""},
		{"/api/v1/messages.txt", http.MethodPost, `{"type":"feat","description":"add text output"}`},
		{"/api/v1/server/healthz.txt", http.MethodGet, ""},
		{"/api/v1/server/about.txt", http.MethodGet, ""},
		{"/api/v1/server/help.txt", http.MethodGet, ""},
		{"/api/v1/server/privacy.txt", http.MethodGet, ""},
		{"/api/v1/server/terms.txt", http.MethodGet, ""},
		{"/api/v1/server/preferences.txt", http.MethodGet, ""},
		{"/api/v1/server/swagger.txt", http.MethodGet, ""},
	}
	for _, route := range routes {
		request := webTestRequest(route.method, route.path, strings.NewReader(route.body))
		request.Header.Set("Accept", "text/plain")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("%s %s returned %d: %s", route.method, route.path, response.Code, response.Body.String())
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("%s content type = %q", route.path, response.Header().Get("Content-Type"))
		}
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, webTestRequest(http.MethodGet, "/api/v1/unknown.txt", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown text route returned %d", unknown.Code)
	}
}

func TestCORSUsesConfiguredOrigins(t *testing.T) {
	SetCORSOrigins([]string{"https://docs.example.test"})
	t.Cleanup(func() { SetCORSOrigins([]string{"*"}) })
	handler := Handler("test-version")

	allowed := webTestRequest(http.MethodGet, "/api/v1/types", nil)
	allowed.Header.Set("Origin", "https://docs.example.test")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if got := allowedResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://docs.example.test" {
		t.Fatalf("allowed origin header = %q", got)
	}

	denied := webTestRequest(http.MethodGet, "/api/v1/types", nil)
	denied.Header.Set("Origin", "https://attacker.example.test")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if got := deniedResponse.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied origin header = %q, want empty", got)
	}
}

func TestServerPolicyPagesHaveMatchingAPIResponses(t *testing.T) {
	handler := Handler("test-version")
	for _, route := range []struct {
		path     string
		expected string
	}{
		{path: "/api/v1/server/privacy", expected: `"data_stored_on_server"`},
		{path: "/api/v1/server/terms", expected: i18n.T("en", "terms.title")},
	} {
		request := webTestRequest(http.MethodGet, route.path, nil)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(route.expected)) {
			t.Errorf("%s response = %d %s", route.path, response.Code, response.Body.String())
		}
	}
}

func TestHealthIncludesOverlayState(t *testing.T) {
	SetOverlayStatus(func() map[string]any {
		return map[string]any{"tor": map[string]string{"status": "healthy"}, "i2p": map[string]string{"status": "starting"}}
	})
	t.Cleanup(func() { SetOverlayStatus(nil) })
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, webTestRequest(http.MethodGet, "/api/v1/server/healthz", nil))
	for _, expected := range []string{`"features"`, `"tor"`, `"i2p"`, `"healthy"`, `"starting"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Errorf("health response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestLocaleEndpointAndAccessibleLandmarks(t *testing.T) {
	handler := Handler("test-version")
	localeResponse := httptest.NewRecorder()
	handler.ServeHTTP(localeResponse, webTestRequest(http.MethodGet, "/locales/it.json", nil))
	var locale struct {
		Meta struct {
			NativeName string `json:"native_name"`
		} `json:"meta"`
	}
	if localeResponse.Code != http.StatusOK || json.Unmarshal(localeResponse.Body.Bytes(), &locale) != nil || locale.Meta.NativeName != "Italiano" {
		t.Fatalf("Italian locale endpoint returned %d: %s", localeResponse.Code, localeResponse.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, webTestRequest(http.MethodGet, "/locales/unsupported.json", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("invalid locale path status = %d", missing.Code)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/", nil))
	body := page.Body.String()
	for _, required := range []string{`href="#main-content"`, `href="#navigation"`, `role="banner"`, `class="nav-links"`, `role="main"`, `class="page-title"`, `href="/server/preferences#language"`} {
		if !strings.Contains(body, required) {
			t.Errorf("page is missing accessible markup %q", required)
		}
	}
	if strings.Contains(body, `<select id="lang"`) {
		t.Fatal("language selection should be on the preferences page, not in the header")
	}
}

func TestOfflineServiceWorkerUsesRequestLocale(t *testing.T) {
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, webTestRequest(http.MethodGet, "/sw.js?lang=fr", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Cette page n") || !strings.Contains(response.Body.String(), "pas disponible hors ligne.") {
		t.Fatalf("service worker did not include French offline text: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "This page is not available offline.") {
		t.Fatal("service worker contains hardcoded English offline text")
	}
}

func TestThemePreferenceUsesCookieAndServerRenderedMarkup(t *testing.T) {
	handler := Handler("test-version")
	for _, test := range []struct {
		name   string
		cookie string
		want   string
	}{
		{name: "default", want: `data-theme="dark"`},
		{name: "light", cookie: "light", want: `data-theme="light"`},
		{name: "auto", cookie: "auto", want: `data-theme="auto"`},
		{name: "invalid", cookie: "bogus", want: `data-theme="dark"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := webTestRequest(http.MethodGet, "/", nil)
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: "theme", Value: test.cookie})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("rendered theme missing %q", test.want)
			}
		})
	}
	SetDefaultTheme("light")
	t.Cleanup(func() { SetDefaultTheme("dark") })
	configured := httptest.NewRecorder()
	handler.ServeHTTP(configured, webTestRequest(http.MethodGet, "/", nil))
	if !strings.Contains(configured.Body.String(), `data-theme="light"`) {
		t.Fatal("configured default theme was not rendered")
	}

	script := httptest.NewRecorder()
	handler.ServeHTTP(script, webTestRequest(http.MethodGet, "/static/js/app.js", nil))
	if !strings.Contains(script.Body.String(), "cookie-preferences") || !strings.Contains(script.Body.String(), "navigator.serviceWorker") {
		t.Fatal("shared app script omitted preferences or PWA behavior")
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/", nil))
	if !strings.Contains(page.Body.String(), `class="theme-toggle-form" method="post" action="/server/preferences"`) || !strings.Contains(page.Body.String(), `name="theme" value="auto"`) {
		t.Fatal("header theme toggle form is missing")
	}
	for current, expected := range map[string]string{"dark": "light", "light": "auto", "auto": "dark"} {
		if actual := nextTheme(current); actual != expected {
			t.Errorf("nextTheme(%q) = %q, want %q", current, actual, expected)
		}
	}
}

func TestPreferencesPageAndAPI(t *testing.T) {
	tokenRouteStore(t)
	handler := Handler("test-version")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/server/preferences", nil))
	if strings.Contains(page.Body.String(), `id="cookie-consent"`) {
		t.Fatal("preferences page rendered a separate consent banner")
	}
	if got := strings.Count(page.Body.String(), `method="post" action="/server/preferences"`); got != 1 {
		t.Fatalf("preferences page has %d preference POST forms, want the single page form", got)
	}
	if strings.Contains(page.Body.String(), `class="theme-toggle-form"`) {
		t.Fatal("preferences page rendered the separate header theme form")
	}
	for _, expected := range []string{`method="post" action="/server/preferences"`, `name="lang"`, `value="auto"`, `Automatic (English)`, `name="theme"`, `Preferences`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Errorf("preferences page missing %q: %d %s", expected, page.Code, page.Body.String())
		}
	}

	var csrfCookie *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil {
		t.Fatal("preferences page did not set its CSRF cookie")
	}
	preferencesPost := webTestRequest(http.MethodPost, "/server/preferences", strings.NewReader("csrf_token="+csrfCookie.Value+"&consent_form=1&consent_preferences=on&lang=fr&theme=light"))
	preferencesPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	preferencesPost.AddCookie(csrfCookie)
	preferencesResponse := httptest.NewRecorder()
	handler.ServeHTTP(preferencesResponse, preferencesPost)
	if preferencesResponse.Code != http.StatusSeeOther || preferencesResponse.Header().Get("Location") != "/server/preferences" {
		t.Fatalf("preferences POST returned %d: %s", preferencesResponse.Code, preferencesResponse.Body.String())
	}
	var hasLanguageCookie, hasThemePreference bool
	var themeCookie *http.Cookie
	for _, cookie := range preferencesResponse.Result().Cookies() {
		hasLanguageCookie = hasLanguageCookie || cookie.Name == "lang" && cookie.Value == "fr" && !cookie.HttpOnly
		hasThemePreference = hasThemePreference || cookie.Name == "theme" && cookie.Value == "light"
		if cookie.Name == "theme" {
			themeCookie = cookie
		}
	}
	if !hasLanguageCookie || !hasThemePreference {
		t.Fatalf("preferences POST did not save both choices: %#v", preferencesResponse.Result().Cookies())
	}
	postTheme := webTestRequest(http.MethodPost, "/server/preferences", strings.NewReader("csrf_token="+csrfCookie.Value+"&theme=dark&redirect=%2Fserver%2Fabout"))
	postTheme.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postTheme.AddCookie(csrfCookie)
	for _, cookie := range preferencesResponse.Result().Cookies() {
		if cookie.Name == "cookie_consent" {
			postTheme.AddCookie(cookie)
		}
	}
	posted := httptest.NewRecorder()
	handler.ServeHTTP(posted, postTheme)
	if posted.Code != http.StatusSeeOther || posted.Header().Get("Location") != "/server/about" {
		t.Fatalf("server-side theme toggle response = %d %q", posted.Code, posted.Header().Get("Location"))
	}
	if cookies := posted.Result().Cookies(); len(cookies) == 0 || cookies[0].Name != "theme" || cookies[0].Value != "dark" {
		t.Fatalf("server-side theme toggle did not save the selected theme: %#v", cookies)
	}

	apiRequest := webTestRequest(http.MethodGet, "/api/v1/server/preferences", nil)
	apiRequest.AddCookie(themeCookie)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusOK || !strings.Contains(apiResponse.Body.String(), `"theme": "light"`) {
		t.Fatalf("preferences API returned %d: %s", apiResponse.Code, apiResponse.Body.String())
	}
}

func TestGraphQLBrowserFormReturnsHTMLDocument(t *testing.T) {
	request := webTestRequest(http.MethodPost, "/api/v1/server/graphql", strings.NewReader("query=%7B+types+%7B+type+%7D+%7D"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") || !strings.Contains(response.Body.String(), `&#34;feat&#34;`) || strings.Contains(response.Body.String(), `id="graphiql"`) {
		t.Fatalf("GraphQL form browser response = %d %q: %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestPreferencesResetPreservesCredentialCookies(t *testing.T) {
	tokenRouteStore(t)
	handler := Handler("test-version")
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, webTestRequest(http.MethodGet, "/server/preferences", nil))
	var csrf *http.Cookie
	for _, cookie := range pageResponse.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("preferences page omitted its CSRF cookie")
	}
	request := webTestRequest(http.MethodPost, "/server/preferences", strings.NewReader("csrf_token="+url.QueryEscape(csrf.Value)+"&action=reset"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrf)
	request.AddCookie(&http.Cookie{Name: ownerCookieName, Value: "owner-credential"})
	request.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	request.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: "consent"})
	request.AddCookie(&http.Cookie{Name: "ccpa_opt_out", Value: "true"})
	request.AddCookie(&http.Cookie{Name: "gitmessages_build", Value: "old-build"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("Reset response = %d Clear-Site-Data=%q", response.Code, response.Header().Get("Clear-Site-Data"))
	}
	deleted := make(map[string]bool)
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			deleted[cookie.Name] = true
		}
	}
	for _, name := range []string{"theme", "lang", "cookie_consent", "ccpa_opt_out", "gitmessages_build"} {
		if !deleted[name] {
			t.Errorf("Reset did not delete %q", name)
		}
	}
	if deleted[ownerCookieName] || deleted[csrfCookieName] {
		t.Fatalf("Reset deleted a credential cookie: %#v", response.Result().Cookies())
	}
}

func TestURLNormalizationAndServerPageMethods(t *testing.T) {
	handler := Handler("test-version")
	trailing := httptest.NewRecorder()
	handler.ServeHTTP(trailing, webTestRequest(http.MethodGet, "/server/security/?lang=fr", nil))
	if trailing.Code != http.StatusMovedPermanently || trailing.Header().Get("Location") != "/server/security?lang=fr" {
		t.Fatalf("trailing slash response = %d %q", trailing.Code, trailing.Header().Get("Location"))
	}
	postTrailing := httptest.NewRecorder()
	handler.ServeHTTP(postTrailing, webTestRequest(http.MethodPost, "/server/security/?lang=fr", strings.NewReader("report=private")))
	if postTrailing.Code != http.StatusPermanentRedirect || postTrailing.Header().Get("Location") != "/server/security?lang=fr" {
		t.Fatalf("POST trailing slash response = %d %q", postTrailing.Code, postTrailing.Header().Get("Location"))
	}
	filePath := httptest.NewRecorder()
	handler.ServeHTTP(filePath, webTestRequest(http.MethodGet, "/assets/site.css/", nil))
	if filePath.Code == http.StatusMovedPermanently {
		t.Fatal("file-like path was normalized")
	}
	post := httptest.NewRecorder()
	handler.ServeHTTP(post, webTestRequest(http.MethodPost, "/server/security", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST server page response = %d Allow=%q", post.Code, post.Header().Get("Allow"))
	}
}

func TestPublicPagesHaveNavigationAndMainLandmarks(t *testing.T) {
	handler := Handler("test-version")
	for _, path := range []string{"/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/healthz", "/server/privacy", "/server/terms", "/server/security", "/server/preferences", "/server/docs/swagger", "/server/docs/graphql"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, webTestRequest(http.MethodGet, path, nil))
			body := response.Body.String()
			for _, marker := range []string{`href="#main-content"`, `href="#navigation"`, `id="navigation"`, `role="main"`, `class="page-title"`} {
				if !strings.Contains(body, marker) {
					t.Errorf("page lacks required accessibility marker %q", marker)
				}
			}
		})
	}
}

func TestPagesUseExternalStylesheetAndRestrictiveCSP(t *testing.T) {
	response := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(response, webTestRequest(http.MethodGet, "/", nil))
	if !bytes.Contains(response.Body.Bytes(), []byte(`<link rel="stylesheet" href="/styles.css?v=test-version-unknown">`)) {
		t.Fatal("page does not link the embedded stylesheet")
	}
	if bytes.Contains(response.Body.Bytes(), []byte("<style")) {
		t.Fatal("page contains inline CSS")
	}
	policy := response.Header().Get("Content-Security-Policy")
	if strings.Contains(policy, "unsafe-inline") || !strings.Contains(policy, "style-src 'self'") || !strings.Contains(policy, "font-src 'self' data: https:") {
		t.Fatalf("CSP does not match the required style and font policy: %q", policy)
	}
}

func TestSecurityHeadersAndRequestID(t *testing.T) {
	handler := Handler("test-version")
	request := webTestRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	requestID := response.Header().Get("X-Request-ID")
	if len(requestID) != 36 || strings.Count(requestID, "-") != 4 {
		t.Fatalf("invalid request ID %q", requestID)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "SAMEORIGIN",
		"X-XSS-Protection":                  "1; mode=block",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Origin-Agent-Cluster":              "?1",
		"Cross-Origin-Opener-Policy":        "unsafe-none",
		"Cross-Origin-Embedder-Policy":      "unsafe-none",
		"Cross-Origin-Resource-Policy":      "cross-origin",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := response.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS sent over HTTP: %q", got)
	}

	tlsRequest := webTestRequest(http.MethodGet, "/missing", nil)
	tlsRequest.TLS = &tls.ConnectionState{}
	tlsResponse := httptest.NewRecorder()
	handler.ServeHTTP(tlsResponse, tlsRequest)
	if got := tlsResponse.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains; preload" {
		t.Errorf("TLS HSTS = %q", got)
	}
}

func TestVersionedAssetCachingAndPurge(t *testing.T) {
	handler := Handler("1.2.3", "abc1234")
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, webTestRequest(http.MethodGet, "/styles.css?v=1.2.3-abc1234", nil))
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("versioned asset cache policy = %q", got)
	}
	etag := asset.Header().Get("ETag")
	if etag != `"1.2.3-abc1234"` {
		t.Fatalf("asset ETag = %q", etag)
	}
	conditional := webTestRequest(http.MethodGet, "/styles.css?v=1.2.3-abc1234", nil)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("conditional asset status = %d, want 304", notModified.Code)
	}
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, webTestRequest(http.MethodGet, "/styles.css", nil))
	if got := stale.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("unstamped asset cache policy = %q", got)
	}
	manifest := httptest.NewRecorder()
	handler.ServeHTTP(manifest, webTestRequest(http.MethodGet, "/manifest.json", nil))
	if got := manifest.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("manifest cache policy = %q", got)
	}
	for _, marker := range []string{`"display":"standalone"`, `"sizes":"192x192"`, `"sizes":"512x512"`, `"purpose":"maskable"`} {
		if !strings.Contains(manifest.Body.String(), marker) {
			t.Errorf("manifest omitted PWA marker %q", marker)
		}
	}

	page := webTestRequest(http.MethodGet, "/", nil)
	page.AddCookie(&http.Cookie{Name: "gitmessages_build", Value: "old-build"})
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, page)
	if got := pageResponse.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Fatalf("build change purge header = %q", got)
	}
	imageRequest := webTestRequest(http.MethodGet, "/", nil)
	imageRequest.AddCookie(&http.Cookie{Name: "gitmessages_build", Value: "old-build"})
	imageRequest.Header.Set("Sec-Fetch-Dest", "image")
	imageRequest.Header.Set("Sec-Fetch-Mode", "no-cors")
	imageResponse := httptest.NewRecorder()
	handler.ServeHTTP(imageResponse, imageRequest)
	if got := imageResponse.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("subresource build purge header = %q, want none", got)
	}
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, webTestRequest(http.MethodGet, "/api/v1/types", nil))
	if got := apiResponse.Header().Get("X-Build"); got != "1.2.3-abc1234" {
		t.Fatalf("API X-Build = %q, want current build stamp", got)
	}
	for _, marker := range []string{`rel="apple-touch-icon"`, `name="apple-mobile-web-app-capable"`, `id="pwa-install-button"`} {
		if !strings.Contains(pageResponse.Body.String(), marker) {
			t.Errorf("page omitted PWA install marker %q", marker)
		}
	}
	installScript := httptest.NewRecorder()
	handler.ServeHTTP(installScript, webTestRequest(http.MethodGet, "/static/js/app.js", nil))
	for _, marker := range []string{"beforeinstallprompt", "appinstalled", "display-mode: standalone", "hasDirtyForm", "X-Build", "navigator.serviceWorker.register"} {
		if !strings.Contains(installScript.Body.String(), marker) {
			t.Errorf("PWA script omitted install behavior %q", marker)
		}
	}
}

func TestPWAIconAndSplashDimensions(t *testing.T) {
	handler := Handler("1.2.3", "abc1234")
	for _, test := range []struct {
		path   string
		width  int
		height int
	}{
		{path: "/static/icons/favicon-16.png", width: 16, height: 16},
		{path: "/static/icons/favicon-32.png", width: 32, height: 32},
		{path: "/static/icons/icon-72.png", width: 72, height: 72},
		{path: "/static/icons/icon-96.png", width: 96, height: 96},
		{path: "/static/icons/icon-128.png", width: 128, height: 128},
		{path: "/static/icons/icon-144.png", width: 144, height: 144},
		{path: "/static/icons/icon-152.png", width: 152, height: 152},
		{path: "/static/icons/icon-180.png", width: 180, height: 180},
		{path: "/static/icons/icon-192.png", width: 192, height: 192},
		{path: "/static/icons/icon-384.png", width: 384, height: 384},
		{path: "/static/icons/icon-512.png", width: 512, height: 512},
		{path: "/static/icons/icon-maskable-192.png", width: 192, height: 192},
		{path: "/static/icons/icon-maskable-512.png", width: 512, height: 512},
		{path: "/static/splash/iphone-1179x2556.png", width: 1179, height: 2556},
		{path: "/static/splash/iphone-1284x2778.png", width: 1284, height: 2778},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, webTestRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("GET %s returned %d with %q", test.path, response.Code, response.Header().Get("Content-Type"))
		}
		configuration, err := png.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
		if err != nil {
			t.Fatalf("decode %s: %v", test.path, err)
		}
		if configuration.Width != test.width || configuration.Height != test.height {
			t.Errorf("%s dimensions = %dx%d, want %dx%d", test.path, configuration.Width, configuration.Height, test.width, test.height)
		}
	}
}

func TestBuilderEndpoint(t *testing.T) {
	handler := Handler("test-version")
	requestBody := []byte(`{"type":"feat","scope":"api","description":"add search"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, webTestRequest(http.MethodPost, "/api/v1/messages", bytes.NewReader(requestBody)))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("feat(api): add search")) {
		t.Fatalf("valid builder response: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, webTestRequest(http.MethodPost, "/api/v1/messages", bytes.NewBufferString(`{"type":"unknown","description":"bad"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid builder status = %d", recorder.Code)
	}
}

func TestBuilderFormAndAPIMethods(t *testing.T) {
	handler := Handler("test-version")
	recorder := httptest.NewRecorder()
	request := webTestRequest(http.MethodPost, "/builder", bytes.NewBufferString("type=fix&scope=parser&description=handle+empty+input"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("fix(parser): handle empty input")) {
		t.Fatalf("builder form response: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, webTestRequest(http.MethodPost, "/api/v1/messages", bytes.NewBufferString(`{"type":"fix","description":"ok"} {}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, webTestRequest(http.MethodPost, "/api/v1/types", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("types method status = %d", recorder.Code)
	}
}

func TestLanguageSelection(t *testing.T) {
	previous := os.Getenv("GITMESSAGES_LANG")
	t.Cleanup(func() { _ = os.Setenv("GITMESSAGES_LANG", previous) })
	_ = os.Setenv("GITMESSAGES_LANG", "de")
	handler := Handler("test-version")
	configuredRequest := webTestRequest(http.MethodGet, "/", nil)
	configuredResponse := httptest.NewRecorder()
	handler.ServeHTTP(configuredResponse, configuredRequest)
	if !bytes.Contains(configuredResponse.Body.Bytes(), []byte(`<html lang="de" dir="ltr"`)) {
		t.Fatalf("configured default language was not applied: %s", configuredResponse.Body.String())
	}
	request := webTestRequest(http.MethodGet, "/?lang=ar", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`<html lang="ar" dir="rtl"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte("استكشف")) {
		t.Fatalf("Arabic page was not localized: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "lang" {
			t.Fatalf("language preference persisted without consent: %#v", cookie)
		}
	}

	request = webTestRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "fr-CA,fr;q=0.9,en;q=0.8")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`<html lang="fr" dir="ltr"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte("Parcourir")) {
		t.Fatalf("Accept-Language was not applied: %s", recorder.Body.String())
	}
}

func TestLocalizedCatalogAPI(t *testing.T) {
	request := webTestRequest(http.MethodGet, "/api/v1/types?lang=es", nil)
	recorder := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("Una nueva funcionalidad")) {
		t.Fatalf("catalog API was not localized: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTextRepresentations(t *testing.T) {
	handler := Handler("test-version")
	cases := []struct {
		name        string
		path        string
		userAgent   string
		accept      string
		contentType string
		contains    string
	}{
		{name: "plain text API extension", path: "/api/v1/types.txt", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "plain text API accept", path: "/api/v1/types", accept: "text/plain", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "curl browser page", path: "/types", userAgent: "curl/8.0", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "JSON wins for curl", path: "/api/v1/types", userAgent: "curl/8.0", accept: "application/json", contentType: "application/json; charset=utf-8", contains: `"ok": true`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := webTestRequest(http.MethodGet, test.path, nil)
			if test.userAgent != "" {
				request.Header.Set("User-Agent", test.userAgent)
			}
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != test.contentType || !bytes.Contains(recorder.Body.Bytes(), []byte(test.contains)) {
				t.Errorf("status=%d content-type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
			}
		})
	}
}
