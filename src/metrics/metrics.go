package metrics

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

var (
	durationBuckets      = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	sizeBuckets          = []float64{100, 1000, 10000, 100000, 1000000, 10000000}
	applicationLogs      = &logBuffer{}
	credentialPattern    = regexp.MustCompile(`(?i)(authorization|token|password|secret|api[_-]?key)([[:space:]"']*[:=][[:space:]"']*)([^[:space:],;"'}]+)`)
	bearerPattern        = regexp.MustCompile(`(?i)(bearer[[:space:]]+)[A-Za-z0-9._~+/=-]+`)
	basicPattern         = regexp.MustCompile(`(?i)(basic[[:space:]]+)[A-Za-z0-9+/=]+`)
	urlCredentialPattern = regexp.MustCompile(`(://)[^/:@]+:[^/@]+@`)
)

type Exporter struct {
	registry         *prometheus.Registry
	requests         *prometheus.CounterVec
	duration         *prometheus.HistogramVec
	requestSize      *prometheus.HistogramVec
	responseSize     *prometheus.HistogramVec
	active           prometheus.Gauge
	appInfo          *prometheus.GaugeVec
	rateLimits       *prometheus.CounterVec
	rateLimitBlocked *prometheus.CounterVec
	authAttempts     *prometheus.CounterVec
	ownerTokens      *prometheus.GaugeVec
	started          time.Time
	includeRuntime   bool
	includeSystem    bool
	lokiMaxEntries   int
	lokiMaxAge       time.Duration
	logs             *logBuffer
	torStatusMu      sync.RWMutex
	torStatus        func() (bool, bool, bool, uint64)
}

func New(cfg config.MetricsConfig, version, commit, buildDate string) *Exporter {
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("gitmessages_", registry)
	buckets := validBuckets(cfg.DurationBuckets, durationBuckets)
	sizes := validBuckets(cfg.SizeBuckets, sizeBuckets)
	e := &Exporter{
		registry:         registry,
		requests:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "Total HTTP requests"}, []string{"method", "path", "status"}),
		duration:         prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration in seconds", Buckets: buckets}, []string{"method", "path"}),
		requestSize:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_request_size_bytes", Help: "HTTP request size in bytes", Buckets: sizes}, []string{"method", "path"}),
		responseSize:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_response_size_bytes", Help: "HTTP response size in bytes", Buckets: sizes}, []string{"method", "path"}),
		active:           prometheus.NewGauge(prometheus.GaugeOpts{Name: "http_active_requests", Help: "Number of active HTTP requests"}),
		appInfo:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "app_info", Help: "Application information"}, []string{"version", "commit", "build_date", "go_version"}),
		rateLimits:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ratelimit_requests_total", Help: "Total requests processed by the global rate limiter"}, []string{"limit", "status"}),
		rateLimitBlocked: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ratelimit_blocked_total", Help: "Requests blocked by the rate limiter"}, []string{"limit"}),
		authAttempts:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "auth_attempts_total", Help: "Total metrics bearer-token authentication attempts"}, []string{"method", "status"}),
		ownerTokens:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "owner_tokens_active", Help: "Active owner tokens; this service has no owner-token system"}, []string{"kind"}),
		started:          time.Now(), includeRuntime: cfg.IncludeRuntime, includeSystem: cfg.IncludeSystem,
		lokiMaxEntries: cfg.Loki.MaxEntries, logs: applicationLogs,
	}
	e.logs.setRedactions(cfg.Auth.Tokens.Prometheus, cfg.Auth.Tokens.Grafana, cfg.Auth.Tokens.Loki)
	if e.lokiMaxEntries <= 0 {
		e.lokiMaxEntries = 1000
	}
	e.lokiMaxAge, _ = time.ParseDuration(cfg.Loki.MaxAge)
	if e.lokiMaxAge <= 0 {
		e.lokiMaxAge = time.Hour
	}
	for _, collector := range []prometheus.Collector{e.requests, e.duration, e.requestSize, e.responseSize, e.active, e.appInfo, e.rateLimits, e.rateLimitBlocked, e.authAttempts, e.ownerTokens} {
		registerer.MustRegister(collector)
	}
	e.ownerTokens.WithLabelValues("primary").Set(0)
	e.ownerTokens.WithLabelValues("linked").Set(0)
	registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "app_uptime_seconds", Help: "Application uptime in seconds"}, func() float64 { return time.Since(e.started).Seconds() }))
	registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "app_start_timestamp", Help: "Unix timestamp when the application started"}, func() float64 { return float64(e.started.Unix()) }))
	registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "tor_enabled", Help: "1 if the Tor hidden service is enabled, otherwise 0"}, func() float64 {
		enabled, _, _, _ := e.torState()
		if enabled {
			return 1
		}
		return 0
	}))
	registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "tor_running", Help: "1 if the Tor process is running, otherwise 0"}, func() float64 {
		_, running, _, _ := e.torState()
		if running {
			return 1
		}
		return 0
	}))
	registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "tor_circuit_established", Help: "1 if a Tor circuit is established, otherwise 0"}, func() float64 {
		_, _, established, _ := e.torState()
		if established {
			return 1
		}
		return 0
	}))
	registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "tor_requests_total", Help: "Total requests served through the Tor hidden service"}, func() float64 { _, _, _, requests := e.torState(); return float64(requests) }))
	if e.includeRuntime {
		registerer.MustRegister(runtimeCollector())
	}
	if e.includeSystem {
		registerer.MustRegister(systemCollector(paths.GetDirectories().Data))
	}
	e.appInfo.WithLabelValues(version, commit, buildDate, runtime.Version()).Set(1)
	return e
}

func validBuckets(values, fallback []float64) []float64 {
	if len(values) == 0 {
		return fallback
	}
	previous := 0.0
	for _, value := range values {
		if value <= previous || math.IsNaN(value) || math.IsInf(value, 0) {
			return fallback
		}
		previous = value
	}
	return append([]float64(nil), values...)
}

func (e *Exporter) SetTorStatusProvider(provider func() (bool, bool, bool, uint64)) {
	e.torStatusMu.Lock()
	e.torStatus = provider
	e.torStatusMu.Unlock()
}

func (e *Exporter) torState() (bool, bool, bool, uint64) {
	e.torStatusMu.RLock()
	provider := e.torStatus
	e.torStatusMu.RUnlock()
	if provider == nil {
		return false, false, false, 0
	}
	return provider()
}

func (e *Exporter) RecordAuth(success bool) {
	status := "failed"
	if success {
		status = "success"
	}
	e.authAttempts.WithLabelValues("metrics_token", status).Inc()
}

func (e *Exporter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		path := normalizePath(r.URL.Path)
		e.active.Inc()
		defer e.active.Dec()
		requestSize := r.ContentLength
		if requestSize < 0 {
			requestSize = 0
		}
		e.requestSize.WithLabelValues(r.Method, path).Observe(float64(requestSize))
		wrapped := &responseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		elapsed := time.Since(started)
		if wrapped.status == 0 {
			wrapped.status = http.StatusOK
		}
		status := strconv.Itoa(wrapped.status)
		e.requests.WithLabelValues(r.Method, path, status).Inc()
		if wrapped.status == http.StatusTooManyRequests {
			e.rateLimits.WithLabelValues("global", "limited").Inc()
			e.rateLimitBlocked.WithLabelValues("global").Inc()
		} else {
			e.rateLimits.WithLabelValues("global", "allowed").Inc()
		}
		e.duration.WithLabelValues(r.Method, path).Observe(elapsed.Seconds())
		e.responseSize.WithLabelValues(r.Method, path).Observe(float64(wrapped.bytes))
		e.logs.add(logEntry{Time: time.Now(), Level: "info", Method: r.Method, Path: path, Status: wrapped.status, Duration: elapsed})
	})
}

type responseWriter struct {
	http.ResponseWriter
	status, bytes int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 || status < 100 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func normalizePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if len(part) >= 16 {
			allHex := true
			for _, r := range part {
				if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') && r != '-' {
					allHex = false
					break
				}
			}
			if allHex {
				parts[i] = ":id"
			}
		}
	}
	return strings.Join(parts, "/")
}

func (e *Exporter) Prometheus() http.Handler {
	return promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{DisableCompression: true})
}

func (e *Exporter) Grafana() http.Handler {
	panels := []map[string]any{
		grafanaPanel(1, "timeseries", "HTTP requests per second", "rate(gitmessages_http_requests_total[5m])"),
		grafanaPanel(2, "timeseries", "HTTP request duration", "histogram_quantile(0.95, sum(rate(gitmessages_http_request_duration_seconds_bucket[5m])) by (le))"),
		grafanaPanel(3, "stat", "Application uptime", "gitmessages_app_uptime_seconds"),
		grafanaPanel(4, "timeseries", "System memory used", "gitmessages_system_memory_used_bytes"),
		grafanaPanel(5, "timeseries", "Go goroutines", "gitmessages_go_goroutines"),
		grafanaPanel(6, "timeseries", "Tor status", "gitmessages_tor_running"),
		grafanaPanel(7, "timeseries", "Tor circuit established", "gitmessages_tor_circuit_established"),
		grafanaPanel(8, "timeseries", "Tor requests", "rate(gitmessages_tor_requests_total[5m])"),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": nil, "uid": "gitmessages", "title": "Git Messages", "schemaVersion": 42, "version": 1, "refresh": "30s", "time": map[string]string{"from": "now-1h", "to": "now"}, "templating": map[string]any{"list": []any{map[string]any{"name": "DS_PROMETHEUS", "label": "Prometheus datasource", "type": "datasource", "query": "prometheus", "refresh": 1, "regex": "", "options": []any{}, "hide": 0, "includeAll": false, "multi": false, "current": map[string]string{"text": "Prometheus", "value": "Prometheus"}}}}, "panels": panels})
	})
}

func grafanaPanel(id int, panelType, title, expression string) map[string]any {
	return map[string]any{
		"id":          id,
		"type":        panelType,
		"title":       title,
		"datasource":  map[string]string{"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
		"gridPos":     map[string]int{"h": 8, "w": 24, "x": 0, "y": (id - 1) * 8},
		"fieldConfig": map[string]any{"defaults": map[string]any{}, "overrides": []any{}},
		"options":     map[string]any{},
		"targets":     []map[string]any{{"refId": "A", "expr": expression, "datasource": map[string]string{"type": "prometheus", "uid": "${DS_PROMETHEUS}"}}},
	}
}

func (e *Exporter) Loki() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries := e.logs.recent(time.Now().Add(-e.lokiMaxAge), e.lokiMaxEntries)
		grouped := make(map[string][][2]string)
		for _, entry := range entries {
			line := entry.Message
			if line == "" {
				encoded, _ := json.Marshal(map[string]any{"level": entry.Level, "method": entry.Method, "path": entry.Path, "status": entry.Status, "duration_seconds": entry.Duration.Seconds()})
				line = string(encoded)
			}
			grouped[entry.Level] = append(grouped[entry.Level], [2]string{strconv.FormatInt(entry.Time.UnixNano(), 10), line})
		}
		streams := make([]map[string]any, 0, len(grouped))
		for level, values := range grouped {
			streams = append(streams, map[string]any{"stream": map[string]string{"app": "gitmessages", "level": level}, "values": values})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"streams": streams})
	})
}

type logEntry struct {
	Time                         time.Time
	Level, Message, Method, Path string
	Status                       int
	Duration                     time.Duration
}
type logBuffer struct {
	mu         sync.Mutex
	entries    []logEntry
	redactions []string
}

func (b *logBuffer) add(entry logEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, entry)
	if len(b.entries) > 5000 {
		b.entries = append([]logEntry(nil), b.entries[len(b.entries)-5000:]...)
	}
}
func (b *logBuffer) addMessage(line string) {
	line = b.sanitize(line)
	b.add(logEntry{Time: time.Now(), Level: logLevel(line), Message: line})
}
func (b *logBuffer) setRedactions(values ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, value := range values {
		if value != "" {
			b.redactions = append(b.redactions, value)
		}
	}
}
func (b *logBuffer) sanitize(line string) string {
	b.mu.Lock()
	redactions := append([]string(nil), b.redactions...)
	b.mu.Unlock()
	line = credentialPattern.ReplaceAllString(line, "$1$2xxxxx")
	line = bearerPattern.ReplaceAllString(line, "${1}xxxxx")
	line = basicPattern.ReplaceAllString(line, "${1}xxxxx")
	line = urlCredentialPattern.ReplaceAllString(line, "$1xxxxx:xxxxx@")
	for _, secret := range redactions {
		line = strings.ReplaceAll(line, secret, "xxxxx")
	}
	return line
}
func (b *logBuffer) recent(after time.Time, limit int) []logEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]logEntry, 0, limit)
	for i := len(b.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if b.entries[i].Time.After(after) {
			out = append(out, b.entries[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func logLevel(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"), strings.Contains(lower, "fatal"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "debug"):
		return "debug"
	default:
		return "info"
	}
}

type logWriter struct {
	mu      sync.Mutex
	pending []byte
}

func (w *logWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for {
		index := strings.IndexByte(string(w.pending), '\n')
		if index < 0 {
			break
		}
		applicationLogs.addMessage(strings.TrimSpace(string(w.pending[:index])))
		w.pending = append([]byte(nil), w.pending[index+1:]...)
	}
	return len(data), nil
}

var applicationLogWriter = &logWriter{}

func LogWriter() io.Writer { return applicationLogWriter }

type runtimeStats struct {
	goroutines, allocated, system, gcRuns, gcPause *prometheus.Desc
}

func runtimeCollector() prometheus.Collector {
	return runtimeStats{
		goroutines: prometheus.NewDesc("gitmessages_go_goroutines", "Number of goroutines", nil, nil),
		allocated:  prometheus.NewDesc("gitmessages_go_mem_alloc_bytes", "Bytes allocated and in use", nil, nil),
		system:     prometheus.NewDesc("gitmessages_go_mem_sys_bytes", "Bytes obtained from system", nil, nil),
		gcRuns:     prometheus.NewDesc("gitmessages_go_gc_runs_total", "Total number of garbage collection runs", nil, nil),
		gcPause:    prometheus.NewDesc("gitmessages_go_gc_pause_total_seconds", "Total time spent in GC pauses", nil, nil),
	}
}

func (c runtimeStats) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.goroutines
	ch <- c.allocated
	ch <- c.system
	ch <- c.gcRuns
	ch <- c.gcPause
}

func (c runtimeStats) Collect(ch chan<- prometheus.Metric) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	ch <- prometheus.MustNewConstMetric(c.goroutines, prometheus.GaugeValue, float64(runtime.NumGoroutine()))
	ch <- prometheus.MustNewConstMetric(c.allocated, prometheus.GaugeValue, float64(stats.Alloc))
	ch <- prometheus.MustNewConstMetric(c.system, prometheus.GaugeValue, float64(stats.Sys))
	ch <- prometheus.MustNewConstMetric(c.gcRuns, prometheus.CounterValue, float64(stats.NumGC))
	ch <- prometheus.MustNewConstMetric(c.gcPause, prometheus.CounterValue, float64(stats.PauseTotalNs)/1e9)
}

type systemStats struct {
	memoryUsed, memoryTotal, memoryPercent, cpuPercent *prometheus.Desc
	diskPercent, diskUsed, diskTotal                   *prometheus.Desc
	dataDir                                            string
}

func systemCollector(dataDir string) prometheus.Collector {
	return systemStats{
		memoryUsed:    prometheus.NewDesc("gitmessages_system_memory_used_bytes", "Memory currently in use", nil, nil),
		memoryTotal:   prometheus.NewDesc("gitmessages_system_memory_total_bytes", "Total system memory", nil, nil),
		memoryPercent: prometheus.NewDesc("gitmessages_system_memory_usage_percent", "Current memory usage percentage", nil, nil),
		cpuPercent:    prometheus.NewDesc("gitmessages_system_cpu_usage_percent", "Current CPU usage percentage", nil, nil),
		diskPercent:   prometheus.NewDesc("gitmessages_system_disk_usage_percent", "Disk usage percentage", []string{"path"}, nil),
		diskUsed:      prometheus.NewDesc("gitmessages_system_disk_used_bytes", "Disk space used", []string{"path"}, nil),
		diskTotal:     prometheus.NewDesc("gitmessages_system_disk_total_bytes", "Total disk space", []string{"path"}, nil),
		dataDir:       filepath.Clean(dataDir),
	}
}

func (c systemStats) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.memoryUsed
	ch <- c.memoryTotal
	ch <- c.memoryPercent
	ch <- c.cpuPercent
	ch <- c.diskPercent
	ch <- c.diskUsed
	ch <- c.diskTotal
}

func (c systemStats) Collect(ch chan<- prometheus.Metric) {
	if memory, err := mem.VirtualMemory(); err == nil && memory.Total > 0 {
		ch <- prometheus.MustNewConstMetric(c.memoryUsed, prometheus.GaugeValue, float64(memory.Used))
		ch <- prometheus.MustNewConstMetric(c.memoryTotal, prometheus.GaugeValue, float64(memory.Total))
		ch <- prometheus.MustNewConstMetric(c.memoryPercent, prometheus.GaugeValue, memory.UsedPercent)
	}
	if percentages, err := cpu.Percent(0, false); err == nil && len(percentages) > 0 {
		ch <- prometheus.MustNewConstMetric(c.cpuPercent, prometheus.GaugeValue, percentages[0])
	}
	if usage, err := disk.Usage(c.dataDir); err == nil && usage.Total > 0 {
		labels := []string{c.dataDir}
		ch <- prometheus.MustNewConstMetric(c.diskPercent, prometheus.GaugeValue, usage.UsedPercent, labels...)
		ch <- prometheus.MustNewConstMetric(c.diskUsed, prometheus.GaugeValue, float64(usage.Used), labels...)
		ch <- prometheus.MustNewConstMetric(c.diskTotal, prometheus.GaugeValue, float64(usage.Total), labels...)
	}
}
