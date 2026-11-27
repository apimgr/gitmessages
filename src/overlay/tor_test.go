package overlay

import (
	"context"
	"errors"
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
	manager := NewTorManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), settings, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	manager.listener = listener
	configuration := manager.torrc("/usr/bin/tor", "/tmp/control-port")
	for _, required := range []string{"ControlPort 127.0.0.1:auto", "SocksPort 127.0.0.1:auto", "DisableNetwork 1", "HiddenServiceVersion 3", "SafeLogging 1", "ExitRelay 0", "ExitPolicy reject *:*"} {
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
	manager := NewTorManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), config.DefaultConfig().Server.Tor, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	directory := projectTestDir(t)
	path := filepath.Join(directory, "control-port")
	contents := "PORT=127.0.0.1:19001\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	address, err := waitTorControlFile(context.Background(), path, time.Second, nil)
	if err != nil || address != "127.0.0.1:19001" {
		t.Fatalf("control address=%q err=%v", address, err)
	}
	if err := os.WriteFile(path, []byte(contents+"COOKIEFILE=/tmp/cookie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := waitTorControlFile(context.Background(), path, 10*time.Millisecond, nil); err == nil {
		t.Fatal("control file with extra content was accepted")
	}
	processDone := make(chan error, 1)
	processDone <- errors.New("exit status 1")
	if _, err := waitTorControlFile(context.Background(), filepath.Join(directory, "missing"), time.Second, processDone); err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("exited Tor process wait error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitTorControlFile(ctx, filepath.Join(directory, "missing"), time.Second, nil); err == nil {
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
	manager := NewTorManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), config.TorConfig{Binary: os.Args[0]}, nil)
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
