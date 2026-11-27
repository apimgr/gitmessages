package main

import "testing"

func TestStartupPublicURLsUseResolvedHostnameAndEveryListener(t *testing.T) {
	got := startupPublicURLs("example.test", "/app", []serverListener{{port: 8080}, {port: 443, tls: true}})
	want := []string{"http://example.test:8080/app", "https://example.test/app"}
	if len(got) != len(want) {
		t.Fatalf("startupPublicURLs() = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("startupPublicURLs()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
	if got := startupPublicURLs("", "", []serverListener{{port: 8080}}); len(got) != 0 {
		t.Fatalf("startupPublicURLs() without a resolved hostname = %q, want no URLs", got)
	}
}

func TestStartupOverlayURLAlwaysUsesHTTP(t *testing.T) {
	if got := startupOverlayURL("abc.onion", "/app"); got != "http://abc.onion/app" {
		t.Fatalf("startupOverlayURL() = %q, want http://abc.onion/app", got)
	}
}

func TestStartupPublicURLsBracketIPv6(t *testing.T) {
	got := startupPublicURLs("2001:db8::1", "", []serverListener{{port: 80}, {port: 8443, tls: true}})
	want := []string{"http://[2001:db8::1]", "https://[2001:db8::1]:8443"}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("startupPublicURLs()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}
