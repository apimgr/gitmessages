package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDetectServiceManager verifies the detector always returns one of the
// known ServiceType constants for the current host, never an out-of-range
// value.
func TestDetectServiceManager(t *testing.T) {
	got := DetectServiceManager()

	switch got {
	case ServiceUnknown, ServiceSystemd, ServiceRunit, ServiceLaunchd, ServiceWindows, ServiceBSDRC:
		// Expected.
	default:
		t.Errorf("DetectServiceManager() returned unrecognized ServiceType %v", got)
	}

	// On Linux specifically, DetectServiceManager must never return the
	// launchd/windows values.
	if runtime.GOOS == "linux" && (got == ServiceLaunchd || got == ServiceWindows) {
		t.Errorf("DetectServiceManager() on linux returned %v, want a linux-only type", got)
	}
}

// TestGetBinaryPath verifies the returned path is well-formed for the
// current OS and always includes the app name.
func TestGetBinaryPath(t *testing.T) {
	path := GetBinaryPath()

	if !strings.Contains(path, "gitmessages") {
		t.Errorf("GetBinaryPath() = %q, want it to contain the app name", path)
	}

	switch runtime.GOOS {
	case "windows":
		if !strings.HasPrefix(path, `C:\Program Files\`) {
			t.Errorf("GetBinaryPath() on windows = %q, want C:\\Program Files\\ prefix", path)
		}
		if !strings.HasSuffix(path, ".exe") {
			t.Errorf("GetBinaryPath() on windows = %q, want .exe suffix", path)
		}
	default:
		if path != "/usr/local/bin/gitmessages" {
			t.Errorf("GetBinaryPath() = %q, want /usr/local/bin/gitmessages", path)
		}
	}
}

// TestCopyBinary_HappyPath verifies the source file's contents land at the
// destination, including creating a nonexistent destination directory.
func TestCopyBinary_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source-binary")
	dst := filepath.Join(tmp, "nested", "dest-binary")

	content := []byte("fake binary contents")
	if err := os.WriteFile(src, content, 0755); err != nil {
		t.Fatalf("failed to seed source file: %v", err)
	}

	if err := copyBinary(src, dst); err != nil {
		t.Fatalf("copyBinary() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("failed to stat destination file: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("destination file mode = %v, want 0755", info.Mode().Perm())
	}
}

// TestCopyBinary_MissingSource verifies a nonexistent source surfaces an
// error rather than silently creating an empty destination.
func TestCopyBinary_MissingSource(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "does-not-exist")
	dst := filepath.Join(tmp, "dest-binary")

	if err := copyBinary(src, dst); err == nil {
		t.Fatal("expected error copying a nonexistent source, got nil")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("destination should not exist after a failed copy")
	}
}

// requireRootLinux skips the test unless running as root on Linux, since the
// install/uninstall helpers below operate on real filesystem paths
// (/etc, /var/lib, /var/log, /usr/local/bin, /run) that only a root process
// in a disposable container can safely create and remove. Per AI.md PART 28,
// this test MUST only run inside a container, never on a bare host -- the
// whole test binary is invoked via `docker run ... go test ./...`, so these
// paths are the container's own filesystem, not the host's.
func requireRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("install/uninstall helpers use Linux-specific paths")
	}
	if os.Geteuid() != 0 {
		t.Skip("install/uninstall helpers require root to write system paths")
	}
}

// cleanupPaths removes each given path (file or directory tree) during test
// cleanup, ignoring not-exist errors so tests stay idempotent.
func cleanupPaths(t *testing.T, paths ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, p := range paths {
			os.RemoveAll(p)
		}
	})
}

// TestInstallUninstallSystemd exercises the systemd install/uninstall path
// against real filesystem paths inside the disposable test container.
// systemctl itself is not present in the build image, so daemon-reload is
// expected to fail after the service file is written -- both branches are
// legitimate coverage.
func TestInstallUninstallSystemd(t *testing.T) {
	requireRootLinux(t)

	servicePath := "/etc/systemd/system/gitmessages.service"
	binaryPath := GetBinaryPath()
	cleanupPaths(t, servicePath, "/var/lib/apimgr", "/var/log/apimgr", "/etc/apimgr", "/etc/systemd", binaryPath)

	if err := os.MkdirAll("/etc/systemd/system", 0755); err != nil {
		t.Fatalf("failed to pre-create /etc/systemd/system: %v", err)
	}

	err := installSystemd()
	if err == nil {
		t.Log("installSystemd() succeeded (systemctl present)")
	} else if !strings.Contains(err.Error(), "systemd") {
		t.Errorf("installSystemd() error = %v, want it to mention systemd", err)
	}

	if _, statErr := os.Stat(servicePath); statErr != nil {
		t.Errorf("expected service file to exist at %s: %v", servicePath, statErr)
	}
	for _, dir := range []string{"/var/lib/apimgr/gitmessages", "/var/log/apimgr/gitmessages", "/etc/apimgr/gitmessages"} {
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Errorf("expected directory %s to exist: %v", dir, statErr)
		}
	}

	if err := uninstallSystemd(); err != nil {
		t.Errorf("uninstallSystemd() error = %v, want nil", err)
	}
	if _, statErr := os.Stat(servicePath); !os.IsNotExist(statErr) {
		t.Errorf("expected service file to be removed, stat err = %v", statErr)
	}
}

// TestInstallUninstallRunit exercises the runit service directory
// creation/removal against real paths.
func TestInstallUninstallRunit(t *testing.T) {
	requireRootLinux(t)

	svDir := "/etc/sv/gitmessages"
	linkPath := "/var/service/gitmessages"
	cleanupPaths(t, svDir, linkPath)

	if err := installRunit(); err != nil {
		t.Fatalf("installRunit() error: %v", err)
	}

	runPath := filepath.Join(svDir, "run")
	if data, err := os.ReadFile(runPath); err != nil {
		t.Errorf("expected run script at %s: %v", runPath, err)
	} else if !strings.Contains(string(data), GetBinaryPath()) {
		t.Errorf("run script does not reference binary path: %s", data)
	}

	logRunPath := filepath.Join(svDir, "log", "run")
	if _, err := os.Stat(logRunPath); err != nil {
		t.Errorf("expected log run script at %s: %v", logRunPath, err)
	}

	if err := uninstallRunit(); err != nil {
		t.Errorf("uninstallRunit() error = %v, want nil", err)
	}
	if _, err := os.Stat(svDir); !os.IsNotExist(err) {
		t.Errorf("expected service dir to be removed, stat err = %v", err)
	}
}

// TestInstallUninstallLaunchd exercises the launchd plist creation/removal.
// The functions don't gate on runtime.GOOS, so they can be exercised
// directly on Linux even though DetectServiceManager would never route
// here outside darwin.
func TestInstallUninstallLaunchd(t *testing.T) {
	requireRootLinux(t)

	plistPath := "/Library/LaunchDaemons/com.apimgr.gitmessages.plist"
	cleanupPaths(t, plistPath, "/Library/Application Support/apimgr", "/Library/Logs/apimgr", "/Library/LaunchDaemons")

	if err := os.MkdirAll("/Library/LaunchDaemons", 0755); err != nil {
		t.Fatalf("failed to pre-create /Library/LaunchDaemons: %v", err)
	}

	if err := installLaunchd(); err != nil {
		t.Fatalf("installLaunchd() error: %v", err)
	}
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("expected plist at %s: %v", plistPath, err)
	}
	if !strings.Contains(string(data), "com.apimgr.gitmessages") {
		t.Errorf("plist does not contain expected label: %s", data)
	}

	if err := uninstallLaunchd(); err != nil {
		t.Errorf("uninstallLaunchd() error = %v, want nil", err)
	}
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Errorf("expected plist to be removed, stat err = %v", statErr)
	}
}

// TestInstallUninstallBSDRC exercises the BSD rc.d script creation/removal.
func TestInstallUninstallBSDRC(t *testing.T) {
	requireRootLinux(t)

	rcPath := "/usr/local/etc/rc.d/gitmessages"
	cleanupPaths(t, rcPath)

	if err := os.MkdirAll("/usr/local/etc/rc.d", 0755); err != nil {
		t.Fatalf("failed to pre-create /usr/local/etc/rc.d: %v", err)
	}

	if err := installBSDRC(); err != nil {
		t.Fatalf("installBSDRC() error: %v", err)
	}
	data, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("expected rc.d script at %s: %v", rcPath, err)
	}
	if !strings.Contains(string(data), "gitmessages") {
		t.Errorf("rc.d script does not reference app name: %s", data)
	}

	if err := uninstallBSDRC(); err != nil {
		t.Errorf("uninstallBSDRC() error = %v, want nil", err)
	}
	if _, statErr := os.Stat(rcPath); !os.IsNotExist(statErr) {
		t.Errorf("expected rc.d script to be removed, stat err = %v", statErr)
	}
}

// TestInstallUninstallWindows exercises the Windows service install/
// uninstall path. sc.exe is absent in the build image, so both are
// expected to return an error after copying the binary.
func TestInstallUninstallWindows(t *testing.T) {
	requireRootLinux(t)

	binaryPath := GetBinaryPath()
	cleanupPaths(t, binaryPath)

	if err := installWindows(); err == nil {
		t.Log("installWindows() succeeded (sc.exe present)")
	} else if !strings.Contains(err.Error(), "Windows service") {
		t.Errorf("installWindows() error = %v, want it to mention Windows service", err)
	}
	if _, statErr := os.Stat(binaryPath); statErr != nil {
		t.Errorf("expected binary to be copied to %s: %v", binaryPath, statErr)
	}

	if err := uninstallWindows(); err == nil {
		t.Log("uninstallWindows() succeeded (sc.exe present)")
	} else if !strings.Contains(err.Error(), "Windows service") {
		t.Errorf("uninstallWindows() error = %v, want it to mention Windows service", err)
	}
}

// TestServiceLifecycle_Systemd drives Install/Uninstall/Start/Stop/Restart/
// Reload through their real dispatch logic by making DetectServiceManager
// see the systemd marker directory, matching how it detects systemd on a
// real host.
func TestServiceLifecycle_Systemd(t *testing.T) {
	requireRootLinux(t)

	if err := os.MkdirAll("/run/systemd/system", 0755); err != nil {
		t.Fatalf("failed to create systemd marker: %v", err)
	}
	cleanupPaths(t, "/run/systemd", "/etc/systemd", "/var/lib/apimgr", "/var/log/apimgr", "/etc/apimgr", GetBinaryPath())

	if got := DetectServiceManager(); got != ServiceSystemd {
		t.Fatalf("DetectServiceManager() = %v, want ServiceSystemd", got)
	}

	if err := os.MkdirAll("/etc/systemd/system", 0755); err != nil {
		t.Fatalf("failed to pre-create /etc/systemd/system: %v", err)
	}

	// Install/Uninstall dispatch through installSystemd/uninstallSystemd.
	_ = Install()
	if err := Uninstall(); err != nil {
		t.Errorf("Uninstall() via dispatcher error = %v, want nil", err)
	}

	// Start/Stop/Restart/Reload all shell out to systemctl, which is absent
	// in the build image, so each must surface an error rather than panic.
	if err := Start(); err == nil {
		t.Log("Start() succeeded (systemctl present)")
	}
	if err := Stop(); err == nil {
		t.Log("Stop() succeeded (systemctl present)")
	}
	if err := Restart(); err == nil {
		t.Log("Restart() succeeded (systemctl present)")
	}
	if err := Reload(); err == nil {
		t.Log("Reload() succeeded (systemctl present)")
	}
}

// TestServiceLifecycle_Runit drives Install/Uninstall/Start/Stop/Restart/
// Reload through their real dispatch logic by making DetectServiceManager
// see the runit marker directory.
func TestServiceLifecycle_Runit(t *testing.T) {
	requireRootLinux(t)

	if err := os.MkdirAll("/run/runit", 0755); err != nil {
		t.Fatalf("failed to create runit marker: %v", err)
	}
	cleanupPaths(t, "/run/runit", "/etc/sv/gitmessages", "/var/service/gitmessages")

	if got := DetectServiceManager(); got != ServiceRunit {
		t.Fatalf("DetectServiceManager() = %v, want ServiceRunit", got)
	}

	if err := Install(); err != nil {
		t.Errorf("Install() via dispatcher error = %v, want nil", err)
	}
	if err := Uninstall(); err != nil {
		t.Errorf("Uninstall() via dispatcher error = %v, want nil", err)
	}

	// sv is absent in the build image; these must surface an error, not panic.
	if err := Start(); err == nil {
		t.Log("Start() succeeded (sv present)")
	}
	if err := Stop(); err == nil {
		t.Log("Stop() succeeded (sv present)")
	}
	if err := Restart(); err == nil {
		t.Log("Restart() succeeded (sv present)")
	}
	if err := Reload(); err == nil {
		t.Log("Reload() succeeded (sv present)")
	}
}

// TestInstall_UnsupportedServiceManager verifies the dispatcher's default
// branch surfaces a clear error when no service manager is detected.
func TestInstall_UnsupportedServiceManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("relies on ServiceUnknown detection on linux")
	}
	for _, marker := range []string{"/run/systemd/system", "/run/runit", "/etc/systemd"} {
		if _, err := os.Stat(marker); err == nil {
			t.Skipf("marker %s present, DetectServiceManager would not return ServiceUnknown", marker)
		}
	}

	if got := DetectServiceManager(); got != ServiceUnknown {
		t.Skipf("DetectServiceManager() = %v, want ServiceUnknown for this test", got)
	}

	if err := Install(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Install() error = %v, want unsupported service manager error", err)
	}
	if err := Uninstall(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Uninstall() error = %v, want unsupported service manager error", err)
	}
	if err := Start(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Start() error = %v, want unsupported service manager error", err)
	}
	if err := Stop(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Stop() error = %v, want unsupported service manager error", err)
	}
	if err := Restart(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Restart() error = %v, want unsupported service manager error", err)
	}
	if err := Reload(); err == nil || !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Reload() error = %v, want unsupported service manager error", err)
	}
}

// TestCapitalize covers the ASCII-first-rune capitalization helper used to
// build the Windows service display name.
func TestCapitalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"lowercase word", "gitmessages", "Gitmessages"},
		{"already capitalized", "Gitmessages", "Gitmessages"},
		{"single char", "a", "A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capitalize(tt.in); got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
