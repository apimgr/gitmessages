package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
