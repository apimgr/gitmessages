package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/metrics"
)

func TestMetricsRoutesRequireServiceTokensAndServeFormats(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Enabled = true
	cfg.Auth.Tokens = config.MetricsTokenConfig{Prometheus: "prom-secret", Grafana: "graf-secret", Loki: "loki-secret"}
	exporter := metrics.New(cfg, "1.2.3", "abc123", "2026-01-01T00:00:00Z")
	mux := http.NewServeMux()
	registerMetricsRoutes(mux, cfg, exporter)
	handler := exporter.Middleware(mux)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/server/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("unauthenticated metrics request = %d, want 401 with bearer challenge", unauthorized.Code)
	}

	prometheus := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/metrics/prometheus", nil)
	request.Header.Set("Authorization", "Bearer prom-secret")
	handler.ServeHTTP(prometheus, request)
	if prometheus.Code != http.StatusOK || !strings.Contains(prometheus.Body.String(), "gitmessages_http_requests_total") {
		t.Fatalf("Prometheus request = %d, body %q", prometheus.Code, prometheus.Body.String())
	}

	grafana := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/server/metrics/grafana", nil)
	request.Header.Set("Authorization", "Bearer graf-secret")
	handler.ServeHTTP(grafana, request)
	var dashboard map[string]any
	if grafana.Code != http.StatusOK || json.Unmarshal(grafana.Body.Bytes(), &dashboard) != nil || dashboard["title"] != "Git Messages" {
		t.Fatalf("Grafana request = %d, body %q", grafana.Code, grafana.Body.String())
	}

	loki := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/metrics/loki", nil)
	request.Header.Set("Authorization", "Bearer loki-secret")
	handler.ServeHTTP(loki, request)
	if loki.Code != http.StatusOK || !strings.Contains(loki.Body.String(), "streams") {
		t.Fatalf("Loki request = %d, body %q", loki.Code, loki.Body.String())
	}

	queryToken := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/metrics?token=prom-secret", nil)
	request.Header.Set("Authorization", "Bearer prom-secret")
	handler.ServeHTTP(queryToken, request)
	if queryToken.Code != http.StatusBadRequest {
		t.Fatalf("query-string token accepted: status %d", queryToken.Code)
	}
}

func TestMetricsRoutesAreGetOnly(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Enabled = true
	cfg.Auth.Tokens.Prometheus = "prom-secret"
	exporter := metrics.New(cfg, "dev", "unknown", "N/A")
	mux := http.NewServeMux()
	registerMetricsRoutes(mux, cfg, exporter)
	request := httptest.NewRequest(http.MethodPost, "/server/metrics", nil)
	request.Header.Set("Authorization", "Bearer prom-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /server/metrics status = %d, want 405", response.Code)
	}
}
