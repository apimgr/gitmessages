package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func TestBuildURLDoesNotInventLocalHost(t *testing.T) {
	if got := BuildURL(nil, "/server/help"); got != "" {
		t.Fatalf("BuildURL(nil, path) = %q, want empty without request context", got)
	}
	if got := fallbackHostname(); strings.EqualFold(got, "localhost") || strings.HasSuffix(strings.ToLower(got), ".local") || strings.HasSuffix(strings.ToLower(got), ".localdomain") {
		t.Fatalf("fallback hostname uses a reserved local name: %q", got)
	}
}

func TestBuildURLUsesTrustedProxyHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "http://internal:8080/", nil)
	r.RemoteAddr = "10.0.0.9:41000"
	r.Host = "internal:8080"
	r.Header.Set("X-Forwarded-Host", "public.example.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Port", "443")
	r.Header.Set("X-Forwarded-Prefix", "/messages")
	if got := BuildURL(r, "/api/v1/items"); got != "https://public.example.test/messages/api/v1/items" {
		t.Fatalf("BuildURL() = %q", got)
	}
}

func TestConfiguredBaseURLPrefixesRoutesAndLinks(t *testing.T) {
	current := config.Get()
	previous := current.Server.BaseURL
	current.Server.BaseURL = "/messages"
	t.Cleanup(func() { current.Server.BaseURL = previous })
	handler := Handler("test-version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/messages/types", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `href="/messages/builder"`) {
		t.Fatalf("prefixed page returned %d or omitted prefixed links: %s", response.Code, response.Body.String())
	}
	unprefixed := httptest.NewRecorder()
	handler.ServeHTTP(unprefixed, httptest.NewRequest(http.MethodGet, "/types", nil))
	if unprefixed.Code != http.StatusNotFound {
		t.Fatalf("unprefixed route returned %d, want 404", unprefixed.Code)
	}
	trailing := httptest.NewRecorder()
	handler.ServeHTTP(trailing, httptest.NewRequest(http.MethodGet, "/messages/types/", nil))
	if trailing.Code != http.StatusMovedPermanently || trailing.Header().Get("Location") != "/messages/types" {
		t.Fatalf("prefixed trailing slash returned %d with Location %q", trailing.Code, trailing.Header().Get("Location"))
	}
}

func TestBuildURLIgnoresUntrustedProxyHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "http://public.example.test/", nil)
	r.RemoteAddr = "203.0.113.8:41000"
	r.Host = "public.example.test"
	r.Header.Set("X-Forwarded-Host", "attacker.invalid")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Prefix", "/attacker")
	got := BuildURL(r, "/security")
	if !strings.HasPrefix(got, "http://public.example.test") || strings.Contains(got, "attacker") {
		t.Fatalf("BuildURL() = %q", got)
	}
}

func TestBuildURLRejectsUnsafePrefix(t *testing.T) {
	r := httptest.NewRequest("GET", "http://internal/", nil)
	r.RemoteAddr = "127.0.0.1:41000"
	r.Host = "public.example.test"
	r.Header.Set("X-Forwarded-Prefix", "/../outside")
	got := BuildURL(r, "/security")
	if !strings.HasPrefix(got, "http://public.example.test") || strings.Contains(got, "outside") {
		t.Fatalf("BuildURL() = %q", got)
	}
}

func TestPrefixHTMLPathsSkipsProtocolRelativeLinks(t *testing.T) {
	got := prefixHTMLPaths(`<a href="/server/help">Help</a><a href="//outside.test/path">External</a><form action="/server/preferences">`, "/messages")
	if !strings.Contains(got, `href="/messages/server/help"`) || !strings.Contains(got, `href="//outside.test/path"`) || !strings.Contains(got, `action="/messages/server/preferences"`) {
		t.Fatalf("prefixHTMLPaths() = %q", got)
	}
}
