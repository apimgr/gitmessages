package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
)

func TestConfigureOutputFallsBackToEnglish(t *testing.T) {
	previous, existed := os.LookupEnv("GITMESSAGES_LANG")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("GITMESSAGES_LANG", previous)
		} else {
			_ = os.Unsetenv("GITMESSAGES_LANG")
		}
	})
	if !i18n.IsSupported("en") {
		t.Fatal("English locale is unavailable")
	}
	if err := configureOutput("auto", "unsupported"); err != nil {
		t.Fatalf("unsupported language should fall back silently: %v", err)
	}
	if got := os.Getenv("GITMESSAGES_LANG"); got != "en" {
		t.Fatalf("unsupported language fallback = %q, want en", got)
	}
}

func TestConfigureSMTPDisablesEmailWithoutConfiguredRelay(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.Enabled = true
	configureSMTP(configuration)
	if configuration.Server.Notifications.Email.Enabled {
		t.Fatal("email remained enabled when SMTP_HOST was explicitly empty")
	}
}

func TestConfigureSMTPDisablesUnavailableConfiguredRelay(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.Enabled = true
	configuration.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
	configuration.Server.Notifications.Email.SMTP.Port = 1
	configuration.Server.Notifications.Email.SMTP.TLS = "none"
	configureSMTP(configuration)
	if configuration.Server.Notifications.Email.Enabled {
		t.Fatal("email remained enabled after configured SMTP probe failed")
	}
}

func TestServerShellIntegrationOutputsAndDetection(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"gitmessages-test"}
	t.Cleanup(func() { os.Args = oldArgs })
	capture := func(call func() error) (string, error) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		previous := os.Stdout
		os.Stdout = writer
		callErr := call()
		_ = writer.Close()
		os.Stdout = previous
		output, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(output), callErr
	}
	if output, err := capture(func() error { return printShellIntegration("help", "") }); err != nil || !strings.Contains(output, "completions") {
		t.Fatalf("shell help = %q, %v", output, err)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "sh", "powershell"} {
		output, err := capture(func() error { return printShellIntegration("completions", shell) })
		if err != nil || !strings.Contains(output, "gitmessages-test") {
			t.Fatalf("%s completions = %q, %v", shell, output, err)
		}
		output, err = capture(func() error { return printShellIntegration("init", shell) })
		if err != nil || !strings.Contains(output, "gitmessages-test") {
			t.Fatalf("%s init = %q, %v", shell, output, err)
		}
	}
	if _, err := capture(func() error { return printShellIntegration("completions", "unknown") }); err == nil {
		t.Fatal("unsupported shell was accepted")
	}
	if _, err := capture(func() error { return printShellIntegration("unknown", "bash") }); err == nil {
		t.Fatal("unsupported shell integration action was accepted")
	}
	t.Setenv("SHELL", "/bin/zsh")
	if got := detectShell(); got != "zsh" {
		t.Fatalf("detectShell() = %q, want zsh", got)
	}
	t.Setenv("SHELL", "")
	t.Setenv("ComSpec", "powershell.exe")
	if got := detectShell(); got != "powershell" {
		t.Fatalf("detectShell() = %q, want powershell", got)
	}
	t.Setenv("ComSpec", "")
	if got := detectShell(); got != "bash" {
		t.Fatalf("detectShell() = %q, want bash", got)
	}
}

func TestConfiguredTokenCookieAge(t *testing.T) {
	for _, input := range []struct {
		value string
		want  time.Duration
	}{
		{"", 365 * 24 * time.Hour},
		{" 1y ", 365 * 24 * time.Hour},
		{"2y", 2 * 365 * 24 * time.Hour},
		{"720h", 30 * 24 * time.Hour},
		{"0", 365 * 24 * time.Hour},
		{"invalid", 365 * 24 * time.Hour},
		{"0y", 365 * 24 * time.Hour},
	} {
		if got := configuredTokenCookieAge(input.value); got != input.want {
			t.Errorf("configuredTokenCookieAge(%q) = %s, want %s", input.value, got, input.want)
		}
	}
}

// TestSetupRoutes is a mux smoke test verifying routes dispatch instead of
// falling through to a 404.
func TestSetupRoutes(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	paths := []string{
		"/", "/types", "/templates", "/builder", "/server/about",
		"/server/help", "/server/healthz", "/manifest.json", "/sw.js",
		"/api/v1", "/api/v1/types", "/api/v1/conventions",
		"/api/v1/templates", "/api/v1/messages/random", "/api/v1/server/healthz",
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
		if r.URL.Path != "/api/v1/server/healthz" {
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
	_ = ln.Close()

	if err := checkHealth(freePort); err == nil {
		t.Error("checkHealth against a closed port: expected error, got nil")
	}
}

func TestNormalizeUpdateBranch(t *testing.T) {
	for input, want := range map[string]string{
		"stable":      "stable",
		"beta":        "beta",
		"devel":       "devel",
		"daily":       "devel",
		"rolling":     "devel",
		"development": "devel",
		"invalid":     "",
	} {
		if got := normalizeUpdateBranch(input); got != want {
			t.Errorf("normalizeUpdateBranch(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestApplyI2POverrides(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"gitmessages"}
	current := config.DefaultConfig()
	current.Server.I2P.Enabled = false
	t.Setenv("I2P_ENABLED", "oui")
	if err := applyI2POverrides(current, false); err != nil || !current.Server.I2P.Enabled {
		t.Fatalf("truthy I2P_ENABLED result = %v, %v", current.Server.I2P.Enabled, err)
	}
	t.Setenv("I2P_ENABLED", "not-a-boolean")
	if err := applyI2POverrides(current, false); err == nil {
		t.Fatal("invalid I2P_ENABLED was accepted")
	}
	t.Setenv("I2P_ENABLED", "true")
	os.Args = []string{"gitmessages", "--i2p=false"}
	if err := applyI2POverrides(current, false); err != nil || current.Server.I2P.Enabled {
		t.Fatalf("explicit --i2p=false did not override env: enabled=%v err=%v", current.Server.I2P.Enabled, err)
	}
	if err := applyI2POverrides(nil, false); err == nil {
		t.Fatal("nil configuration was accepted")
	}
}

func TestConfigurationFingerprintAndWatcher(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  mode: production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first := configurationFingerprint(directory)
	if first == "" {
		t.Fatal("configuration fingerprint is empty")
	}
	if err := os.WriteFile(configPath, []byte("server:\n  mode: development\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if configurationFingerprint(directory) == first {
		t.Fatal("configuration fingerprint did not change after an edit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	changed := make(chan struct{}, 1)
	go watchConfiguration(ctx, directory, changed)
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte("server:\n  mode: debug\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("configuration watcher did not observe the change")
	}
	cancel()
}

func TestPrintHelp(t *testing.T) {
	printHelp("gitmessages")
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
	if reloaded.Server.Update.Branch != "beta" {
		t.Errorf("Server.Update.Branch = %q, want beta", reloaded.Server.Update.Branch)
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

// TestSystemdServiceInstallAndUninstallCancellation exercises service file
// creation and verifies that a declined uninstall preserves the service data.
func TestSystemdServiceInstallAndUninstallCancellation(t *testing.T) {
	configDir := t.TempDir()
	serviceFile := filepath.Join(t.TempDir(), "systemd", "gitmessages.service")
	var commands []string
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	var installOutput bytes.Buffer
	serviceInstall(configDir, "linux", serviceFile, "", command, &installOutput)

	data, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("service unit file missing after serviceInstall: %v", err)
	}
	if !strings.Contains(string(data), configDir) {
		t.Errorf("service unit file does not reference configDir %q", configDir)
	}
	if len(commands) != 3 || commands[0] != "systemctl daemon-reload" || commands[1] != "systemctl enable gitmessages" || commands[2] != "systemctl start gitmessages" {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(installOutput.String(), "Service installed and started successfully") {
		t.Errorf("installation status missing: %q", installOutput.String())
	}

	var output bytes.Buffer
	serviceUninstallWithInput(configDir, strings.NewReader("n\n"), &output)

	if _, err := os.Stat(serviceFile); err != nil {
		t.Errorf("service unit file was removed despite uninstall cancellation, err=%v", err)
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Errorf("configuration directory was removed despite uninstall cancellation, err=%v", err)
	}
	if !strings.Contains(output.String(), "This will delete ALL data, configs, and the system user. Continue? [y/N]") {
		t.Errorf("uninstall confirmation prompt missing: %q", output.String())
	}

	var unsupportedOutput bytes.Buffer
	if err := installServiceFor("openbsd", configDir, "", "", command, &unsupportedOutput); err != nil {
		t.Fatalf("unsupported service platform should report status without failing: %v", err)
	}
	if !strings.Contains(unsupportedOutput.String(), "Service installation not supported on openbsd") {
		t.Errorf("unsupported platform status missing: %q", unsupportedOutput.String())
	}
	serviceInstall(configDir, "linux", filepath.Join(t.TempDir(), "missing", "gitmessages.service"), "", command, &installOutput)
}

func TestLaunchdServiceInstall(t *testing.T) {
	configDir := t.TempDir()
	serviceFile := filepath.Join(t.TempDir(), "LaunchDaemons", "gitmessages.plist")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	var commands []string
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	var output bytes.Buffer
	if err := installServiceFor("darwin", configDir, "", serviceFile, command, &output); err != nil {
		t.Fatalf("installServiceFor: %v", err)
	}
	data, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("service plist missing after installServiceFor: %v", err)
	}
	if !strings.Contains(string(data), configDir) {
		t.Errorf("service plist does not reference configDir %q", configDir)
	}
	if len(commands) != 1 || commands[0] != "launchctl load "+serviceFile {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(output.String(), "Service installed and started successfully") {
		t.Errorf("installation status missing: %q", output.String())
	}
}

func TestRemoveServiceData(t *testing.T) {
	root := t.TempDir()
	dirs := paths.Directories{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
		Logs:   filepath.Join(root, "logs"),
		Backup: filepath.Join(root, "backup"),
		PID:    filepath.Join(root, "run", "gitmessages.pid"),
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, filepath.Dir(dirs.PID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	if err := os.WriteFile(dirs.PID, []byte("123"), 0600); err != nil {
		t.Fatalf("WriteFile(PID): %v", err)
	}
	if err := removeServiceData(dirs); err != nil {
		t.Fatalf("removeServiceData: %v", err)
	}
	for _, path := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, dirs.PID} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("path remains after service uninstall: %s (err=%v)", path, err)
		}
	}
}

func TestConfirmServiceUninstall(t *testing.T) {
	var output bytes.Buffer
	if !confirmServiceUninstall(strings.NewReader("yes\n"), &output) {
		t.Fatal("affirmative response did not confirm service uninstall")
	}
	if !strings.Contains(output.String(), "This will delete ALL data, configs, and the system user. Continue? [y/N]") {
		t.Errorf("uninstall confirmation prompt missing: %q", output.String())
	}
}

func TestUninstallService(t *testing.T) {
	root := t.TempDir()
	serviceFile := filepath.Join(root, "systemd", "gitmessages.service")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	if err := os.WriteFile(serviceFile, []byte("unit"), 0600); err != nil {
		t.Fatalf("WriteFile(service file): %v", err)
	}
	dirs := paths.Directories{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
		Logs:   filepath.Join(root, "logs"),
		Backup: filepath.Join(root, "backup"),
		PID:    filepath.Join(root, "run", "gitmessages.pid"),
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, filepath.Dir(dirs.PID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	if err := os.WriteFile(dirs.PID, []byte("123"), 0600); err != nil {
		t.Fatalf("WriteFile(PID): %v", err)
	}
	var commands []string
	var output bytes.Buffer
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := uninstallService("linux", dirs, serviceFile, "/usr/local/bin/gitmessages", &output, command); err != nil {
		t.Fatalf("uninstallService: %v", err)
	}
	if len(commands) != 3 || commands[0] != "systemctl stop gitmessages" || commands[1] != "systemctl disable gitmessages" || commands[2] != "systemctl daemon-reload" {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(output.String(), "Service uninstalled. Delete binary manually: rm /usr/local/bin/gitmessages") {
		t.Errorf("binary removal guidance missing: %q", output.String())
	}
	for _, path := range []string{serviceFile, dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, dirs.PID} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("path remains after uninstall: %s (err=%v)", path, err)
		}
	}
}

func TestUninstallServiceDarwin(t *testing.T) {
	serviceFile := filepath.Join(t.TempDir(), "launchd", "gitmessages.plist")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	if err := os.WriteFile(serviceFile, []byte("plist"), 0600); err != nil {
		t.Fatalf("WriteFile(service file): %v", err)
	}
	var commands []string
	var output bytes.Buffer
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := uninstallService("darwin", paths.Directories{}, serviceFile, "/usr/local/bin/gitmessages", &output, command); err != nil {
		t.Fatalf("uninstallService: %v", err)
	}
	if len(commands) != 1 || commands[0] != "launchctl unload "+serviceFile {
		t.Errorf("service commands = %v", commands)
	}
	if _, err := os.Stat(serviceFile); !os.IsNotExist(err) {
		t.Errorf("launchd plist remains after uninstall (err=%v)", err)
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
	defer func() { _ = os.RemoveAll(srcDir) }()

	want := "mode: test\n"
	if err := os.WriteFile(filepath.Join(srcDir, "config.yaml"), []byte(want), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.tar.gz")
	maintenanceBackup(srcDir, backupFile)

	if _, err := os.Stat(backupFile); err != nil {
		t.Fatalf("backup file missing after maintenanceBackup: %v", err)
	}

	maintenanceRestore(backupFile, srcDir)

	data, err := os.ReadFile(filepath.Join(srcDir, "config.yaml"))
	if err != nil {
		t.Fatalf("restored file missing after maintenanceRestore: %v", err)
	}
	if string(data) != want {
		t.Errorf("restored content = %q, want %q", data, want)
	}
}
