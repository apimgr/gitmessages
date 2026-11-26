package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func TestConsentBannerAndServerSideChoices(t *testing.T) {
	cfg := config.Get()
	previous := cfg.Server.Privacy.Consent
	cfg.Server.Privacy.Consent = config.DefaultConfig().Server.Privacy.Consent
	t.Cleanup(func() { cfg.Server.Privacy.Consent = previous })
	handler := Handler("test-version")

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, webTestRequest(http.MethodGet, "/server/help", nil))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `id="cookie-consent"`) || !strings.Contains(first.Body.String(), `action="/server/consent"`) {
		t.Fatalf("first-visit consent banner missing: %d %s", first.Code, first.Body.String())
	}
	acceptedCookie := &http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true,"analytics":true,"timestamp":1}`)}
	withConsent := webTestRequest(http.MethodGet, "/server/help", nil)
	withConsent.AddCookie(acceptedCookie)
	acceptedPage := httptest.NewRecorder()
	handler.ServeHTTP(acceptedPage, withConsent)
	if strings.Contains(acceptedPage.Body.String(), `id="cookie-consent"`) {
		t.Fatal("valid consent cookie did not hide the banner")
	}

	for _, choice := range []string{"accept", "decline"} {
		form := url.Values{"csrf_token": {"csrf-consent"}, "choice": {choice}, "redirect": {"/server/help?from=consent"}}
		request := webTestRequest(http.MethodPost, "/server/consent", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-consent"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/server/help?from=consent" {
			t.Fatalf("consent %s response = %d %q", choice, response.Code, response.Header().Get("Location"))
		}
		var state cookieConsentState
		var cookie *http.Cookie
		for _, candidate := range response.Result().Cookies() {
			if candidate.Name == "cookie_consent" {
				cookie = candidate
			}
		}
		if cookie == nil {
			t.Fatalf("consent %s did not write the consent cookie", choice)
		}
		decoded, err := url.PathUnescape(cookie.Value)
		if err != nil || json.Unmarshal([]byte(decoded), &state) != nil || !state.Essential || state.Timestamp == 0 {
			t.Fatalf("consent %s cookie is invalid: %q", choice, cookie.Value)
		}
		if (choice == "accept") != state.Analytics || state.Preferences != state.Analytics {
			t.Errorf("consent %s categories are inconsistent: %+v", choice, state)
		}
	}
}
