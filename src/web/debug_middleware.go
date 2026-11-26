package web

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/mode"
)

func debugMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mode.IsDebugEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		settings := config.Get().Server.Debug
		limit := debugBodyLimit(settings.MaxBodyLogSize)
		var requestBody []byte
		requestTruncated := false
		if settings.LogBodies && r.Body != nil {
			originalBody := r.Body
			replay, _ := io.ReadAll(io.LimitReader(originalBody, limit+1))
			body := replay
			requestTruncated = int64(len(body)) > limit
			if requestTruncated {
				body = body[:limit]
			}
			requestBody = body
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(replay), originalBody))
		}
		response := &debugLoggingResponseWriter{ResponseWriter: w, status: http.StatusOK, bodyLimit: limit, captureBody: settings.LogBodies}
		next.ServeHTTP(response, r)
		duration := time.Since(started)
		debugRequestCount.Add(1)
		debugRequestSeconds.Add(duration.Seconds())
		if response.status >= http.StatusInternalServerError {
			debugErrorCount.Add(1)
		}
		attributes := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", response.status,
			"duration_ms", duration.Milliseconds(),
			"size", response.size,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
			"request_id", r.Header.Get("X-Request-ID"),
		}
		if settings.LogBodies {
			attributes = append(attributes,
				"request_body", redactDebugText(string(requestBody)),
				"request_body_truncated", requestTruncated,
				"response_body", redactDebugText(string(response.body)),
				"response_body_truncated", response.truncated,
			)
		}
		slog.Debug("request", attributes...)
	})
}

type debugLoggingResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	size        int
	bodyLimit   int64
	body        []byte
	truncated   bool
	captureBody bool
}

func (w *debugLoggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *debugLoggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	w.wroteHeader = true
}

func (w *debugLoggingResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(body)
	w.size += written
	if w.captureBody && written > 0 {
		remaining := int(w.bodyLimit) - len(w.body)
		if remaining > 0 {
			captured := written
			if captured > remaining {
				captured = remaining
			}
			w.body = append(w.body, body[:captured]...)
		}
		if written > remaining {
			w.truncated = true
		}
	}
	return written, err
}

func (w *debugLoggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *debugLoggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *debugLoggingResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func debugBodyLimit(value string) int64 {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 10 * 1024
	}
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		size   int64
	}{{"KIB", 1024}, {"MIB", 1024 * 1024}, {"KB", 1024}, {"MB", 1024 * 1024}, {"B", 1}} {
		if strings.HasSuffix(value, unit.suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			multiplier = unit.size
			break
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount < 1 || amount > (10*1024*1024)/multiplier {
		return 10 * 1024
	}
	return amount * multiplier
}
