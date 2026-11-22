package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReleaseSelectionHelpers(t *testing.T) {
	for _, test := range []struct {
		branch string
		name   string
		want   bool
	}{
		{"stable", "v1.2.3", true},
		{"stable", "v1.2.3-beta", false},
		{"beta", "1.2.3-beta", true},
		{"beta", "devel", false},
		{"devel", "devel", true},
		{"unknown", "1.2.3", false},
	} {
		release := Release{TagName: test.name, Prerelease: strings.Contains(test.name, "beta") || test.name == "devel"}
		if got := eligible(&release, test.branch); got != test.want {
			t.Errorf("eligible(%q, %q) = %v, want %v", test.branch, test.name, got, test.want)
		}
	}
	if !isStableTag("1.2.3") || isStableTag("1.2-beta") || normalizeTag("v1.2.3") != "1.2.3" {
		t.Fatal("version tag helpers returned unexpected results")
	}
	if compareTags("1.2.4", "1.2.3") <= 0 || compareTags("1.2.3-beta", "1.2.3") >= 0 || compareTags("v1.2.3", "1.2.3") != 0 {
		t.Fatal("version ordering is incorrect")
	}
}

func TestChecksumFor(t *testing.T) {
	digest := strings.Repeat("a", sha256.Size*2)
	if got, ok := checksumFor(""+digest+" *gitmessages-linux-amd64\n", "gitmessages-linux-amd64"); !ok || got != digest {
		t.Fatalf("checksumFor() = %q, %v", got, ok)
	}
	if _, ok := checksumFor(digest+" other\n", "gitmessages-linux-amd64"); ok {
		t.Fatal("accepted checksum for a different asset")
	}
	if _, ok := checksumFor("short gitmessages-linux-amd64\n", "gitmessages-linux-amd64"); ok {
		t.Fatal("accepted malformed checksum")
	}
}

func TestDownloadBoundsAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte("too large"))
			return
		}
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	data, err := download(context.Background(), server.URL+"/ok", 2)
	if err != nil || string(data) != "ok" {
		t.Fatalf("download() = %q, %v", data, err)
	}
	if _, err := download(context.Background(), server.URL+"/large", 2); err == nil {
		t.Fatal("download accepted content over its size limit")
	}
	if _, err := download(context.Background(), server.URL+"/missing", 10); err == nil {
		t.Fatal("download accepted a non-200 response")
	}
	if _, err := download(context.Background(), "://invalid", 10); err == nil {
		t.Fatal("download accepted an invalid URL")
	}
}

func TestChecksumMatchesSHA256(t *testing.T) {
	data := []byte("binary")
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) == "" {
		t.Fatal("SHA-256 result is empty")
	}
}

func TestCheckReleaseChannelsAndFailures(t *testing.T) {
	oldAPI := releaseAPI
	t.Cleanup(func() { releaseAPI = oldAPI })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.2.3","published_at":"2026-01-01T00:00:00Z"},{"tag_name":"v1.3.0-beta","prerelease":true,"published_at":"2026-02-01T00:00:00Z"},{"tag_name":"devel","prerelease":true,"published_at":"2026-03-01T00:00:00Z"}]`))
		case "/missing":
			http.NotFound(w, r)
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	releaseAPI = server.URL + "/releases"

	stable, found, err := Check(context.Background(), "stable", "1.2.2", 0)
	if err != nil || !found || stable.TagName != "v1.2.3" {
		t.Fatalf("stable Check() = %#v, %v, %v", stable, found, err)
	}
	beta, found, err := Check(context.Background(), "beta", "1.2.2", 0)
	if err != nil || !found || beta.TagName != "v1.3.0-beta" {
		t.Fatalf("beta Check() = %#v, %v, %v", beta, found, err)
	}
	devel, found, err := Check(context.Background(), "devel", "1.2.2", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix())
	if err != nil || !found || devel.TagName != "devel" {
		t.Fatalf("devel Check() = %#v, %v, %v", devel, found, err)
	}
	releaseAPI = server.URL + "/missing"
	if _, found, err := Check(context.Background(), "stable", "1.2.2", 0); err != nil || found {
		t.Fatalf("404 Check() = found %v, err %v", found, err)
	}
	releaseAPI = server.URL + "/error"
	if _, _, err := Check(context.Background(), "stable", "1.2.2", 0); err == nil {
		t.Fatal("Check accepted a server error")
	}
	releaseAPI = server.URL + "/releases"
	if _, _, err := Check(context.Background(), "unknown", "1.2.2", 0); err != nil {
		t.Fatalf("unknown channel should return no update: %v", err)
	}
}

func TestInstallRejectsMissingAssets(t *testing.T) {
	if err := Install(context.Background(), Release{TagName: "v1.2.3"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Install without assets error = %v", err)
	}
}
