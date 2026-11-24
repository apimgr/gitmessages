package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/cache"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/scheduler"
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

func TestDatabaseAndSchedulerMetricsAreExported(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.IncludeSystem = false
	cfg.IncludeRuntime = false
	exporter := New(cfg, "dev", "unknown", "N/A")
	exporter.SetDatabaseStatsProvider(func() (int, int) { return 3, 1 })
	exporter.SetOwnerTokenProvider(func() (int, int) { return 2, 4 })
	exporter.RecordAuthMethod("owner_token", true)
	exporter.RecordAuthMethod("operator_token", false)
	exporter.ObserveDatabaseQuery(database.QueryObservation{Operation: "select", Table: "owners", Duration: 5 * time.Millisecond})
	exporter.ObserveDatabaseQuery(database.QueryObservation{Operation: "insert", Table: "audit_log", ErrorType: "constraint", Duration: 2 * time.Millisecond})
	exporter.ObserveDatabaseBusy(1200 * time.Millisecond)
	exporter.ObserveScheduler(scheduler.ExecutionEvent{Task: "geoip_update", Running: true})
	exporter.ObserveScheduler(scheduler.ExecutionEvent{Task: "geoip_update", Status: "success", Duration: 2 * time.Second, LastRun: time.Unix(100, 0)})
	exporter.RecordMessageBuild()
	exporter.ObserveCacheOperation("rate_limit", false, cache.Stats{Items: 2, Bytes: 48})
	exporter.ObserveCacheOperation("rate_limit", true, cache.Stats{Items: 3, Bytes: 72, Evictions: 1})
	response := httptest.NewRecorder()
	exporter.Prometheus().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, metric := range []string{
		"gitmessages_db_queries_total{operation=\"select\",table=\"owners\"} 1",
		"gitmessages_db_errors_total{error_type=\"constraint\",operation=\"insert\"} 1",
		"gitmessages_db_connections_open 3",
		"gitmessages_db_connections_in_use 1",
		"gitmessages_db_busy_total 1",
		"gitmessages_db_write_wait_seconds_count 1",
		"gitmessages_owner_tokens_active{kind=\"primary\"} 2",
		"gitmessages_owner_tokens_active{kind=\"linked\"} 4",
		"gitmessages_auth_attempts_total{method=\"owner_token\",status=\"success\"} 1",
		"gitmessages_auth_attempts_total{method=\"operator_token\",status=\"failed\"} 1",
		"gitmessages_scheduler_tasks_total{status=\"success\",task=\"geoip_update\"} 1",
		"gitmessages_scheduler_tasks_running{task=\"geoip_update\"} 0",
		"gitmessages_scheduler_last_run_timestamp{task=\"geoip_update\"} 100",
		"gitmessages_commit_message_builds_total 1",
		"gitmessages_cache_misses_total{cache=\"rate_limit\"} 1",
		"gitmessages_cache_hits_total{cache=\"rate_limit\"} 1",
		"gitmessages_cache_evictions_total{cache=\"rate_limit\"} 1",
		"gitmessages_cache_size{cache=\"rate_limit\"} 3",
		"gitmessages_cache_bytes{cache=\"rate_limit\"} 72",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("metrics output does not contain %q", metric)
		}
	}
}
