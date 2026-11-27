package banner

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPrintServerStartupBannerResponsiveModes(t *testing.T) {
	urls := []string{"https://example.test:8443", "http://example.test:8080"}
	startedAt := time.Date(2026, time.November, 26, 21, 0, 0, 0, time.UTC)
	startedLabel := "Server started on November 26, 2026 at 16:00:00 EST"
	tests := []struct {
		name       string
		width      int
		plain      bool
		want       []string
		unexpected []string
	}{
		{name: "full", width: 80, want: []string{"🚀 Git Messages v1.2.3", "🌐 Listening on: https://example.test:8443", "✅ " + startedLabel, " ____"}},
		{name: "compact", width: 60, want: []string{"🚀 Git Messages v1.2.3", "🌐 Listening on: http://example.test:8080", "✅ " + startedLabel}, unexpected: []string{" ____"}},
		{name: "minimal", width: 40, want: []string{"Git Messages 1.2.3", "example.test:8443"}, unexpected: []string{"🚀", "https://"}},
		{name: "micro", width: 39, want: []string{"Git Messages example.test:8443"}, unexpected: []string{"🚀", "http://"}},
		{name: "plain", width: 100, plain: true, want: []string{"Git Messages v1.2.3", "Running in mode: production", "Listening on: https://example.test:8443", startedLabel}, unexpected: []string{"🚀", "🌐", " ____"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			info := ServerBannerInfo{AppName: "Git Messages", Version: "1.2.3", AppMode: "production", URLs: urls, Lang: "en"}
			printServerStartupBanner(&output, info, FormatStartupDatetime(startedAt, "America/New_York"), test.width, false, !test.plain)
			got := output.String()
			for _, value := range test.want {
				if !strings.Contains(got, value) {
					t.Errorf("banner does not contain %q:\n%s", value, got)
				}
			}
			for _, value := range test.unexpected {
				if strings.Contains(got, value) {
					t.Errorf("banner unexpectedly contains %q:\n%s", value, got)
				}
			}
		})
	}
}

func TestPrintServerStartupBannerIncludesConfiguredServices(t *testing.T) {
	var output bytes.Buffer
	info := ServerBannerInfo{
		AppName: "Git Messages", Version: "1.2.3", AppMode: "production", Lang: "en",
		URLs: []string{"https://example.test"}, OnionAddress: "example.onion",
		I2PAddress: "example.i2p", SMTPAddress: "smtp.example.test:587", SetupToken: "one-time-token",
	}
	printServerStartupBanner(&output, info, "November 26, 2026 at 16:00:00 EST", 100, false, true)
	for _, want := range []string{"Tor: example.onion", "I2P: example.i2p", "SMTP: smtp.example.test:587", "Setup token: one-time-token"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("banner does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestMinimalServerBannerOmitsIconsForConfiguredServices(t *testing.T) {
	var output bytes.Buffer
	info := ServerBannerInfo{
		AppName: "Git Messages", Version: "1.2.3", AppMode: "production", Lang: "en",
		URLs: []string{"https://example.test"}, OnionAddress: "http://example.onion",
	}
	printServerStartupBanner(&output, info, "", 45, false, true)
	if strings.Contains(output.String(), "🧅") || !strings.Contains(output.String(), "Tor: http://example.onion") {
		t.Fatalf("minimal banner should show plain service labels without icons:\n%s", output.String())
	}
}

func TestFormatStartupDatetimeUsesConfiguredAndDefaultTimezone(t *testing.T) {
	startedAt := time.Date(2026, time.November, 26, 21, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		timezone string
		want     string
	}{
		{name: "configured timezone", timezone: "Europe/Paris", want: "November 26, 2026 at 22:00:00 CET"},
		{name: "default timezone", want: "November 26, 2026 at 16:00:00 EST"},
		{name: "invalid timezone fallback", timezone: "Invalid/Zone", want: "November 26, 2026 at 16:00:00 EST"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatStartupDatetime(startedAt, test.timezone); got != test.want {
				t.Fatalf("FormatStartupDatetime() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtractHostPort(t *testing.T) {
	if got := extractHostPort("https://[2001:db8::1]:8443/path"); got != "[2001:db8::1]:8443" {
		t.Fatalf("extractHostPort() = %q, want IPv6 host and port", got)
	}
	if got := extractHostPort("https://example.test/path"); got != "example.test" {
		t.Fatalf("extractHostPort() = %q, want hostname", got)
	}
}
