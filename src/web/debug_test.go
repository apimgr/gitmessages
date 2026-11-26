package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/mode"
)

func TestDebugEndpointsRequireExplicitEnablementAndOperatorToken(t *testing.T) {
	store := tokenRouteStore(t)
	wasEnabled := mode.IsDebugEnabled()
	mode.SetDebugEnabled(false)
	t.Cleanup(func() { mode.SetDebugEnabled(wasEnabled) })
	handler := debugHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/debug/memory", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled debug endpoint returned %d", response.Code)
	}

	mode.SetDebugEnabled(true)
	request = httptest.NewRequest(http.MethodGet, "/debug/memory", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated debug endpoint returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/debug/memory", nil)
	request.Header.Set("Authorization", "Bearer adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "goroutines") {
		t.Fatalf("authenticated debug endpoint returned %d: %s", response.Code, response.Body.String())
	}
	if store == nil {
		t.Fatal("debug test did not initialize operator authentication")
	}
}

func TestDebugMiddlewarePreservesBoundedRequestBodyAndRedactsSecrets(t *testing.T) {
	wasEnabled := mode.IsDebugEnabled()
	mode.SetDebugEnabled(true)
	t.Cleanup(func() { mode.SetDebugEnabled(wasEnabled) })
	configuration := config.Get()
	previous := configuration.Server.Debug
	configuration.Server.Debug.LogBodies = true
	configuration.Server.Debug.MaxBodyLogSize = "4B"
	t.Cleanup(func() { configuration.Server.Debug = previous })
	body := `{"token":"adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ","message":"hello"}`
	request := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	response := httptest.NewRecorder()
	debugMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read replayed body: %v", err)
		}
		_, _ = w.Write(read)
	})).ServeHTTP(response, request)
	if response.Body.String() != body {
		t.Fatalf("middleware changed request body: %q", response.Body.String())
	}
	if got := redactDebugText(`{"token":"adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`); strings.Contains(got, "adm_0123456789") {
		t.Fatalf("credential was not redacted: %s", got)
	}
	for _, field := range []string{"access_token", "refresh_token", "session", "code", "apiKey", "cookie", "credential", "authorization", "password_confirmation", "client_secret"} {
		input := `{"` + field + `":"sensitive-value"}`
		if got := redactDebugText(input); strings.Contains(got, "sensitive-value") {
			t.Errorf("%s value was not redacted: %s", field, got)
		}
	}
	if got := redactDebugText("Authorization: Bearer sensitive-value"); strings.Contains(got, "sensitive-value") {
		t.Fatalf("bearer value was not redacted: %s", got)
	}
	if debugBodyLimit("11MiB") != 10*1024 {
		t.Fatal("oversized body limit was not clamped to the safe default")
	}
}

func TestDebugConfigRedactsCredentialsAndError500UsesThemedRenderer(t *testing.T) {
	tokenRouteStore(t)
	wasEnabled := mode.IsDebugEnabled()
	mode.SetDebugEnabled(true)
	t.Cleanup(func() { mode.SetDebugEnabled(wasEnabled) })
	configuration := config.Get()
	previousToken := configuration.Server.Token
	configuration.Server.Token = "debug-test-secret"
	t.Cleanup(func() { configuration.Server.Token = previousToken })
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		request.Header.Set("Accept", "text/html")
		response := httptest.NewRecorder()
		debugHandler(nil).ServeHTTP(response, request)
		return response
	}
	configurationResponse := call("/debug/config")
	if configurationResponse.Code != http.StatusOK || strings.Contains(configurationResponse.Body.String(), "debug-test-secret") {
		t.Fatalf("debug config did not redact credentials: %d %s", configurationResponse.Code, configurationResponse.Body.String())
	}
	errorResponse := call("/debug/error500")
	if errorResponse.Code != http.StatusInternalServerError || !strings.Contains(errorResponse.Body.String(), "<main") || !strings.Contains(errorResponse.Body.String(), `class="theme-`) {
		t.Fatalf("debug error did not use the themed renderer: %d %s", errorResponse.Code, errorResponse.Body.String())
	}
}

func TestDebugRoutesIncludeRegisteredApplicationPatterns(t *testing.T) {
	tokenRouteStore(t)
	wasEnabled := mode.IsDebugEnabled()
	mode.SetDebugEnabled(true)
	t.Cleanup(func() { mode.SetDebugEnabled(wasEnabled) })
	request := httptest.NewRequest(http.MethodGet, "/debug/routes", nil)
	request.Header.Set("Authorization", "Bearer adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	response := httptest.NewRecorder()
	Handler("test").ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("debug route list returned %d", response.Code)
	}
	var result struct {
		Count  int         `json:"count"`
		Routes []routeInfo `json:"routes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode debug routes: %v", err)
	}
	paths := make(map[string]bool, len(result.Routes))
	for _, route := range result.Routes {
		paths[route.Path] = true
	}
	for _, path := range []string{"/server/security/policy", "/server/preferences/tokens", "/api/v1/messages", "/debug/error500"} {
		if !paths[path] {
			t.Errorf("debug route list omitted %s", path)
		}
	}
	if result.Count != len(result.Routes) {
		t.Errorf("debug route count %d does not match %d entries", result.Count, len(result.Routes))
	}
}

func TestDebugPprofIndexUsesSlashFreeCanonicalRoute(t *testing.T) {
	tokenRouteStore(t)
	wasEnabled := mode.IsDebugEnabled()
	mode.SetDebugEnabled(true)
	t.Cleanup(func() { mode.SetDebugEnabled(wasEnabled) })

	request := httptest.NewRequest(http.MethodGet, "/debug/pprof", nil)
	request.Header.Set("Authorization", "Bearer adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	response := httptest.NewRecorder()
	Handler("test").ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "heap") {
		t.Fatalf("canonical pprof index returned %d: %s", response.Code, response.Body.String())
	}

	trailing := httptest.NewRecorder()
	Handler("test").ServeHTTP(trailing, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if trailing.Code != http.StatusMovedPermanently || trailing.Header().Get("Location") != "/debug/pprof" {
		t.Fatalf("trailing slash returned %d with Location %q", trailing.Code, trailing.Header().Get("Location"))
	}
}
