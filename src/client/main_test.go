package main

import (
	"testing"
)

func TestValidServer(t *testing.T) {
	for _, input := range []struct {
		value string
		valid bool
	}{
		{"https://api.example.com", true},
		{"http://127.0.0.1:8080/base", true},
		{"ftp://api.example.com", false},
		{"https://user:password@api.example.com", false},
		{"https://api.example.com?token=value", false},
		{"https://api.example.com#fragment", false},
		{"https://api.example.com path", false},
	} {
		if got := validServer(input.value); got != input.valid {
			t.Errorf("validServer(%q) = %t, want %t", input.value, got, input.valid)
		}
	}
}

func TestSelectLanguage(t *testing.T) {
	t.Setenv("GITMESSAGES_LANG", "fr-CA")
	if got := selectLanguage("", "de"); got != "fr" {
		t.Fatalf("environment language priority = %q, want fr", got)
	}
	if got := selectLanguage("ja", "de"); got != "ja" {
		t.Fatalf("flag language priority = %q, want ja", got)
	}
	t.Setenv("GITMESSAGES_LANG", "")
	if got := selectLanguage("unsupported", "unsupported"); got != "en" {
		t.Fatalf("unsupported language fallback = %q, want en", got)
	}
}
