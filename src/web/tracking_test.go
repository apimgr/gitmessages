package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func TestTrackingRequiresConsentAndUsesSameOriginScript(t *testing.T) {
	cfg := config.Get()
	previousTracking, previousOnion, previousHonorDNT := cfg.Server.Tracking, cfg.Server.Tor.OnionAddress, cfg.Web.Headers.HonorDNT
	cfg.Server.Tracking = config.TrackingConfig{Type: "google", ID: "G-ABC123"}
	cfg.Server.Tor.OnionAddress = ""
	cfg.Web.Headers.HonorDNT = true
	t.Cleanup(func() {
		cfg.Server.Tracking = previousTracking
		cfg.Server.Tor.OnionAddress = previousOnion
		cfg.Web.Headers.HonorDNT = previousHonorDNT
	})
	handler := Handler("test-version")

	withoutConsent := httptest.NewRecorder()
	handler.ServeHTTP(withoutConsent, webTestRequest(http.MethodGet, "/server/tracking.js", nil))
	if withoutConsent.Code != http.StatusNotFound {
		t.Fatalf("tracking endpoint without consent = %d, want 404", withoutConsent.Code)
	}

	request := webTestRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"analytics":true,"timestamp":1}`)})
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, request)
	for _, expected := range []string{"https://www.googletagmanager.com/gtag/js?id=G-ABC123", `src="/server/tracking.js"`, "https://www.googletagmanager.com", "https://www.google-analytics.com"} {
		if !strings.Contains(page.Body.String()+page.Header().Get("Content-Security-Policy"), expected) {
			t.Errorf("consented tracking page omitted %q", expected)
		}
	}

	scriptRequest := webTestRequest(http.MethodGet, "/server/tracking.js", nil)
	scriptRequest.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"analytics":true,"timestamp":1}`)})
	script := httptest.NewRecorder()
	handler.ServeHTTP(script, scriptRequest)
	if script.Code != http.StatusOK || script.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(script.Body.String(), "gtag('config'") {
		t.Fatalf("tracking script response = %d %q %s", script.Code, script.Header().Get("Cache-Control"), script.Body.String())
	}

	for _, signal := range []struct{ header, value string }{{"Sec-GPC", "1"}, {"DNT", "1"}} {
		signalRequest := webTestRequest(http.MethodGet, "/server/tracking.js", nil)
		signalRequest.Header.Set(signal.header, signal.value)
		signalRequest.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"analytics":true,"timestamp":1}`)})
		signalResponse := httptest.NewRecorder()
		handler.ServeHTTP(signalResponse, signalRequest)
		if signalResponse.Code != http.StatusNotFound {
			t.Errorf("%s did not disable tracking: %d", signal.header, signalResponse.Code)
		}
	}

	cfg.Server.Tor.OnionAddress = "gitmessagesexample.onion"
	onionRequest := webTestRequest(http.MethodGet, "/server/tracking.js", nil)
	onionRequest.Host = cfg.Server.Tor.OnionAddress
	onionRequest.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"analytics":true,"timestamp":1}`)})
	onion := httptest.NewRecorder()
	handler.ServeHTTP(onion, onionRequest)
	if onion.Code != http.StatusNotFound || strings.Contains(onion.Header().Get("Content-Security-Policy"), "googletagmanager") {
		t.Fatalf("overlay tracking response = %d CSP=%q", onion.Code, onion.Header().Get("Content-Security-Policy"))
	}
}
