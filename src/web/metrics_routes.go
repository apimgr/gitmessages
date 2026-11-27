package web

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/metrics"
)

func metricsRouteDefinitions(r *http.Request, cfg config.MetricsConfig, exporter *metrics.Exporter) []Route {
	exporter.SetTorStatusProvider(func() (bool, bool, bool, uint64) {
		status, ok := overlayStatuses()["tor"]
		if !ok {
			return false, false, false, 0
		}
		encoded, err := json.Marshal(status)
		if err != nil {
			return false, false, false, 0
		}
		var tor struct {
			Enabled            bool   `json:"enabled"`
			Running            bool   `json:"running"`
			CircuitEstablished bool   `json:"circuit_established"`
			RequestsTotal      uint64 `json:"requests_total"`
		}
		if json.Unmarshal(encoded, &tor) != nil {
			return false, false, false, 0
		}
		return tor.Enabled, tor.Running, tor.CircuitEstablished, tor.RequestsTotal
	})
	prometheus := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, exporter.Prometheus(), exporter)
	grafana := metricsAuth(cfg, cfg.Auth.Tokens.Grafana, exporter.Grafana(), exporter)
	loki := metricsAuth(cfg, cfg.Auth.Tokens.Loki, exporter.Loki(), exporter)

	definitions := make([]Route, 0, 20)
	mount := func(prefix, mountedWhen string) {
		for _, route := range []struct {
			path    string
			handler http.Handler
		}{
			{path: prefix, handler: prometheus},
			{path: prefix + "/prometheus", handler: prometheus},
			{path: prefix + "/grafana", handler: grafana},
			{path: prefix + "/loki", handler: loki},
		} {
			definitions = append(definitions, Route{
				Method: http.MethodGet, Path: route.path, OperationID: swaggerOperationID("getMetrics", route.path),
				Summary: tr(r, "swagger.metrics.summary"), Description: tr(r, "swagger.metrics.response"),
				Handler: http.HandlerFunc(route.handler.ServeHTTP), Auth: "service", Internal: true,
				MountedWhen: mountedWhen,
			})
		}
	}
	mount("/server/metrics", "server.metrics.enabled")
	mount(APIBasePath()+"/server/metrics", "server.metrics.enabled")
	mount("/api/metrics", "server.metrics.enabled")
	if cfg.Root.Enabled {
		mount("/metrics", "server.metrics.root.enabled")
	}
	return definitions
}

func configuredMetricsRouteDefinitions(r *http.Request) []Route {
	cfg := config.Get().Server.Metrics
	if !cfg.Enabled {
		return nil
	}
	metricsExporterMu.RLock()
	exporter := activeMetricsExporter
	metricsExporterMu.RUnlock()
	if exporter == nil {
		return nil
	}
	return metricsRouteDefinitions(r, cfg, exporter)
}

func registerMetricsRoutes(mux routeRegistrar, cfg config.MetricsConfig, exporter *metrics.Exporter) {
	request := &http.Request{Header: make(http.Header)}
	for _, route := range metricsRouteDefinitions(request, cfg, exporter) {
		mux.Handle(route.Path, route.Handler)
	}
}

func metricsAuth(cfg config.MetricsConfig, token string, handler http.Handler, exporter *metrics.Exporter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "Authorization")
		if !method(w, r, http.MethodGet) {
			return
		}
		if r.URL.Query().Has("token") {
			http.Error(w, "Metrics tokens must be sent in the Authorization header", http.StatusBadRequest)
			return
		}
		if cfg.Auth.AllowUnauthenticated {
			handler.ServeHTTP(w, r)
			return
		}
		provided := r.Header.Get("Authorization")
		if token == "" {
			http.Error(w, "Metrics authentication is not configured", http.StatusServiceUnavailable)
			return
		}
		wanted := "Bearer " + token
		if len(provided) != len(wanted) || subtle.ConstantTimeCompare([]byte(provided), []byte(wanted)) != 1 {
			exporter.RecordAuth(false)
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		exporter.RecordAuth(true)
		handler.ServeHTTP(w, r)
	})
}
