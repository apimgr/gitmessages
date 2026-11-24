package securitydata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/paths"
)

func securityDataTestDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(parent, paths.ProjectName+"-security-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestParseBlocklistIPFiltersProtectedNetworks(t *testing.T) {
	entries, skipped, err := parseBlocklist([]byte("203.0.113.4\n10.0.0.0/8\n192.0.2.0/24\ninvalid\n"), "ip", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(entries, ",")
	if got != "192.0.2.0/24,203.0.113.4/32" {
		t.Fatalf("entries = %q", got)
	}
	if skipped != 2 {
		t.Fatalf("skipped entries = %d, want 2", skipped)
	}
}

func TestBlocklistMatchingUsesIPPrefixesAndDomainBoundaries(t *testing.T) {
	manager := New(securityDataTestDir(t))
	directory := filepath.Join(manager.Dir, "blocklists")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "firehol_level1.txt"), []byte("203.0.113.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "domains.txt"), []byte("example.com\n*.bad.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if source, err := manager.IsIPBlocked(netip.MustParseAddr("203.0.113.9"), nil); err != nil || source != "firehol_level1" {
		t.Fatalf("IsIPBlocked() = %q, %v", source, err)
	}
	extra := []Source{{ID: "domains", Type: "domain", Enabled: true}}
	if source, err := manager.IsDomainBlocked("https://example.com:8443/path", extra); err != nil || source != "domains" {
		t.Fatalf("IsDomainBlocked() = %q, %v", source, err)
	}
	if source, err := manager.IsDomainBlocked("notexample.com", extra); err != nil || source != "" {
		t.Fatalf("IsDomainBlocked() matched a non-boundary suffix: %q, %v", source, err)
	}
	if source, err := manager.IsDomainBlocked("child.bad.example", extra); err != nil || source != "domains" {
		t.Fatalf("IsDomainBlocked() did not match wildcard subdomain: %q, %v", source, err)
	}
	if source, err := manager.IsDomainBlocked("bad.example", extra); err != nil || source != "" {
		t.Fatalf("IsDomainBlocked() matched wildcard apex: %q, %v", source, err)
	}
}

func TestParseBlocklistIPFiltersTrustedOverlap(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	entries, skipped, err := parseBlocklist([]byte("198.51.100.128/25\n198.51.101.0/24\n"), "ip", trusted)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != "198.51.101.0/24" {
		t.Fatalf("entries = %#v", entries)
	}
	if skipped != 1 {
		t.Fatalf("skipped entries = %d, want 1", skipped)
	}
}

func TestParseBlocklistDomainsNormalizesAndRejectsPublicSuffix(t *testing.T) {
	entries, skipped, err := parseBlocklist([]byte("EXAMPLE.COM.\n*.example.com\n*.com\nhttps://bad.example/path\n"), "domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(entries, ",")
	if got != "*.example.com,example.com" {
		t.Fatalf("entries = %q", got)
	}
	if skipped != 2 {
		t.Fatalf("skipped entries = %d, want 2", skipped)
	}
}

func TestParseSHA256(t *testing.T) {
	want := strings.Repeat("a", 64)
	got, err := parseSHA256([]byte(want + "  database.mmdb\n"))
	if err != nil || got != want {
		t.Fatalf("parseSHA256() = %q, %v", got, err)
	}
	if _, err := parseSHA256([]byte("not-a-digest")); err == nil {
		t.Fatal("parseSHA256 accepted a malformed checksum")
	}
}

func TestUpdateBlocklistsPublishesValidatedFeedsAndPreservesLastGoodCopy(t *testing.T) {
	var mu sync.Mutex
	domainFeed := "EXAMPLE.COM.\n*.bad.example\n*.com\n"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "domain-feed") {
			_, _ = io.WriteString(w, domainFeed)
			return
		}
		_, _ = io.WriteString(w, "203.0.113.4\n10.0.0.0/8\n192.0.2.1\ninvalid\n")
	}))
	defer server.Close()
	manager := New(securityDataTestDir(t))
	manager.Client = rewriteHTTPClient(t, server)
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	extra := []Source{{ID: "domain_feed", Type: "domain", URL: "https://feeds.invalid/domain-feed", Enabled: true}}
	trusted := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	results, err := manager.UpdateBlocklists(context.Background(), extra, trusted)
	if err != nil {
		t.Fatalf("UpdateBlocklists() error: %v", err)
	}
	if got := results["firehol_level1"]; got.ValidEntries != 1 || got.SkippedEntries != 3 {
		t.Errorf("built-in result = %+v; want one valid and three skipped entries", got)
	}
	if got := results["domain_feed"]; got.ValidEntries != 2 || got.SkippedEntries != 1 {
		t.Errorf("domain result = %+v; want two valid entries and one skipped", got)
	}
	blocklistPath := filepath.Join(manager.Dir, "blocklists", "domain_feed.txt")
	if err := os.Chtimes(blocklistPath, now, now); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(blocklistPath)
	if err != nil || string(contents) != "*.bad.example\nexample.com\n" {
		t.Fatalf("stored domain feed = %q, %v", contents, err)
	}
	if status, updated := manager.CheckBlocklist("domain_feed", time.Hour); status != "ok" || !updated.Equal(now) {
		t.Errorf("fresh blocklist status = %q, %v", status, updated)
	}
	if status, _ := manager.CheckBlocklist("domain_feed", 0); status != "ok" {
		t.Errorf("unaged blocklist status = %q, want ok", status)
	}
	now = now.Add(73 * time.Hour)
	if status, _ := manager.CheckBlocklist("domain_feed", 72*time.Hour); status != "degraded" {
		t.Errorf("stale blocklist status = %q, want degraded", status)
	}
	if status, _ := manager.Check("blocklists", 0); status != "ok" {
		t.Errorf("blocklist dataset status = %q, want ok", status)
	}
	mu.Lock()
	domainFeed = "*.com\n"
	mu.Unlock()
	if _, err := manager.UpdateBlocklists(context.Background(), extra, trusted); err == nil {
		t.Fatal("an empty-after-validation domain feed was accepted")
	}
	contents, err = os.ReadFile(blocklistPath)
	if err != nil || string(contents) != "*.bad.example\nexample.com\n" {
		t.Fatalf("last good domain feed changed after invalid update: %q, %v", contents, err)
	}
	if status, _ := manager.CheckBlocklist("invalid/id", 0); status != "error" {
		t.Errorf("invalid blocklist ID status = %q, want error", status)
	}
}

func TestUpdateBlocklistsRejectsReservedAndInvalidSources(t *testing.T) {
	manager := New(securityDataTestDir(t))
	if _, err := manager.UpdateBlocklists(context.Background(), []Source{{ID: "firehol_level1", Type: "ip", URL: "https://example.com/list", Enabled: true}}, nil); err == nil {
		t.Fatal("reserved built-in source was accepted")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "203.0.113.4\n")
	}))
	defer server.Close()
	manager.Client = rewriteHTTPClient(t, server)
	for _, source := range []Source{
		{ID: "bad/id", Type: "ip", URL: "https://feeds.invalid/list", Enabled: true},
		{ID: "bad_type", Type: "country", URL: "https://feeds.invalid/list", Enabled: true},
		{ID: "plain_http", Type: "ip", URL: "http://feeds.invalid/list", Enabled: true},
	} {
		_, err := manager.UpdateBlocklists(context.Background(), []Source{source}, nil)
		if err == nil {
			t.Errorf("UpdateBlocklists() accepted invalid source %+v", source)
		}
	}
}

func TestUpdateNVDStoresAndReplacesIncrementalRecords(t *testing.T) {
	var mu sync.Mutex
	record := `{"id":"CVE-2026-1234","description":"first"}`
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.URL.Query().Get("resultsPerPage") != "2000" || r.URL.Query().Get("startIndex") != "0" {
			t.Errorf("NVD paging query = %q", r.URL.RawQuery)
		}
		if r.Header.Get("apiKey") != "test-key" {
			t.Errorf("NVD API key header = %q", r.Header.Get("apiKey"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"totalResults":1,"vulnerabilities":[{"cve":`+record+`}]}`)
	}))
	defer server.Close()
	manager := New(securityDataTestDir(t))
	manager.Client = rewriteHTTPClient(t, server)
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if err := manager.UpdateNVD(context.Background(), "test-key"); err != nil {
		t.Fatalf("first UpdateNVD() error: %v", err)
	}
	now = now.Add(24 * time.Hour)
	record = `{"id":"CVE-2026-1234","description":"updated"}`
	if err := manager.UpdateNVD(context.Background(), "test-key"); err != nil {
		t.Fatalf("incremental UpdateNVD() error: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(manager.Dir, "cve", "nvd.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]string
	if err := json.Unmarshal(contents, &records); err != nil {
		t.Fatalf("stored NVD records are invalid JSON: %v", err)
	}
	if len(records) != 1 || records[0]["description"] != "updated" {
		t.Fatalf("stored NVD records = %s", contents)
	}
	if requests != 2 {
		t.Errorf("NVD requests = %d, want 2", requests)
	}
	if status, _ := manager.Check("cve", 48*time.Hour); status != "ok" {
		t.Errorf("fresh CVE dataset status = %q, want ok", status)
	}
}

func TestUpdateNVDFailuresLeaveLastGoodCopyAndCheckFreshness(t *testing.T) {
	fail := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"totalResults":1,"vulnerabilities":[{"cve":{"id":"CVE-2026-1234"}}]}`)
	}))
	defer server.Close()
	manager := New(securityDataTestDir(t))
	manager.Client = rewriteHTTPClient(t, server)
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if err := manager.UpdateNVD(context.Background(), ""); err != nil {
		t.Fatalf("initial UpdateNVD() error: %v", err)
	}
	path := filepath.Join(manager.Dir, "cve", "nvd.json")
	previous, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := manager.UpdateNVD(context.Background(), ""); err == nil {
		t.Fatal("NVD HTTP failure was accepted")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(previous) {
		t.Fatalf("NVD last good copy changed after HTTP failure: %q, %v", current, err)
	}
	if status, _ := manager.Check("missing", 0); status != "error" {
		t.Errorf("missing dataset status = %q, want error", status)
	}
	now = now.Add(2 * time.Hour)
	if status, _ := manager.Check("cve", time.Hour); status != "degraded" {
		t.Errorf("stale CVE dataset status = %q, want degraded", status)
	}
}

func TestDoBoundsResponsesAndRejectsStatusAndInsecureRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = io.WriteString(w, "too large")
		case "/error":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer server.Close()
	manager := New(securityDataTestDir(t))
	manager.Client = server.Client()
	for _, test := range []struct {
		path  string
		limit int64
		want  string
		fail  bool
	}{
		{path: "/ok", limit: 2, want: "ok"},
		{path: "/large", limit: 2, fail: true},
		{path: "/error", limit: 32, fail: true},
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := manager.do(request, test.limit)
		if (err != nil) != test.fail || string(got) != test.want {
			t.Errorf("do(%s) = %q, %v; want %q, fail=%t", test.path, got, err, test.want, test.fail)
		}
	}
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "insecure") }))
	defer insecure.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, insecure.URL, http.StatusFound) }))
	defer redirect.Close()
	manager.Client = redirect.Client()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirect.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.do(request, 32); err == nil || !strings.Contains(err.Error(), "redirected away from HTTPS") {
		t.Fatalf("HTTPS redirect to HTTP error = %v", err)
	}
}

func TestSecurityDataFreshnessReportsMissingCurrentAndStaleDatasets(t *testing.T) {
	manager := New(securityDataTestDir(t))
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if status, _ := manager.Check("cve", time.Hour); status != "error" {
		t.Fatalf("missing CVE dataset status = %q, want error", status)
	}
	marker := filepath.Join(manager.Dir, "cve", ".last_updated")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(now.Add(-30*time.Minute).Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, updated := manager.Check("cve", time.Hour); status != "ok" || !updated.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("fresh CVE status = %q at %s", status, updated)
	}
	if status, _ := manager.Check("cve", 0); status != "ok" {
		t.Fatalf("unbounded-age CVE status = %q, want ok", status)
	}
	if status, _ := manager.Check("cve", time.Minute); status != "degraded" {
		t.Fatalf("stale CVE status = %q, want degraded", status)
	}
	if err := os.WriteFile(marker, []byte("invalid timestamp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _ := manager.Check("cve", time.Hour); status != "error" {
		t.Fatalf("malformed CVE marker status = %q, want error", status)
	}

	blocklist := filepath.Join(manager.Dir, "blocklists", "sample.txt")
	if err := os.MkdirAll(filepath.Dir(blocklist), 0o700); err != nil {
		t.Fatal(err)
	}
	if status, _ := manager.CheckBlocklist("sample", time.Hour); status != "error" {
		t.Fatalf("missing blocklist status = %q, want error", status)
	}
	if err := os.WriteFile(blocklist, []byte("203.0.113.1/32\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, updated := manager.CheckBlocklist("sample", time.Hour); status != "ok" || updated.IsZero() {
		t.Fatalf("fresh blocklist status = %q at %s", status, updated)
	}
	if status, _ := manager.CheckBlocklist("../sample", time.Hour); status != "error" {
		t.Fatalf("invalid blocklist ID status = %q, want error", status)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(blocklist, old, old); err != nil {
		t.Fatal(err)
	}
	if status, _ := manager.CheckBlocklist("sample", time.Hour); status != "degraded" {
		t.Fatalf("stale blocklist status = %q, want degraded", status)
	}
}

func TestUpdateTrivySkipsFreshCachedDatabase(t *testing.T) {
	manager := New(securityDataTestDir(t))
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	directory := filepath.Join(manager.Dir, "trivy")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := `{"Version":2,"NextUpdate":"2026-11-23T00:00:00Z","UpdatedAt":"2026-11-22T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(directory, "metadata.json"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.UpdateTrivy(context.Background(), "invalid-reference")
	if err != nil || updated {
		t.Fatalf("fresh Trivy database update = %t, %v; want false, nil", updated, err)
	}
}

func rewriteHTTPClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	baseTransport := server.Client().Transport
	return &http.Client{Transport: rewriteTransport{base: baseTransport, destination: baseURL}}
}

type rewriteTransport struct {
	base        http.RoundTripper
	destination *url.URL
}

func (r rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	urlCopy := *request.URL
	urlCopy.Scheme = r.destination.Scheme
	urlCopy.Host = r.destination.Host
	cloned.URL = &urlCopy
	return r.base.RoundTrip(cloned)
}
