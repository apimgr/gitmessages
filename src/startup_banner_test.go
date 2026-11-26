package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/common/display"
)

func TestWriteStartupBannerFitsTerminalWidths(t *testing.T) {
	for _, width := range []int{100, 70, 50, 30} {
		t.Run(string(rune('0'+width/10)), func(t *testing.T) {
			var output bytes.Buffer
			if err := writeStartupBanner(&output, display.DisplayEnv{Cols: width}, "auto", "1.2.3", "abc1234", "2026-01-01T00:00:00Z", "production", "https://example.test"); err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
				if len(line) > width {
					t.Errorf("banner line has %d columns at width %d: %q", len(line), width, line)
				}
			}
		})
	}
}

func TestWriteStartupBannerDefaultsWidthWhenUnknown(t *testing.T) {
	var output bytes.Buffer
	if err := writeStartupBanner(&output, display.DisplayEnv{}, "auto", "1.2.3", "abc1234", "2026-01-01T00:00:00Z", "production", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Git Messages 1.2.3") {
		t.Fatalf("banner output = %q", output.String())
	}
}

func TestStartupPublicURLUsesConfiguredTLSAndPort(t *testing.T) {
	if got := startupPublicURL("example.test", "/app", []serverListener{{port: 8080}, {port: 443, tls: true}}); got != "https://example.test/app" {
		t.Fatalf("startupPublicURL() = %q, want https://example.test/app", got)
	}
	if got := startupPublicURL("example.test", "", []serverListener{{port: 8443, tls: true}}); got != "https://example.test:8443" {
		t.Fatalf("startupPublicURL() = %q, want https://example.test:8443", got)
	}
	if got := startupPublicURL("", "", []serverListener{{port: 8080}}); got != "" {
		t.Fatalf("startupPublicURL() without a configured FQDN = %q, want empty", got)
	}
}
