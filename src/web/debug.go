package web

import (
	"context"
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/mode"
	"github.com/apimgr/gitmessages/src/scheduler"
)

var (
	debugSchedulerMu sync.RWMutex
	debugScheduler   *scheduler.Scheduler
	debugCredential  = regexp.MustCompile(`(?i)(?:\b(?:adm|own|mtr)_[A-Za-z0-9_-]{16,}\b|(["']?(?:password|secret|token|session|code|key|auth|pwd|api[_-]?key|access[_-]?token|refresh[_-]?token|authorization|cookie|credential)[\w-]*["']?\s*[:=]\s*(?:Bearer\s+)?["']?)[^"'&,}\s]+)`)
)

func setDebugScheduler(value *scheduler.Scheduler) {
	debugSchedulerMu.Lock()
	debugScheduler = value
	debugSchedulerMu.Unlock()
}

var debugPprofRouteNames = []string{
	"/debug/pprof", "/debug/pprof/heap", "/debug/pprof/goroutine",
	"/debug/pprof/allocs", "/debug/pprof/block", "/debug/pprof/mutex",
	"/debug/pprof/threadcreate", "/debug/pprof/cmdline", "/debug/pprof/profile",
	"/debug/pprof/symbol", "/debug/pprof/trace",
}

var debugRuntimeRouteNames = []string{
	"/debug/vars", "/debug/config",
	"/debug/routes", "/debug/cache", "/debug/db", "/debug/scheduler",
	"/debug/memory", "/debug/goroutines", "/debug/error500",
}

func debugHandler(routeProvider func() []routeInfo) http.Handler {
	mux := http.NewServeMux()
	settings := config.Get().Server.Debug
	if settings.Pprof {
		mux.HandleFunc("/debug/pprof", func(w http.ResponseWriter, r *http.Request) {
			indexRequest := r.Clone(r.Context())
			indexRequest.URL.Path = "/debug/pprof/"
			pprof.Index(w, indexRequest)
		})
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		for _, profile := range []string{"heap", "goroutine", "allocs", "block", "mutex", "threadcreate"} {
			mux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
		}
		mux.HandleFunc("/debug/pprof/cmdline", func(w http.ResponseWriter, _ *http.Request) {
			binary := filepath.Base(os.Args[0])
			if binary == "." || binary == string(filepath.Separator) {
				binary = "gitmessages"
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(binary))
		})
	}
	if settings.RuntimeEndpoints {
		mux.HandleFunc("/debug/vars", debugVars)
		mux.HandleFunc("/debug/config", debugConfig)
		mux.HandleFunc("/debug/routes", func(w http.ResponseWriter, r *http.Request) {
			routes := makeDebugRouteList(routeProvider, settings)
			writeJSON(w, r, http.StatusOK, map[string]any{"count": len(routes), "routes": routes})
		})
		mux.HandleFunc("/debug/cache", func(w http.ResponseWriter, r *http.Request) {
			rateLimitCounterMu.RLock()
			counter := rateLimitCounter
			rateLimitCounterMu.RUnlock()
			if counter == nil {
				writeJSON(w, r, http.StatusOK, map[string]any{"available": false})
				return
			}
			writeJSON(w, r, http.StatusOK, counter.Stats())
		})
		mux.HandleFunc("/debug/db", func(w http.ResponseWriter, r *http.Request) {
			store := currentAuthStore()
			if store == nil || store.Database() == nil {
				writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"available": false})
				return
			}
			stats := store.Database().Read().Stats()
			writeJSON(w, r, http.StatusOK, map[string]any{"open_connections": stats.OpenConnections, "in_use": stats.InUse, "idle": stats.Idle, "wait_count": stats.WaitCount, "wait_duration_ms": stats.WaitDuration.Milliseconds(), "max_idle_closed": stats.MaxIdleClosed, "max_lifetime_closed": stats.MaxLifetimeClosed})
		})
		mux.HandleFunc("/debug/scheduler", func(w http.ResponseWriter, r *http.Request) {
			debugSchedulerMu.RLock()
			active := debugScheduler
			debugSchedulerMu.RUnlock()
			if active == nil {
				writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"available": false})
				return
			}
			tasks := active.GetTasks()
			status := make([]map[string]any, 0, len(tasks))
			for _, task := range tasks {
				lastError := ""
				if task.LastError != "" {
					lastError = "[REDACTED]"
				}
				status = append(status, map[string]any{
					"id": task.ID, "name": task.Name, "schedule": task.Schedule,
					"interval": task.Interval.String(), "last_run": task.LastRun,
					"next_run": task.NextRun, "last_status": task.LastStatus,
					"last_error": lastError, "run_count": task.RunCount,
					"fail_count": task.FailCount, "enabled": task.Enabled,
				})
			}
			writeJSON(w, r, http.StatusOK, status)
		})
		mux.HandleFunc("/debug/memory", func(w http.ResponseWriter, r *http.Request) {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			writeJSON(w, r, http.StatusOK, map[string]any{"alloc_mb": stats.Alloc / 1024 / 1024, "total_alloc_mb": stats.TotalAlloc / 1024 / 1024, "sys_mb": stats.Sys / 1024 / 1024, "num_gc": stats.NumGC, "heap_objects": stats.HeapObjects, "goroutines": runtime.NumGoroutine()})
		})
		mux.HandleFunc("/debug/goroutines", func(w http.ResponseWriter, _ *http.Request) {
			buf := make([]byte, 1024*1024)
			n := runtime.Stack(buf, true)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(debugCredential.ReplaceAll(buf[:n], []byte("[REDACTED]")))
		})
		mux.HandleFunc("/debug/error500", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mode.IsDebugEnabled() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		store := currentAuthStore()
		authorization := r.Header.Get("Authorization")
		if store == nil || !strings.HasPrefix(authorization, "Bearer ") {
			http.Error(w, "Operator token required", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authorization, "Bearer ")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		identity, err := store.AuthenticateBearer(ctx, token)
		if err != nil || !identity.Admin || identity.Kind != "operator" {
			http.Error(w, "Operator token required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func makeDebugRouteList(provider func() []routeInfo, settings config.DebugConfig) []routeInfo {
	routes := make([]routeInfo, 0, len(debugPprofRouteNames)+len(debugRuntimeRouteNames))
	seen := make(map[routeInfo]struct{}, cap(routes))
	if provider != nil {
		for _, route := range provider() {
			if _, exists := seen[route]; exists {
				continue
			}
			seen[route] = struct{}{}
			routes = append(routes, route)
		}
	}
	paths := make([]string, 0, len(debugPprofRouteNames)+len(debugRuntimeRouteNames))
	if settings.Pprof {
		paths = append(paths, debugPprofRouteNames...)
	}
	if settings.RuntimeEndpoints {
		paths = append(paths, debugRuntimeRouteNames...)
	}
	for _, path := range paths {
		route := routeInfo{Method: "ANY", Path: path}
		if _, exists := seen[route]; exists {
			continue
		}
		seen[route] = struct{}{}
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
	return routes
}

func debugVars(w http.ResponseWriter, r *http.Request) {
	values := map[string]json.RawMessage{}
	expvar.Do(func(value expvar.KeyValue) {
		if value.Key == "cmdline" {
			binary := filepath.Base(os.Args[0])
			encoded, _ := json.Marshal([]string{binary})
			values[value.Key] = encoded
			return
		}
		values[value.Key] = json.RawMessage(value.Value.String())
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(values)
}

func debugConfig(w http.ResponseWriter, r *http.Request) {
	var value any
	data, err := json.Marshal(config.Get())
	if err != nil || json.Unmarshal(data, &value) != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "configuration unavailable"})
		return
	}
	writeJSON(w, r, http.StatusOK, redactDebugValue(value))
}

func redactDebugValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "private") || strings.Contains(lower, "_key") || strings.Contains(lower, "apikey") || lower == "key" {
				typed[key] = "[REDACTED]"
			} else if strings.Contains(lower, "url") {
				if rawURL, ok := child.(string); ok {
					typed[key] = config.RedactConnectionURL(rawURL)
				} else {
					typed[key] = redactDebugValue(child)
				}
			} else {
				typed[key] = redactDebugValue(child)
			}
		}
	case []any:
		for index := range typed {
			typed[index] = redactDebugValue(typed[index])
		}
	}
	return value
}

func redactDebugText(value string) string {
	return debugCredential.ReplaceAllString(value, "[REDACTED]")
}
