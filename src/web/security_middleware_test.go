package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/securitydata"
)

func TestSubmittedDomainTargetsFindsEmailAndURLsAndRestoresBody(t *testing.T) {
	body := `{"email":"reporter@blocked.example","reported_url":"https://bad.example/path","message":"See https://another.example/item"}`
	request := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	targets := submittedDomainTargets(request)
	if len(targets) < 3 {
		t.Fatalf("submittedDomainTargets() returned %v", targets)
	}
	restored, err := io.ReadAll(request.Body)
	if err != nil || string(restored) != body {
		t.Fatalf("request body after inspection = %q, %v", restored, err)
	}
}

func TestSubmittedDomainTargetsLeavesOversizedBodyIntact(t *testing.T) {
	body := strings.Repeat("x", (1<<20)+16)
	request := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(body))
	if targets := submittedDomainTargets(request); len(targets) != 0 {
		t.Fatalf("submittedDomainTargets() returned targets for oversized body: %v", targets)
	}
	restored, err := io.ReadAll(request.Body)
	if err != nil || string(restored) != body {
		t.Fatalf("oversized request body after inspection has length %d, %v", len(restored), err)
	}
}

func TestBlocklistRunsBeforeAllowlistedBypassAndGeoIP(t *testing.T) {
	configuration := config.Get()
	previousAllowlist := configuration.Server.Security.Allowlist
	previousBlocked := configuration.Server.Security.BlockedIPs
	previousBlocklists := configuration.Server.Security.Blocklists
	configuration.Server.Security.Allowlist = nil
	configuration.Server.Security.BlockedIPs = nil
	configuration.Server.Security.Blocklists = config.BlocklistsConfig{Enabled: true}
	t.Cleanup(func() {
		configuration.Server.Security.Allowlist = previousAllowlist
		configuration.Server.Security.BlockedIPs = previousBlocked
		configuration.Server.Security.Blocklists = previousBlocklists
		ConfigureSecurityData(nil, nil)
	})

	manager := securitydata.New(projectTestDir(t))
	blocklistDirectory := filepath.Join(manager.Dir, "blocklists")
	if err := os.MkdirAll(blocklistDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocklistDirectory, "firehol_level1.txt"), []byte("198.51.100.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ConfigureSecurityData(manager, nil)
	handler := Handler("test-version")

	blocked := webTestRequest(http.MethodGet, "/types", nil)
	blocked.RemoteAddr = "198.51.100.9:1234"
	blockedResponse := httptest.NewRecorder()
	handler.ServeHTTP(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusForbidden {
		t.Fatalf("blocklisted request status = %d, want 403", blockedResponse.Code)
	}

	configuration.Server.Security.Allowlist = []config.AllowlistEntry{{CIDR: "198.51.100.9/32", Description: "monitor"}}
	trusted := webTestRequest(http.MethodGet, "/types", nil)
	trusted.RemoteAddr = blocked.RemoteAddr
	trustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedResponse, trusted)
	if trustedResponse.Code != http.StatusOK {
		t.Fatalf("allowlisted request status = %d, want 200", trustedResponse.Code)
	}
}

func TestDomainBlocklistChecksSubmittedTargetsAndOriginPrecedence(t *testing.T) {
	configuration := config.Get()
	previousAllowlist := configuration.Server.Security.Allowlist
	previousBlocklists := configuration.Server.Security.Blocklists
	configuration.Server.Security.Allowlist = nil
	configuration.Server.Security.Blocklists = config.BlocklistsConfig{
		Enabled:      true,
		ExtraSources: []config.BlocklistSourceConfig{{ID: "domains", Type: "domain", Enabled: true}},
	}
	t.Cleanup(func() {
		configuration.Server.Security.Allowlist = previousAllowlist
		configuration.Server.Security.Blocklists = previousBlocklists
		ConfigureSecurityData(nil, nil)
	})
	manager := securitydata.New(projectTestDir(t))
	blocklistDirectory := filepath.Join(manager.Dir, "blocklists")
	if err := os.MkdirAll(blocklistDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocklistDirectory, "domains.txt"), []byte("blocked.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ConfigureSecurityData(manager, nil)
	handler := Handler("test-version")

	submission := httptest.NewRequest(http.MethodPost, "/api/v1/server/contact", strings.NewReader(`{"email":"sender@blocked.example"}`))
	submission.Header.Set("Content-Type", "application/json")
	submission.RemoteAddr = "203.0.113.45:1234"
	submissionResponse := httptest.NewRecorder()
	handler.ServeHTTP(submissionResponse, submission)
	if submissionResponse.Code != http.StatusForbidden {
		t.Fatalf("blocked submitted email status = %d, want 403", submissionResponse.Code)
	}

	originWins := httptest.NewRequest(http.MethodGet, "/types", nil)
	originWins.Header.Set("Origin", "https://safe.example")
	originWins.Header.Set("Referer", "https://blocked.example/page")
	originWins.RemoteAddr = "203.0.113.45:1234"
	originResponse := httptest.NewRecorder()
	handler.ServeHTTP(originResponse, originWins)
	if originResponse.Code != http.StatusOK {
		t.Fatalf("safe Origin with blocked Referer status = %d, want 200", originResponse.Code)
	}
}

func TestCountryBlockingAllowlistPrecedence(t *testing.T) {
	if countryDenied("CN", nil, []string{"CN"}) != true {
		t.Fatal("deny list did not block matching country")
	}
	if countryDenied("US", nil, []string{"CN"}) {
		t.Fatal("deny list blocked an unlisted country")
	}
	if countryDenied("CN", []string{"US"}, []string{"CN"}) != true {
		t.Fatal("allow list did not take precedence over deny list")
	}
	if countryDenied("US", []string{"US"}, []string{"US"}) {
		t.Fatal("allow list blocked its matching country")
	}
}
