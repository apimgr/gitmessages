package web

import (
	"encoding/base64"
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
