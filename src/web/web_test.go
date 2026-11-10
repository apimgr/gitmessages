package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicPagesAndAPI(t *testing.T) {
	handler := Handler("test-version")
	for _, path := range []string{"/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/healthz", "/server/privacy", "/server/terms", "/server/docs/swagger", "/server/docs/graphql", "/manifest.json", "/icon.svg", "/sw.js", "/graphql.js", "/theme.js", "/api/v1", "/api/v1/types", "/api/v1/conventions", "/api/v1/templates", "/api/v1/templates/random", "/api/v1/messages/random", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/swagger"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code == http.StatusNotFound {
			t.Errorf("GET %s returned 404", path)
		}
		if recorder.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("GET %s missing CORS header", path)
		}
	}
}

func TestBuilderEndpoint(t *testing.T) {
	handler := Handler("test-version")
	requestBody := []byte(`{"type":"feat","scope":"api","description":"add search"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewReader(requestBody)))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("feat(api): add search")) {
		t.Fatalf("valid builder response: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBufferString(`{"type":"unknown","description":"bad"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid builder status = %d", recorder.Code)
	}
}

func TestBuilderFormAndAPIMethods(t *testing.T) {
	handler := Handler("test-version")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/builder", bytes.NewBufferString("type=fix&scope=parser&description=handle+empty+input"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("fix(parser): handle empty input")) {
		t.Fatalf("builder form response: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewBufferString(`{"type":"fix","description":"ok"} {}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/types", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("types method status = %d", recorder.Code)
	}
}

func TestLanguageSelection(t *testing.T) {
	handler := Handler("test-version")
	request := httptest.NewRequest(http.MethodGet, "/?lang=ar", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`<html lang="ar" dir="rtl"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte("استكشف")) {
		t.Fatalf("Arabic page was not localized: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if cookie := recorder.Result().Cookies(); len(cookie) == 0 || cookie[0].Name != "lang" || cookie[0].Value != "ar" || !cookie[0].HttpOnly {
		t.Fatalf("language preference cookie missing or unsafe: %#v", cookie)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "fr-CA,fr;q=0.9,en;q=0.8")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`<html lang="fr" dir="ltr"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte("Explorez les types")) {
		t.Fatalf("Accept-Language was not applied: %s", recorder.Body.String())
	}
}

func TestLocalizedCatalogAPI(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/types?lang=es", nil)
	recorder := httptest.NewRecorder()
	Handler("test-version").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("Una nueva funcionalidad")) {
		t.Fatalf("catalog API was not localized: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTextRepresentations(t *testing.T) {
	handler := Handler("test-version")
	cases := []struct {
		name        string
		path        string
		userAgent   string
		accept      string
		contentType string
		contains    string
	}{
		{name: "plain text API extension", path: "/api/v1/types.txt", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "plain text API accept", path: "/api/v1/types", accept: "text/plain", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "curl browser page", path: "/types", userAgent: "curl/8.0", contentType: "text/plain; charset=utf-8", contains: "feat"},
		{name: "JSON wins for curl", path: "/api/v1/types", userAgent: "curl/8.0", accept: "application/json", contentType: "application/json; charset=utf-8", contains: `"ok": true`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.userAgent != "" {
				request.Header.Set("User-Agent", test.userAgent)
			}
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != test.contentType || !bytes.Contains(recorder.Body.Bytes(), []byte(test.contains)) {
				t.Errorf("status=%d content-type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
			}
		})
	}
}
