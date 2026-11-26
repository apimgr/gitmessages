package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverMiddlewareRendersContentNegotiatedFallback(t *testing.T) {
	for _, test := range []struct {
		name        string
		accept      string
		contentType string
		contains    string
	}{{"html", "text/html", "text/html; charset=utf-8", "<h1>500 Internal Server Error</h1>"}, {"json", "application/json", "application/json; charset=utf-8", `"error":"SERVER_ERROR"`}} {
		t.Run(test.name, func(t *testing.T) {
			handler := RecoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private panic detail") }))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", test.accept)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("content type = %q, want %q", got, test.contentType)
			}
			if !strings.Contains(response.Body.String(), test.contains) || strings.Contains(response.Body.String(), "private panic detail") {
				t.Fatalf("unexpected fallback body: %s", response.Body.String())
			}
		})
	}
}
