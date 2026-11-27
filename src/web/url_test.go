package web

import (
	"net"
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

func TestResolveStartupFQDNUsesConfiguredResolutionOrder(t *testing.T) {
	addresses := []net.IP{
		net.ParseIP("8.8.8.8"),
		net.ParseIP("2001:4860:4860::8888"),
	}
	tests := []struct {
		name        string
		domain      string
		osHostname  string
		envHostname string
		addresses   []net.IP
		want        string
	}{
		{name: "domain list primary", domain: "primary.example.com,secondary.example.net", osHostname: "host.example.org", want: "primary.example.com"},
		{name: "operating system hostname", osHostname: "host.example.org", envHostname: "env.example.net", addresses: addresses, want: "host.example.org"},
		{name: "environment hostname", envHostname: "env.example.net", addresses: addresses, want: "env.example.net"},
		{name: "invalid domain entry falls through", domain: "192.168.1.1,valid.example.net", osHostname: "host.example.org", want: "valid.example.net"},
		{name: "invalid domain and hostname fall through", domain: "dev.local", envHostname: "single-label", addresses: addresses, want: "2001:4860:4860::8888"},
		{name: "IPv6 before IPv4", addresses: addresses, want: "2001:4860:4860::8888"},
		{name: "skip private and loopback addresses", addresses: []net.IP{net.ParseIP("192.168.1.2"), net.ParseIP("127.0.0.1"), net.ParseIP("8.8.4.4")}, want: "8.8.4.4"},
		{name: "unresolved", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveStartupFQDN(test.domain, test.osHostname, test.envHostname, test.addresses); got != test.want {
				t.Fatalf("resolveStartupFQDN() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIsValidHostUsesModeAndRejectsMalformedValues(t *testing.T) {
	for _, test := range []struct {
		host string
		dev  bool
		want bool
	}{
		{host: "api.example.com", want: true},
		{host: "service.test", want: false},
		{host: "service.test", dev: true, want: true},
		{host: "127.0.0.1", dev: true, want: false},
		{host: "bad host.example.com", want: false},
		{host: "co.uk", want: false},
		{host: "service.onion", want: true},
	} {
		if got := IsValidHost(test.host, test.dev, "gitmessages"); got != test.want {
			t.Errorf("IsValidHost(%q, %t) = %t, want %t", test.host, test.dev, got, test.want)
		}
	}
	if IsValidEnvironmentHost("service.onion", false, "gitmessages") {
		t.Fatal("app-managed overlay host was accepted as an environment override")
	}
	for host, want := range map[string]bool{"api.example.com": true, "service.test": false, "service.onion": false, "service.i2p": false, "service.exit": false} {
		if got := IsValidSSLHost(host); got != want {
			t.Errorf("IsValidSSLHost(%q) = %t, want %t", host, got, want)
		}
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
