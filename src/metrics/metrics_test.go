package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestMiddlewareExportsHTTPAndRuntimeMetrics(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.IncludeSystem = false
	cfg.IncludeRuntime = true
	exporter := New(cfg, "1.2.3", "abc123", "2026-01-01T00:00:00Z")
	application := exporter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	response := httptest.NewRecorder()
	application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/types", nil))

	metricsResponse := httptest.NewRecorder()
	exporter.Prometheus().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	appInfo := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "gitmessages_app_info{") {
			appInfo = line
			break
		}
	}
	for _, label := range []string{`version="1.2.3"`, `commit="abc123"`, `build_date="2026-01-01T00:00:00Z"`, `go_version="`} {
		if !strings.Contains(appInfo, label) {
			t.Errorf("application info metric does not contain %q: %s", label, appInfo)
		}
	}
	for _, metric := range []string{
		"gitmessages_app_uptime_seconds",
		"gitmessages_http_requests_total{method=\"GET\",path=\"/api/v1/types\",status=\"201\"} 1",
		"gitmessages_http_request_duration_seconds_count{method=\"GET\",path=\"/api/v1/types\"} 1",
		"gitmessages_http_response_size_bytes_count{method=\"GET\",path=\"/api/v1/types\"} 1",
		"gitmessages_go_goroutines",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output does not contain %q", metric)
		}
	}
}

func TestTorMetricsUseLiveProvider(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.IncludeSystem = false
	cfg.IncludeRuntime = false
	exporter := New(cfg, "dev", "unknown", "N/A")
	exporter.SetTorStatusProvider(func() (bool, bool, bool, uint64) { return true, true, true, 17 })
	response := httptest.NewRecorder()
	exporter.Prometheus().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{
		"gitmessages_tor_enabled 1",
		"gitmessages_tor_running 1",
		"gitmessages_tor_circuit_established 1",
		"gitmessages_tor_requests_total 17",
	} {
		if !strings.Contains(response.Body.String(), metric) {
			t.Errorf("metrics output does not contain %q", metric)
		}
	}
}

func TestNormalizePathBoundsIdentifierCardinality(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"/api/v1/types", "/api/v1/types"},
		{"/api/v1/items/0123456789abcdef", "/api/v1/items/:id"},
		{"/api/v1/items/short", "/api/v1/items/short"},
	} {
		if got := normalizePath(test.input); got != test.want {
			t.Errorf("normalizePath(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestLokiLogBufferRedactsCredentials(t *testing.T) {
	buffer := &logBuffer{}
	buffer.setRedactions("private-token")
	buffer.addMessage(`failed request Authorization: Bearer private-token password="sensitive-value"`)
	entries := buffer.recent(time.Now().Add(-time.Minute), 10)
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	for _, secret := range []string{"private-token", "sensitive-value"} {
		if strings.Contains(entries[0].Message, secret) {
			t.Errorf("Loki log retained secret %q: %s", secret, entries[0].Message)
		}
	}
}
