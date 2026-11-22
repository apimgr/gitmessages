package web

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/apimgr/gitmessages/src/common/i18n"
)

var webTestRequestSequence atomic.Uint32

func webTestRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	sequence := webTestRequestSequence.Add(1)
	request.RemoteAddr = fmt.Sprintf("198.18.%d.%d:1234", (sequence>>8)&0xff, sequence&0xff)
	return request
}

func TestPublicPagesAndAPI(t *testing.T) {
	SetCORSOrigins([]string{"*"})
	handler := Handler("test-version")
	for _, path := range []string{"/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/help/mcp", "/server/healthz", "/server/privacy", "/server/terms", "/server/security", "/server/preferences", "/.well-known/security.txt", "/server/docs/swagger", "/server/docs/graphql", "/manifest.json", "/icon.svg", "/styles.css", "/locales/en.json", "/sw.js", "/graphql.js", "/theme.js", "/api/v1", "/api/v1/types", "/api/v1/conventions", "/api/v1/templates", "/api/v1/templates/random", "/api/v1/messages/random", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/help/mcp", "/api/v1/server/privacy", "/api/v1/server/terms", "/api/v1/server/security", "/api/v1/server/preferences", "/api/v1/server/swagger"} {
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
	if !strings.Contains(listResponse.Body.String(), "graphql_query_get") || !strings.Contains(listResponse.Body.String(), "graphql_query") {
		t.Fatalf("MCP tool listing omitted GraphQL operations: %s", listResponse.Body.String())
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
	handler := Handler("test-version")
	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, webTestRequest(http.MethodGet, "/server/docs/swagger", nil))
	for _, path := range []string{"/api/v1/templates/random", "/api/v1/server/preferences", "/api/v1/server/security", "/api/v1/server/healthz", "/api/v1/server/mcp", "/server/help/mcp"} {
		if !strings.Contains(ui.Body.String(), path) {
			t.Errorf("Swagger browser page omitted %s", path)
		}
	}

	spec := httptest.NewRecorder()
	handler.ServeHTTP(spec, webTestRequest(http.MethodGet, "/api/v1/server/swagger", nil))
	for _, path := range []string{"/api/v1", "/api/v1/server/preferences", "/api/v1/server/security", "/api/v1/server/mcp", "/api/mcp", "/mcp"} {
		if !strings.Contains(spec.Body.String(), path) {
			t.Errorf("OpenAPI JSON omitted %s", path)
		}
	}
}

func TestMCPHelpPageAndAPIStayAligned(t *testing.T) {
	handler := Handler("test-version")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/server/help/mcp", nil))
	for _, expected := range []string{`id="overview"`, `id="endpoint"`, `id="authentication"`, `id="connect"`, `id="tools"`, `id="limits"`, `id="troubleshooting"`, `id="project"`, "build_commit_message", `data-copy-text="/mcp"`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Errorf("MCP help page missing %q: %d", expected, page.Code)
		}
	}
	if strings.Contains(page.Body.String(), "%!") {
		t.Fatalf("MCP help page contains a formatting error: %s", page.Body.String())
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, webTestRequest(http.MethodGet, "/api/v1/server/help/mcp", nil))
	for _, expected := range []string{"/api/v1/server/mcp", "list_commit_types", "build_commit_message"} {
		if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), expected) {
			t.Errorf("MCP help API missing %q: %d", expected, api.Code)
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
	for _, field := range []string{`"contacts"`, `"policy_url"`, `"thanks_url"`} {
		if !bytes.Contains(api.Body.Bytes(), []byte(field)) {
			t.Errorf("security API mirror omitted %s: %s", field, api.Body.String())
		}
	}
	text := httptest.NewRecorder()
	handler.ServeHTTP(text, webTestRequest(http.MethodGet, "/.well-known/security.txt", nil))
	for _, expected := range []string{"Contact: https://github.com/apimgr/gitmessages/security/advisories/new", "Expires:", "Policy:"} {
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
		path string
		key  string
	}{
		{path: "/api/v1/server/privacy", key: "privacy.title"},
		{path: "/api/v1/server/terms", key: "terms.title"},
	} {
		request := webTestRequest(http.MethodGet, route.path, nil)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(i18n.T("en", route.key))) {
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
	if localeResponse.Code != http.StatusOK || !strings.Contains(localeResponse.Body.String(), `"native_name":"Italiano"`) {
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
	handler.ServeHTTP(script, webTestRequest(http.MethodGet, "/theme.js", nil))
	if strings.Contains(script.Body.String(), "localStorage") || strings.Contains(script.Body.String(), "document.cookie") {
		t.Fatal("theme changes must work through the server form without JavaScript")
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/", nil))
	if !strings.Contains(page.Body.String(), `action="/server/preferences/theme"`) {
		t.Fatal("header theme toggle form is missing")
	}
}

func TestPreferencesPageAndAPI(t *testing.T) {
	handler := Handler("test-version")
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, webTestRequest(http.MethodGet, "/server/preferences", nil))
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
	preferencesPost := webTestRequest(http.MethodPost, "/server/preferences", strings.NewReader("csrf_token="+csrfCookie.Value+"&consent_form=1&lang=fr&theme=light"))
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
	postTheme := webTestRequest(http.MethodPost, "/server/preferences/theme", strings.NewReader("csrf_token="+csrfCookie.Value+"&theme=dark&return_to=%2Fserver%2Fabout"))
	postTheme.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postTheme.AddCookie(csrfCookie)
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

func TestURLNormalizationAndServerPageMethods(t *testing.T) {
	handler := Handler("test-version")
	trailing := httptest.NewRecorder()
	handler.ServeHTTP(trailing, webTestRequest(http.MethodGet, "/server/security/?lang=fr", nil))
	if trailing.Code != http.StatusMovedPermanently || trailing.Header().Get("Location") != "/server/security?lang=fr" {
		t.Fatalf("trailing slash response = %d %q", trailing.Code, trailing.Header().Get("Location"))
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
	if strings.Contains(policy, "unsafe-inline") || !strings.Contains(policy, "style-src 'self'") {
		t.Fatalf("CSP does not restrict styles to same-origin assets: %q", policy)
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

	page := webTestRequest(http.MethodGet, "/", nil)
	page.AddCookie(&http.Cookie{Name: "gitmessages_build", Value: "old-build"})
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, page)
	if got := pageResponse.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Fatalf("build change purge header = %q", got)
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
	if cookie := recorder.Result().Cookies(); len(cookie) == 0 || cookie[0].Name != "lang" || cookie[0].Value != "ar" || cookie[0].HttpOnly {
		t.Fatalf("language preference cookie missing or unsafe: %#v", cookie)
	}

	request = webTestRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "fr-CA,fr;q=0.9,en;q=0.8")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`<html lang="fr" dir="ltr"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte("Explorez les types")) {
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
