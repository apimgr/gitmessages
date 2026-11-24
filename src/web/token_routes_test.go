package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/auth"
)

func tokenRouteStore(t *testing.T) *auth.Store {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := auth.Open(projectTestDir(t)+"/tokens.db", "adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", key, 25, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ConfigureAuth(store)
	t.Cleanup(func() { ConfigureAuth(nil) })
	return store
}

func TestTokenAPIListRevealAndRotateResponses(t *testing.T) {
	store := tokenRouteStore(t)
	primary, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	linked, linkedToken, err := store.CreateLinked(t.Context(), identity.OwnerID, "route-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, APIBasePath()+"/server/preferences/tokens"+path, nil)
		r.Header.Set("Authorization", "Bearer "+primary)
		w := httptest.NewRecorder()
		tokenAPI(w, r)
		return w
	}
	listed := call(http.MethodGet, "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), primary) || strings.Contains(listed.Body.String(), linked) {
		t.Fatalf("list leaked credentials or returned %d", listed.Code)
	}
	var envelope map[string]any
	if err := json.Unmarshal(listed.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if envelope["data"] == nil {
		t.Fatalf("list body omitted data: %s", listed.Body.String())
	}
	primaryReveal := call(http.MethodPost, "/primary/reveal")
	if primaryReveal.Code != http.StatusOK || !strings.Contains(primaryReveal.Body.String(), primary) || primaryReveal.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("primary reveal response = %d %s", primaryReveal.Code, primaryReveal.Body.String())
	}
	linkedReveal := call(http.MethodPost, "/"+strconv.FormatInt(linkedToken.ID, 10)+"/reveal")
	if linkedReveal.Code != http.StatusOK || !strings.Contains(linkedReveal.Body.String(), linked) {
		t.Fatalf("linked reveal response = %d", linkedReveal.Code)
	}
	linkedRotate := call(http.MethodPost, "/"+strconv.FormatInt(linkedToken.ID, 10)+"/rotate")
	if linkedRotate.Code != http.StatusOK || !strings.Contains(linkedRotate.Body.String(), `"value"`) {
		t.Fatalf("linked rotate response = %d %s", linkedRotate.Code, linkedRotate.Body.String())
	}
	primaryRotate := call(http.MethodPost, "/primary/rotate")
	if primaryRotate.Code != http.StatusOK || !strings.Contains(primaryRotate.Body.String(), `"token"`) || primaryRotate.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("primary rotate response = %d %s", primaryRotate.Code, primaryRotate.Body.String())
	}
}

func TestTokenPageCSRFAndCookieAttributes(t *testing.T) {
	tokenRouteStore(t)
	r := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	r.Header.Set("X-GitMessages-Language", "en")
	w := httptest.NewRecorder()
	tokenPageOrAction(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET token page = %d", w.Code)
	}
	var csrf *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil || csrf.HttpOnly || !csrf.Secure || csrf.SameSite != http.SameSiteStrictMode || csrf.Path != "/" {
		t.Fatalf("CSRF cookie attributes are invalid: %#v", csrf)
	}
	if !strings.Contains(w.Body.String(), "Create token") || strings.Contains(w.Body.String(), "Linked tokens") {
		t.Fatal("anonymous token page exposed owner details or omitted issue form")
	}
}

func TestTokenIssueRequiresCSRFAndRedirectsAfterIssue(t *testing.T) {
	store := tokenRouteStore(t)
	bad := httptest.NewRequest(http.MethodPost, "/server/preferences/token/create", strings.NewReader("csrf_token=x"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "y"})
	badResponse := httptest.NewRecorder()
	tokenWebAction("create")(badResponse, bad)
	if badResponse.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF status = %d", badResponse.Code)
	}

	csrf := "0123456789abcdef"
	r := httptest.NewRequest(http.MethodPost, "/server/preferences/token/create", strings.NewReader("csrf_token="+csrf))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	tokenWebAction("create")(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/server/preferences/tokens#primary" {
		t.Fatalf("issue response = %d %q", w.Code, w.Header().Get("Location"))
	}
	var owner *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == ownerCookieName {
			owner = cookie
		}
	}
	if owner == nil || !owner.HttpOnly || !owner.Secure || owner.SameSite != http.SameSiteStrictMode || owner.Path != "/" {
		t.Fatalf("owner cookie attributes are invalid: %#v", owner)
	}
	if _, err := store.Authenticate(r.Context(), owner.Value); err != nil {
		t.Fatalf("issued cookie token does not authenticate: %v", err)
	}
	var legacyCleared bool
	for _, cookie := range w.Result().Cookies() {
		legacyCleared = legacyCleared || cookie.Name == legacyOwnerCookieName && cookie.MaxAge == -1
	}
	if !legacyCleared {
		t.Fatalf("new owner cookie write did not clear the legacy name: %#v", w.Result().Cookies())
	}
}

func TestOwnerCookieNameMatchesProjectIdentityAndIDEA(t *testing.T) {
	const prefix = "gitmessages_owner_token_"
	suffix := strings.TrimPrefix(ownerCookieName, prefix)
	if len(suffix) != 8 {
		t.Fatalf("owner cookie suffix length = %d, want 8", len(suffix))
	}
	for _, letter := range suffix {
		if letter < 'a' || letter > 'z' {
			t.Fatalf("owner cookie suffix contains non-lowercase letter %q", letter)
		}
	}
	idea, err := os.ReadFile("../../IDEA.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idea), "owner_token: "+ownerCookieName) {
		t.Fatalf("IDEA.md does not record the active cookie name %q", ownerCookieName)
	}
}

func TestLegacyOwnerCookieMigratesOnVerifiedUse(t *testing.T) {
	store := tokenRouteStore(t)
	primary, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	r.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: primary})
	w := httptest.NewRecorder()
	tokenPageOrAction(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy owner page returned %d", w.Code)
	}
	var current, legacy *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		switch cookie.Name {
		case ownerCookieName:
			current = cookie
		case legacyOwnerCookieName:
			legacy = cookie
		}
	}
	if current == nil || current.Value != primary || !current.HttpOnly || !current.Secure || current.SameSite != http.SameSiteStrictMode || current.Path != "/" || current.MaxAge <= 0 {
		t.Fatalf("new owner cookie was not issued with the existing attributes: %#v", current)
	}
	if legacy == nil || legacy.MaxAge != -1 {
		t.Fatalf("legacy owner cookie was not cleared: %#v", legacy)
	}
}

func TestInvalidNewOwnerCookieDoesNotFallBackToLegacy(t *testing.T) {
	store := tokenRouteStore(t)
	primary, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	r.AddCookie(&http.Cookie{Name: ownerCookieName, Value: "invalid-new-token"})
	r.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: primary})
	w := httptest.NewRecorder()
	if _, ok := ownerCookieIdentity(w, r, store); ok {
		t.Fatal("invalid new cookie fell back to the valid legacy cookie")
	}
	cleared := map[string]bool{}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge == -1 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared[ownerCookieName] || !cleared[legacyOwnerCookieName] {
		t.Fatalf("invalid new cookie did not clear both names: %#v", w.Result().Cookies())
	}
}

func TestOwnerCookieIsNotUsedWhenAuthorizationHeaderIsPresent(t *testing.T) {
	store := tokenRouteStore(t)
	primary, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	r.Header.Set("Authorization", "Bearer invalid")
	r.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: primary})
	w := httptest.NewRecorder()
	if _, ok := ownerCookieIdentity(w, r, store); ok {
		t.Fatal("owner cookie authenticated despite an Authorization header")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("owner cookie was refreshed despite an Authorization header: %#v", w.Result().Cookies())
	}
}

func TestInvalidLegacyOwnerCookieIsCleared(t *testing.T) {
	store := tokenRouteStore(t)
	r := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	r.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: "invalid-legacy-token"})
	w := httptest.NewRecorder()
	if _, ok := ownerCookieIdentity(w, r, store); ok {
		t.Fatal("invalid legacy cookie authenticated")
	}
	cleared := map[string]bool{}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge == -1 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared[legacyOwnerCookieName] {
		t.Fatalf("invalid legacy cookie was not cleared: %#v", w.Result().Cookies())
	}
}

func TestTokenCookieMiddlewareMigratesLegacyCookieAndIgnoresAPI(t *testing.T) {
	store := tokenRouteStore(t)
	primary, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	frontend := httptest.NewRequest(http.MethodGet, "https://example.test/server/about", nil)
	frontend.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: primary})
	frontendResponse := httptest.NewRecorder()
	TokenCookieMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(frontendResponse, frontend)
	var migrated, expired bool
	for _, cookie := range frontendResponse.Result().Cookies() {
		migrated = migrated || cookie.Name == ownerCookieName && cookie.Value == primary && cookie.MaxAge > 0
		expired = expired || cookie.Name == legacyOwnerCookieName && cookie.MaxAge == -1
	}
	if frontendResponse.Code != http.StatusNoContent || !migrated || !expired {
		t.Fatalf("frontend middleware did not migrate legacy cookie: status=%d cookies=%#v", frontendResponse.Code, frontendResponse.Result().Cookies())
	}

	for _, cookieName := range []string{ownerCookieName, legacyOwnerCookieName} {
		apiRequest := httptest.NewRequest(http.MethodGet, "https://example.test"+APIBasePath()+"/server/preferences/tokens", nil)
		apiRequest.RemoteAddr = "192.0.2.10:1234"
		apiRequest.AddCookie(&http.Cookie{Name: cookieName, Value: primary})
		apiResponse := httptest.NewRecorder()
		TokenCookieMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenAPI(w, r)
		})).ServeHTTP(apiResponse, apiRequest)
		if apiResponse.Code != http.StatusUnauthorized || len(apiResponse.Result().Cookies()) != 0 {
			t.Errorf("API accepted or changed %s cookie: status=%d cookies=%#v", cookieName, apiResponse.Code, apiResponse.Result().Cookies())
		}
		if got := rateIdentity(apiRequest, "127.0.0.1"); got != "ip:127.0.0.1" {
			t.Errorf("API rate identity used %s cookie: %q", cookieName, got)
		}
	}

	for _, path := range []string{"/api/graphql", "/api/mcp", "/mcp", "/server/metrics", "/server/metrics/prometheus"} {
		request := httptest.NewRequest(http.MethodGet, "https://example.test"+path, nil)
		request.AddCookie(&http.Cookie{Name: legacyOwnerCookieName, Value: primary})
		response := httptest.NewRecorder()
		TokenCookieMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(response, request)
		if len(response.Result().Cookies()) != 0 {
			t.Errorf("API alias %s used or changed owner cookie: %#v", path, response.Result().Cookies())
		}
	}
}

func TestTokenRateLimitConfiguration(t *testing.T) {
	tokenRouteStore(t)
	tokenRateMu.Lock()
	tokenRate = map[string]tokenRateWindow{}
	tokenRateMu.Unlock()
	ConfigureTokenRateLimits(1, 60_000_000_000, 1, 60_000_000_000)
	t.Cleanup(func() { ConfigureTokenRateLimits(10, 15*60_000_000_000, 5, 15*60_000_000_000) })
	first := httptest.NewRecorder()
	if !strictTokenAllowed(first, httptest.NewRequest(http.MethodPost, "/", nil)) {
		t.Fatal("first strict request blocked")
	}
	second := httptest.NewRecorder()
	if strictTokenAllowed(second, httptest.NewRequest(http.MethodPost, "/", nil)) || second.Code != http.StatusTooManyRequests {
		t.Fatal("configured strict limit was not enforced")
	}
}

func TestMCPAuthenticationAliases(t *testing.T) {
	tokenRouteStore(t)
	handler := Handler("test")
	for _, path := range []string{"/mcp", "/api/mcp", APIBasePath() + "/server/mcp"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
		r.Header.Set("Mcp-Method", "tools/list")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s returned %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestPrimaryRevocationRequiresLastFourAndDeletesOwner(t *testing.T) {
	store := tokenRouteStore(t)
	primary, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateLinked(t.Context(), identity.OwnerID, "automation", nil); err != nil {
		t.Fatal(err)
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	pageRequest.AddCookie(&http.Cookie{Name: ownerCookieName, Value: primary})
	page := httptest.NewRecorder()
	tokenPageOrAction(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), primary[len(primary)-4:]) || !strings.Contains(page.Body.String(), `name="confirm"`) || !strings.Contains(page.Body.String(), "Linked tokens (1)") {
		t.Fatalf("primary token page omitted the last-four revocation flow: status=%d", page.Code)
	}
	call := func(confirm string) *httptest.ResponseRecorder {
		body := "csrf_token=csrf-value&confirm=" + confirm
		r := httptest.NewRequest(http.MethodPost, "https://example.test/server/preferences/token/revoke", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-value"})
		r.AddCookie(&http.Cookie{Name: ownerCookieName, Value: primary})
		w := httptest.NewRecorder()
		tokenWebAction("primary-revoke")(w, r)
		return w
	}
	invalid := call("nope")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("wrong last four returned %d, want 400", invalid.Code)
	}
	if _, err := store.Authenticate(t.Context(), primary); err != nil {
		t.Fatalf("wrong confirmation deleted owner: %v", err)
	}
	valid := call(primary[len(primary)-4:])
	if valid.Code != http.StatusSeeOther || valid.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("valid primary revocation returned %d or omitted storage clearing", valid.Code)
	}
	if _, err := store.Authenticate(t.Context(), primary); err == nil {
		t.Fatal("primary token remained valid after owner deletion")
	}
}

func TestOpenAPIAndGeneratedHelpDocumentPrimaryRevocation(t *testing.T) {
	openAPI := httptest.NewRecorder()
	swaggerJSON(openAPI, webTestRequest(http.MethodGet, APIBasePath()+"/server/swagger", nil))
	var schema struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if openAPI.Code != http.StatusOK || json.Unmarshal(openAPI.Body.Bytes(), &schema) != nil {
		t.Fatalf("OpenAPI response = %d: %s", openAPI.Code, openAPI.Body.String())
	}
	deleteOperation, exists := schema.Paths[APIBasePath()+"/server/preferences/tokens/primary"]["delete"]
	if !exists || !strings.Contains(string(deleteOperation), "confirm") {
		t.Fatal("OpenAPI omitted primary token deletion or its confirmation field")
	}
	tools, _ := json.Marshal(mcpTools(webTestRequest(http.MethodGet, "/server/help/mcp", nil)))
	if strings.Contains(string(tools), "revoke_primary_token") {
		t.Fatal("primary token deletion was exposed as an MCP tool")
	}
	if !strings.Contains(llmsTextBody(webTestRequest(http.MethodGet, "/.well-known/llms.txt", nil)), "DELETE") {
		t.Fatal("generated API help omitted primary token deletion")
	}
}

func TestPrimaryRevocationAPIMirrorRequiresLastFour(t *testing.T) {
	store := tokenRouteStore(t)
	primary, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateLinked(t.Context(), identity.OwnerID, "automation", nil); err != nil {
		t.Fatal(err)
	}
	call := func(confirm string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"confirm": confirm})
		r := httptest.NewRequest(http.MethodDelete, APIBasePath()+"/server/preferences/tokens/primary", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+primary)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tokenAPI(w, r)
		return w
	}
	invalid := call("nope")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("wrong API last four returned %d, want 400", invalid.Code)
	}
	if _, err := store.Authenticate(t.Context(), primary); err != nil {
		t.Fatalf("wrong API confirmation deleted owner: %v", err)
	}
	valid := call(primary[len(primary)-4:])
	var response struct {
		Data struct {
			Tokens    int64 `json:"tokens"`
			Resources int64 `json:"resources"`
		} `json:"data"`
	}
	if valid.Code != http.StatusOK || json.Unmarshal(valid.Body.Bytes(), &response) != nil || response.Data.Tokens != 2 || response.Data.Resources != 0 {
		t.Fatalf("valid API primary revocation = %d %s", valid.Code, valid.Body.String())
	}
}
