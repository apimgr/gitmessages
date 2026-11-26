package web

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
)

// RecoverMiddleware converts handler panics into a bounded internal error response.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				panicLoggerMu.RLock()
				logger := panicLogger
				panicLoggerMu.RUnlock()
				if logger != nil {
					logger.Printf("panic recovered request_id=%s\n%s", w.Header().Get("X-Request-ID"), debug.Stack())
				}
				renderFallbackError(w, r, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func renderFallbackError(w http.ResponseWriter, r *http.Request, status int) {
	if strings.Contains(r.Header.Get("Accept"), "json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "{\"ok\":false,\"error\":\"SERVER_ERROR\",\"message\":%q,\"details\":{}}\n", http.StatusText(status))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>%d %s</title></head><body><main><h1>%d %s</h1><p><a href=\"/\">Home</a></p></main></body></html>", status, http.StatusText(status), status, http.StatusText(status))
}
