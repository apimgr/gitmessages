package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
)

func TestPrivacyPageRendersLocalizedRetentionAndOwnerRights(t *testing.T) {
	configuration := config.Get()
	previous := configuration.Server.Privacy
	configuration.Server.Privacy = config.DefaultConfig().Server.Privacy
	t.Cleanup(func() { configuration.Server.Privacy = previous })

	request := httptest.NewRequest(http.MethodGet, "/server/privacy", nil)
	request.Header.Set("X-GitMessages-Language", "fr")
	response := httptest.NewRecorder()
	privacyPage(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK || !strings.Contains(body, i18n.T("fr", "privacy.retention.period")) {
		t.Fatalf("privacy page did not render localized built-in retention text: status=%d", response.Code)
	}
	for _, key := range []string{"privacy.export_right", "privacy.deletion_right"} {
		if !strings.Contains(body, i18n.T("fr", key)) {
			t.Errorf("privacy page omitted configured owner-data right %q", key)
		}
	}

	configuration.Server.Privacy.Retention.Period = map[string]any{"fr": "Conservation personnalisée", "default": "Custom fallback"}
	response = httptest.NewRecorder()
	privacyPage(response, request)
	if !strings.Contains(response.Body.String(), "Conservation personnalisée") {
		t.Fatal("privacy page did not resolve the requested retention language")
	}
}
