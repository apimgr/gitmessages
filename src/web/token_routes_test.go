package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

func TestTokenIdentifierRequiresUUIDv7(t *testing.T) {
	if !validTokenID("019535d4-9df7-7b2e-8c3a-c792722a4f12") {
		t.Fatal("valid UUID v7 token ID was rejected")
	}
	for _, value := range []string{"123", "00000000-0000-4000-8000-000000000000", "019535d4-9df7-7b2e-0c3a-c792722a4f12"} {
		if validTokenID(value) {
			t.Errorf("invalid public token ID %q was accepted", value)
		}
	}
}

func TestReadScopeBlocksRESTWritesButAllowsGraphQLQueries(t *testing.T) {
	store := tokenRouteStore(t)
	_, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	readToken, _, err := store.CreateLinkedWithScope(t.Context(), identity.OwnerID, "read-only", "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, APIBasePath()+"/messages", nil)
	request.Header.Set("Authorization", "Bearer "+readToken)
	response := httptest.NewRecorder()
	if requireWriteScope(response, request, Route{Method: http.MethodPost, Path: APIBasePath() + "/messages"}) || response.Code != http.StatusForbidden || response.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope"` {
		t.Fatalf("read-scope REST write returned %d with %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	graphqlResponse := httptest.NewRecorder()
	if !requireWriteScope(graphqlResponse, request, Route{Method: http.MethodPost, Path: APIBasePath() + "/server/graphql"}) {
		t.Fatal("read-only GraphQL query was blocked because GraphQL uses POST")
	}
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
	linkedReveal := call(http.MethodPost, "/"+linkedToken.ID+"/reveal")
	if linkedReveal.Code != http.StatusOK || !strings.Contains(linkedReveal.Body.String(), linked) {
		t.Fatalf("linked reveal response = %d", linkedReveal.Code)
	}
	linkedRotate := call(http.MethodPost, "/"+linkedToken.ID+"/rotate")
	if linkedRotate.Code != http.StatusOK || !strings.Contains(linkedRotate.Body.String(), `"linked_token"`) {
		t.Fatalf("linked rotate response = %d %s", linkedRotate.Code, linkedRotate.Body.String())
	}
	primaryRotate := call(http.MethodPost, "/primary/rotate")
	if primaryRotate.Code != http.StatusOK || !strings.Contains(primaryRotate.Body.String(), `"owner_token"`) || primaryRotate.Header().Get("Cache-Control") != "no-store" {
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

	getPage := httptest.NewRecorder()
	tokenPageOrAction(getPage, httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil))
	var csrf *http.Cookie
	for _, cookie := range getPage.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("token page did not issue its signed CSRF cookie")
	}
	r := httptest.NewRequest(http.MethodPost, "/server/preferences/token/create", strings.NewReader("csrf_token="+csrf.Value))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(csrf)
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

func TestOwnerAndFlashCookiesOmitSecureOnI2P(t *testing.T) {
	tokenRouteStore(t)
	request := httptest.NewRequest(http.MethodPost, "http://gitmessages.b32.i2p/server/preferences/tokens", nil)
	response := httptest.NewRecorder()
	setOwnerCookie(response, request, "own_example", 0)
	if !setTokenFlash(response, request, "primary", "tokens.toast_created") {
		t.Fatal("could not sign I2P token-view selector")
	}

	secure := make(map[string]bool)
	for _, cookie := range response.Result().Cookies() {
		secure[cookie.Name] = cookie.Secure
	}
	if secure[ownerCookieName] || secure[tokenFlashCookieName] {
		t.Fatalf("I2P cookies must omit Secure: %#v", response.Result().Cookies())
	}
	followup := httptest.NewRequest(http.MethodGet, "http://gitmessages.b32.i2p/server/preferences/tokens", nil)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == tokenFlashCookieName {
			followup.AddCookie(cookie)
		}
	}
	if flash, ok := takeTokenFlash(httptest.NewRecorder(), followup); !ok || flash.card != "primary" || flash.toast != "tokens.toast_created" {
		t.Fatal("I2P flash cookie was not usable on its follow-up request")
	}
}

func TestCSRFCookieSecurePolicyIncludesHiddenServices(t *testing.T) {
	if !csrfCookieSecure(httptest.NewRequest(http.MethodGet, "https://example.test/", nil)) {
		t.Fatal("HTTPS CSRF cookie should be Secure with auto policy")
	}
	if !csrfCookieSecure(httptest.NewRequest(http.MethodGet, "http://service.onion/", nil)) {
		t.Fatal("Tor CSRF cookie should be Secure")
	}
	if csrfCookieSecure(httptest.NewRequest(http.MethodGet, "http://gitmessages.b32.i2p/", nil)) {
		t.Fatal("I2P CSRF cookie must omit Secure")
	}
}

func TestCSRFCookieIsSignedAndRotatesImmediately(t *testing.T) {
	store := tokenRouteStore(t)
	request := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	response := httptest.NewRecorder()
	tokenPageOrAction(response, request)
	var csrf *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil || !validSignedCSRF(request.Context(), csrf.Value) {
		t.Fatal("CSRF cookie was not signed with the application secret")
	}
	old := csrf.Value
	if err := store.Database().RotateAppSecret(request.Context(), "csrf_token_secret"); err != nil {
		t.Fatalf("rotate CSRF secret: %v", err)
	}
	if validSignedCSRF(request.Context(), old) {
		t.Fatal("CSRF token signed by a rotated key remained valid")
	}
	parts := strings.Split(old, ".")
	parts[1] = strings.Repeat("A", len(parts[1]))
	if validSignedCSRF(request.Context(), strings.Join(parts, ".")) {
		t.Fatal("tampered CSRF signature was accepted")
	}
}

func TestTokenViewCookieCarriesSignedSelectorOnly(t *testing.T) {
	store := tokenRouteStore(t)
	primary, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://example.test/server/preferences/tokens", nil)
	response := httptest.NewRecorder()
	if !setTokenFlash(response, request, "primary", "tokens.toast_created") {
		t.Fatal("could not sign token-view selector")
	}
	var view *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == tokenFlashCookieName {
			view = cookie
		}
	}
	if view == nil || strings.Contains(view.Value, primary) {
		t.Fatalf("token-view cookie is missing or exposes the raw token: %#v", view)
	}
	followup := httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	followup.AddCookie(view)
	flash, ok := takeTokenFlash(httptest.NewRecorder(), followup)
	if !ok || flash.card != "primary" || flash.toast != "tokens.toast_created" {
		t.Fatalf("signed token-view selector = %#v, %v", flash, ok)
	}
	view.Value += "tampered"
	followup = httptest.NewRequest(http.MethodGet, "https://example.test/server/preferences/tokens", nil)
	followup.AddCookie(view)
	if _, ok := takeTokenFlash(httptest.NewRecorder(), followup); ok {
		t.Fatal("tampered token-view selector was accepted")
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
	var csrf *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("token page did not issue its signed CSRF cookie")
	}
	call := func(confirm string) *httptest.ResponseRecorder {
		body := "csrf_token=" + csrf.Value + "&confirm=" + confirm
		r := httptest.NewRequest(http.MethodPost, "https://example.test/server/preferences/token/revoke", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(csrf)
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
	csrfCleared := false
	for _, cookie := range valid.Result().Cookies() {
		csrfCleared = csrfCleared || cookie.Name == csrfCookieName && cookie.MaxAge == -1
	}
	if !csrfCleared {
		t.Fatal("primary token revocation did not expire the CSRF token for regeneration")
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
	for _, operation := range []string{"listOwnerTokens", "createLinkedToken", "issuePrimaryToken", "revokePrimaryToken", "revealPrimaryToken", "rotatePrimaryToken", "revealLinkedToken", "rotateLinkedToken", "updateLinkedToken", "revokeLinkedToken"} {
		if strings.Contains(string(tools), operation) {
			t.Errorf("token management operation %s was exposed as an MCP tool", operation)
		}
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

func TestLinkedTokenRevocationUsesCanonicalDeleteRoute(t *testing.T) {
	store := tokenRouteStore(t)
	primary, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	linkedRaw, linked, err := store.CreateLinked(t.Context(), identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, APIBasePath()+"/server/preferences/tokens/"+linked.ID, nil)
	request.Header.Set("Authorization", "Bearer "+primary)
	response := httptest.NewRecorder()
	tokenAPI(response, request)
	var result struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.ID != linked.ID || result.Data.Status != "revoked" {
		t.Fatalf("canonical linked-token DELETE returned %d %s", response.Code, response.Body.String())
	}
	if _, err := store.Authenticate(t.Context(), linkedRaw); err == nil {
		t.Fatal("revoked linked token remained valid")
	}
}

func TestAPITokenExpiryContract(t *testing.T) {
	for _, value := range []string{"never", "7d", "30d", "90d", "1y"} {
		if _, err := parseAPITokenExpiry(value, ""); err != nil {
			t.Errorf("expires=%q rejected: %v", value, err)
		}
	}
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	deadline, err := parseAPITokenExpiry("", future)
	if err != nil {
		t.Fatalf("future expires_on=%q rejected: %v", future, err)
	}
	wantDeadline, _ := time.Parse("2006-01-02", future)
	wantDeadline = wantDeadline.AddDate(0, 0, 1)
	if deadline == nil || !deadline.Equal(wantDeadline) {
		t.Fatalf("expires_on deadline = %v, want %v", deadline, wantDeadline)
	}
	for _, invalid := range []struct{ expires, expiresOn string }{
		{"2030-01-01", ""}, {"7d", "2030-01-01"}, {"", "7d"}, {"", "2000-01-01"},
	} {
		if _, err := parseAPITokenExpiry(invalid.expires, invalid.expiresOn); err == nil {
			t.Errorf("invalid expiry accepted: expires=%q expires_on=%q", invalid.expires, invalid.expiresOn)
		}
	}
}
