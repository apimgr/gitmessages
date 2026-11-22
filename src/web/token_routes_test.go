package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/auth"
)

func tokenRouteStore(t *testing.T) *auth.Store {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := auth.Open(t.TempDir()+"/tokens.db", "adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", key, 25, 0, 0)
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
		r := httptest.NewRequest(method, apiBase+"/server/preferences/tokens"+path, nil)
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
	for _, path := range []string{"/mcp", "/api/mcp", apiBase + "/server/mcp"} {
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
