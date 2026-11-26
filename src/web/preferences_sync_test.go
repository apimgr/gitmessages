package web

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMergePreferenceValuesAcceptsCodeFormatsAndDirectOverrides(t *testing.T) {
	query := "lang=fr&theme=light"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(query))
	for _, code := range []string{encoded, base64.URLEncoding.EncodeToString([]byte(query)), query, "https://example.test/server/preferences/sync/import?" + query} {
		got := mergePreferenceValues(url.Values{"theme": {"dark"}}, code)
		if got.Get("lang") != "fr" || got.Get("theme") != "dark" {
			t.Errorf("mergePreferenceValues(%q) = %v", code, got)
		}
	}
}

func TestValidPreferenceInputsIgnoresControlsAndRejectsOtherValues(t *testing.T) {
	input := url.Values{"theme": {"dark"}, "lang": {"fr-CA"}, "cookie_consent": {"forged"}, "code": {"not-a-preference"}, "session": {"credential"}}
	valid, applied, ignored := validPreferenceInputs(input)
	if valid.Get("theme") != "dark" || valid.Get("lang") != "fr" {
		t.Fatalf("validated values = %v", valid)
	}
	if len(applied) != 2 || applied[0] != "lang" || applied[1] != "theme" {
		t.Fatalf("applied = %v", applied)
	}
	if len(ignored) != 2 || ignored[0] != "cookie_consent" || ignored[1] != "session" {
		t.Fatalf("ignored = %v", ignored)
	}
}

func TestMergePreferenceValuesDiscardsOversizedCode(t *testing.T) {
	input := url.Values{"theme": {"light"}}
	got := mergePreferenceValues(input, strings.Repeat("A", 2049))
	if got.Get("theme") != "light" || got.Get("lang") != "" {
		t.Fatalf("oversized code changed direct input: %v", got)
	}
}

func TestPreferenceSyncRequiresLocalConsent(t *testing.T) {
	handler := Handler("test-version")
	webResponse := httptest.NewRecorder()
	handler.ServeHTTP(webResponse, webTestRequest(http.MethodGet, "/server/preferences/sync/import?theme=light&lang=fr", nil))
	if webResponse.Code != http.StatusSeeOther || !strings.Contains(webResponse.Header().Get("Location"), "consent_required=1") {
		t.Fatalf("web import without consent response = %d %q", webResponse.Code, webResponse.Header().Get("Location"))
	}
	for _, cookie := range webResponse.Result().Cookies() {
		if cookie.Name == "theme" || cookie.Name == "lang" {
			t.Fatalf("web import saved a preference without consent: %#v", cookie)
		}
	}

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, webTestRequest(http.MethodGet, "/api/v1/server/preferences/sync/import?theme=light&lang=fr", nil))
	for _, expected := range []string{`"consent_required": true`, `"applied": []`, `"ignored": [`} {
		if apiResponse.Code != http.StatusOK || !strings.Contains(apiResponse.Body.String(), expected) {
			t.Errorf("API import without consent omitted %q: status=%d body=%s", expected, apiResponse.Code, apiResponse.Body.String())
		}
	}
	for _, cookie := range apiResponse.Result().Cookies() {
		if cookie.Name == "theme" || cookie.Name == "lang" {
			t.Fatalf("API import saved a preference without consent: %#v", cookie)
		}
	}
}
