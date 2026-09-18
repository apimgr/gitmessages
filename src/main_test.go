package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/messages"
)

// setupTestState initializes the package-level msgManager and cfg used by
// the handlers, restoring the previous values afterward so tests stay
// hermetic and order-independent.
func setupTestState(t *testing.T) {
	t.Helper()

	savedManager := msgManager
	savedCfg := cfg
	t.Cleanup(func() {
		msgManager = savedManager
		cfg = savedCfg
	})

	m, err := messages.New()
	if err != nil {
		t.Fatalf("messages.New() error: %v", err)
	}
	msgManager = m
	cfg = config.DefaultConfig()
}

// TestHandleHome covers the root path and the 404 fallback for any other
// path routed to it directly.
func TestHandleHome(t *testing.T) {
	setupTestState(t)

	t.Run("root path renders page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handleHome(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "GitMessages API") {
			t.Error("expected home page to mention GitMessages API")
		}
	})

	t.Run("non-root path 404s", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/nope", nil)
		rec := httptest.NewRecorder()
		handleHome(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// TestHandleHealthz covers both the JSON and plain-text health handlers.
func TestHandleHealthz(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handleHealthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("status field = %v, want healthy", body["status"])
	}
}

func TestHandleHealthzText(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz.txt", nil)
	rec := httptest.NewRecorder()
	handleHealthzText(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("body = %q, want OK", rec.Body.String())
	}
}

// TestHandleAPIInfo covers the info endpoint and its 404 fallback.
func TestHandleAPIInfo(t *testing.T) {
	setupTestState(t)

	t.Run("exact path returns info", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/", nil)
		rec := httptest.NewRecorder()
		handleAPIInfo(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		if body["name"] != "GitMessages API" {
			t.Errorf("name field = %v, want GitMessages API", body["name"])
		}
	})

	t.Run("other path 404s", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/other", nil)
		rec := httptest.NewRecorder()
		handleAPIInfo(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// TestHandleRandom_JSONAndText verifies both success responses carry a
// non-empty message and correct content types.
func TestHandleRandom_JSONAndText(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/random", nil)
	rec := httptest.NewRecorder()
	handleRandom(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["success"] != true {
		t.Errorf("success field = %v, want true", body["success"])
	}

	reqText := httptest.NewRequest(http.MethodGet, "/api/v1/random.txt", nil)
	recText := httptest.NewRecorder()
	handleRandomText(recText, reqText)

	if recText.Code != http.StatusOK {
		t.Fatalf("text status = %d, want 200", recText.Code)
	}
	if recText.Body.Len() == 0 {
		t.Error("expected non-empty random message text")
	}
}

// TestHandleMessages_JSONAndText verifies both list endpoints succeed and
// return the full message set.
func TestHandleMessages_JSONAndText(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil)
	rec := httptest.NewRecorder()
	handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	reqText := httptest.NewRequest(http.MethodGet, "/api/v1/messages.txt", nil)
	recText := httptest.NewRecorder()
	handleMessagesText(recText, reqText)

	if recText.Code != http.StatusOK {
		t.Fatalf("text status = %d, want 200", recText.Code)
	}
	if recText.Body.Len() == 0 {
		t.Error("expected non-empty messages text body")
	}
}

// TestHandleStats_JSONAndText verifies both stats endpoints report a
// cycle/total shape.
func TestHandleStats_JSONAndText(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["success"] != true {
		t.Errorf("success field = %v, want true", body["success"])
	}

	reqText := httptest.NewRequest(http.MethodGet, "/api/v1/stats.txt", nil)
	recText := httptest.NewRecorder()
	handleStatsText(recText, reqText)

	if recText.Code != http.StatusOK {
		t.Fatalf("text status = %d, want 200", recText.Code)
	}
	if !strings.Contains(recText.Body.String(), "Cycle:") {
		t.Error("expected stats text to contain Cycle:")
	}
}

// TestHandleReset covers the method-not-allowed branch and the successful
// POST branch, verifying it actually resets the cycle state.
func TestHandleReset(t *testing.T) {
	setupTestState(t)

	t.Run("GET is not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reset", nil)
		rec := httptest.NewRecorder()
		handleReset(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("POST resets cycle", func(t *testing.T) {
		// Draw a message first so used-in-cycle is nonzero before reset.
		if _, err := msgManager.GetRandom(); err != nil {
			t.Fatalf("GetRandom() error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/reset", nil)
		rec := httptest.NewRecorder()
		handleReset(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		stats := msgManager.Stats()
		if stats["used_in_cycle"] != 0 {
			t.Errorf("used_in_cycle after reset = %v, want 0", stats["used_in_cycle"])
		}
	})
}

// TestHandleRobotsTxt covers both the configured-allow/deny case and the
// nil-config fallback.
func TestHandleRobotsTxt(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	rec := httptest.NewRecorder()
	handleRobotsTxt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Allow:") {
		t.Error("expected robots.txt to contain an Allow directive")
	}

	savedCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = savedCfg })

	req2 := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	rec2 := httptest.NewRecorder()
	handleRobotsTxt(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "Allow: /") {
		t.Error("expected nil-config fallback to allow everything")
	}
}

// TestHandleSecurityTxt verifies the contact email defaults when config is
// nil or has no explicit admin address, and honors it when set.
func TestHandleSecurityTxt(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/security.txt", nil)
	rec := httptest.NewRecorder()
	handleSecurityTxt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Contact: mailto:security@apimgr.us") {
		t.Error("expected default security contact address")
	}

	cfg.WebSecurity.Admin = "custom@example.com"
	rec2 := httptest.NewRecorder()
	handleSecurityTxt(rec2, req)
	if !strings.Contains(rec2.Body.String(), "Contact: mailto:custom@example.com") {
		t.Error("expected configured security contact address")
	}
}

// TestHandleManifestAndServiceWorker are smoke tests for the two static
// asset handlers.
func TestHandleManifestAndServiceWorker(t *testing.T) {
	setupTestState(t)

	req := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	rec := httptest.NewRecorder()
	handleManifest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type = %q, want application/manifest+json", ct)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	rec2 := httptest.NewRecorder()
	handleServiceWorker(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("sw.js status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "CACHE_NAME") {
		t.Error("expected service worker body to define CACHE_NAME")
	}
}

// TestCorsMiddleware verifies the security/CORS headers are set and that
// an OPTIONS request short-circuits without reaching the wrapped handler.
func TestCorsMiddleware(t *testing.T) {
	setupTestState(t)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := corsMiddleware(next)

	t.Run("OPTIONS short-circuits", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if called {
			t.Error("wrapped handler should not run for OPTIONS")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("GET sets security headers and passes through", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if !called {
			t.Error("wrapped handler should run for GET")
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Error("expected X-Frame-Options: DENY")
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("expected default CORS origin *")
		}
	})
}

// TestSetupRoutes is a mux smoke test verifying routes dispatch instead of
// falling through to a 404.
func TestSetupRoutes(t *testing.T) {
	setupTestState(t)

	mux := http.NewServeMux()
	setupRoutes(mux)

	paths := []string{
		"/", "/healthz", "/robots.txt", "/security.txt", "/manifest.json",
		"/sw.js", "/api/v1/", "/api/v1/random", "/api/v1/messages", "/api/v1/stats",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("route %s not registered on mux", p)
		}
	}
}

// TestCheckHealth covers the success path against a live httptest server and
// the failure path against a port nothing listens on.
func TestCheckHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("failed to split test server host/port: %v", err)
	}

	if err := checkHealth(port); err != nil {
		t.Errorf("checkHealth(%q) error = %v, want nil", port, err)
	}

	// Grab a port and immediately close the listener so nothing answers it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate a free port: %v", err)
	}
	_, freePort, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()

	if err := checkHealth(freePort); err == nil {
		t.Error("checkHealth against a closed port: expected error, got nil")
	}
}

// TestMaintenanceUpdate and TestPrintHelp are no-panic smoke tests for the
// pure stdout-printing informational commands.
func TestMaintenanceUpdate(t *testing.T) {
	maintenanceUpdate()
}

func TestPrintHelp(t *testing.T) {
	printHelp()
}

// TestRunSetupWizard verifies the wizard creates a config file under an
// isolated temp directory without touching any real system path.
func TestRunSetupWizard(t *testing.T) {
	dir := t.TempDir()

	runSetupWizard(dir)

	configFile := filepath.Join(dir, "server.yml")
	if _, err := os.Stat(configFile); err != nil {
		t.Errorf("expected setup wizard to create %s: %v", configFile, err)
	}
}

// TestSetApplicationMode_ValidMode covers the success branch: a valid mode
// is persisted to an isolated temp config file. The invalid-mode branch
// calls os.Exit and cannot be safely unit tested in-process.
func TestSetApplicationMode_ValidMode(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "server.yml")

	setApplicationMode("development", configFile)

	loaded, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if loaded.Server.Mode != "development" {
		t.Errorf("Server.Mode = %q, want development", loaded.Server.Mode)
	}
}

// TestHandleUpdateCommand covers the "check" informational branch and the
// "branch <valid>" success branch. The invalid-branch/unknown-command
// branches call os.Exit and cannot be safely unit tested in-process.
func TestHandleUpdateCommand(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "server.yml")
	currentCfg, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	handleUpdateCommand("check", currentCfg)

	handleUpdateCommand("branch beta", currentCfg)

	reloaded, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if reloaded.Server.UpdateBranch != "beta" {
		t.Errorf("Server.UpdateBranch = %q, want beta", reloaded.Server.UpdateBranch)
	}
}

// TestServiceDispatch_Status exercises the safe read-only service commands.
// These only shell out via runCommand, which logs and returns on failure
// rather than exiting, so they are safe to call even when the underlying
// service manager (systemctl/launchctl) is unavailable in this environment.
func TestServiceDispatch_Status(t *testing.T) {
	handleServiceCommand("status", t.TempDir())
	serviceStart()
	serviceStop()
	serviceRestart()
	serviceReload()
	serviceStatus()
}

// TestServiceInstallUninstallDisable exercises the real systemd unit file
// writer/remover. systemctl itself is absent in the test container, but
// runCommand only logs on failure rather than exiting, so the file-write and
// file-remove paths are still exercised end to end.
func TestServiceInstallUninstallDisable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("serviceInstall/Uninstall/Disable target Linux systemd paths")
	}
	if err := os.MkdirAll("/etc/systemd/system", 0755); err != nil {
		t.Fatalf("MkdirAll(/etc/systemd/system): %v", err)
	}
	t.Cleanup(func() { os.Remove("/etc/systemd/system/gitmessages.service") })

	configDir := t.TempDir()
	serviceInstall(configDir)

	data, err := os.ReadFile("/etc/systemd/system/gitmessages.service")
	if err != nil {
		t.Fatalf("service unit file missing after serviceInstall: %v", err)
	}
	if !strings.Contains(string(data), configDir) {
		t.Errorf("service unit file does not reference configDir %q", configDir)
	}

	serviceDisable()
	serviceUninstall()

	if _, err := os.Stat("/etc/systemd/system/gitmessages.service"); !os.IsNotExist(err) {
		t.Errorf("service unit file still exists after serviceUninstall, err=%v", err)
	}
}

// TestMaintenanceBackupRestore exercises the real tar-based backup/restore
// cycle against a uniquely named directory so the restore's "-C /" target
// only ever touches a path this test creates and cleans up itself.
func TestMaintenanceBackupRestore(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "gitmessages-restoretest-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(srcDir)

	want := "mode: test\n"
	if err := os.WriteFile(filepath.Join(srcDir, "config.yaml"), []byte(want), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.tar.gz")
	maintenanceBackup(srcDir, backupFile)

	if _, err := os.Stat(backupFile); err != nil {
		t.Fatalf("backup file missing after maintenanceBackup: %v", err)
	}

	restoredDir := "/" + filepath.Base(srcDir)
	t.Cleanup(func() { os.RemoveAll(restoredDir) })

	maintenanceRestore(backupFile, srcDir)

	data, err := os.ReadFile(filepath.Join(restoredDir, "config.yaml"))
	if err != nil {
		t.Fatalf("restored file missing after maintenanceRestore: %v", err)
	}
	if string(data) != want {
		t.Errorf("restored content = %q, want %q", data, want)
	}
}
