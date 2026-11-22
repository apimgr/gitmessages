package overlay

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestTorConfigurationAndMissingBinary(t *testing.T) {
	settings := config.DefaultConfig().Server.Tor
	settings.UseNetwork = true
	manager := NewTorManager(t.TempDir(), t.TempDir(), t.TempDir(), settings, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	manager.listener = listener
	configuration := manager.torrc("/usr/bin/tor", "/tmp/control-port")
	for _, required := range []string{"ControlPort 127.0.0.1:auto", "SocksPort 127.0.0.1:auto", "HiddenServiceVersion 3", "SafeLogging 1", "ExitRelay 0", "ExitPolicy reject *:*"} {
		if !strings.Contains(configuration, required) {
			t.Errorf("torrc missing %q", required)
		}
	}
	if !strings.Contains(configuration, "HiddenServicePort 80 127.0.0.1:") {
		t.Errorf("torrc does not map to the dedicated backend: %s", configuration)
	}

	manager.config.Binary = "/missing/tor"
	if err := manager.Start(); err == nil {
		t.Fatal("Start accepted a missing explicitly configured Tor binary")
	}
	if status := manager.Status(); status.Status != "disabled" || status.Running {
		t.Fatalf("missing Tor status = %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTorManagerTracksHiddenServiceRequests(t *testing.T) {
	manager := NewTorManager(t.TempDir(), t.TempDir(), t.TempDir(), config.DefaultConfig().Server.Tor, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	manager.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/types", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("Tor backend response status = %d, want %d", response.Code, http.StatusNoContent)
	}
	status := manager.Status()
	if status.RequestsTotal != 1 {
		t.Fatalf("Tor requests total = %d, want 1", status.RequestsTotal)
	}
	if status.CircuitEstablished {
		t.Fatal("circuit status is true before Tor has bootstrapped")
	}
}

func TestTorControlFileAndConfigurationPaths(t *testing.T) {
	directory := t.TempDir()
	cookie := filepath.Join(directory, "auth cookie")
	path := filepath.Join(directory, "control-port")
	contents := "PORT=127.0.0.1:19001\nCOOKIEFILE=\"" + cookie + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	address, gotCookie, err := waitTorControlFile(context.Background(), path, time.Second)
	if err != nil || address != "127.0.0.1:19001" || gotCookie != cookie {
		t.Fatalf("control address=%q cookie=%q err=%v", address, gotCookie, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := waitTorControlFile(ctx, filepath.Join(directory, "missing"), time.Second); err == nil {
		t.Fatal("cancelled control-file wait succeeded")
	}
	if got := torrcPath(`path with "quote`); got != `"path with \"quote"` {
		t.Fatalf("escaped Tor path = %q", got)
	}
	manager := NewTorManager(directory, directory, directory, config.TorConfig{SafeLogging: false}, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	manager.listener = listener
	configuration := manager.torrc("tor", filepath.Join(directory, "control"))
	if !strings.Contains(configuration, "SocksPort 0") || !strings.Contains(configuration, "SafeLogging 0") {
		t.Fatalf("disabled outbound Tor settings missing:\n%s", configuration)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTorFailureAndBinaryResolution(t *testing.T) {
	manager := NewTorManager(t.TempDir(), t.TempDir(), t.TempDir(), config.TorConfig{Binary: os.Args[0]}, nil)
	if binary, err := manager.resolveBinary(); err != nil || binary == "" {
		t.Fatalf("current executable did not resolve as a binary: %q %v", binary, err)
	}
	manager.fail(os.ErrNotExist)
	status := manager.Status()
	if status.Status != "unavailable" || status.Error == "" {
		t.Fatalf("Tor failure status = %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}
