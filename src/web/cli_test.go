package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
)

func TestCLIBinaryDownloadAndDiscoveryChecksum(t *testing.T) {
	tmp := projectTestDir(t)
	configuration, err := config.Load(filepath.Join(tmp, "server.yml"))
	if err != nil {
		t.Fatal(err)
	}
	resetPath := filepath.Join(projectTestDir(t), "server.yml")
	t.Cleanup(func() {
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})
	binaryDir := filepath.Join(tmp, "cli")
	configuration.CLI.BinaryDownload.Dir = binaryDir
	if err := os.MkdirAll(binaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := paths.ProjectName + "-cli-linux-amd64"
	contents := []byte("binary-release-content")
	if err := os.WriteFile(filepath.Join(binaryDir, name), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if err := os.WriteFile(filepath.Join(binaryDir, "version.txt"), []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binaryDir, "sha256.txt"), []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), name)), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := Handler("1.2.4")
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, webTestRequest(http.MethodGet, "/cli/binaries/"+name, nil))
	if download.Code != http.StatusOK || download.Body.String() != string(contents) {
		t.Fatalf("binary response = %d %q", download.Code, download.Body.String())
	}
	discovery := httptest.NewRecorder()
	handler.ServeHTTP(discovery, webTestRequest(http.MethodGet, "/api/autodiscover", nil))
	if discovery.Code != http.StatusOK || !strings.Contains(discovery.Body.String(), `"linux-amd64"`) {
		t.Fatalf("discovery omitted verified CLI version: %s", discovery.Body.String())
	}
	if err := os.WriteFile(filepath.Join(binaryDir, name), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, webTestRequest(http.MethodGet, "/api/autodiscover", nil))
	if strings.Contains(invalid.Body.String(), "linux-amd64") {
		t.Fatalf("discovery advertised a binary with an invalid checksum: %s", invalid.Body.String())
	}
}
