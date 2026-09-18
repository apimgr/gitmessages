package paths

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
)

// withForcedDetection overrides the isContainer/isRootUser hooks for the
// duration of the test, restoring the originals afterward, so the
// root/non-root and container/non-container branches of GetDefaultDirs can
// be exercised regardless of the real environment the test binary happens
// to run in (which, under this project's Docker-only test workflow, is
// always root inside a container).
func withForcedDetection(t *testing.T, container, root bool) {
	t.Helper()
	savedContainer := isContainer
	savedRoot := isRootUser
	isContainer = func() bool { return container }
	isRootUser = func() bool { return root }
	t.Cleanup(func() {
		isContainer = savedContainer
		isRootUser = savedRoot
	})
}

// TestGetDefaultDirs_Container verifies the container branch short-circuits
// to the fixed /config,/data,/logs paths regardless of privilege level.
func TestGetDefaultDirs_Container(t *testing.T) {
	for _, root := range []bool{true, false} {
		withForcedDetection(t, true, root)
		configDir, dataDir, logsDir := GetDefaultDirs("gitmessages")
		if configDir != "/config" || dataDir != "/data" || logsDir != "/logs" {
			t.Errorf("GetDefaultDirs() (root=%v) = (%q, %q, %q), want (/config, /data, /logs)", root, configDir, dataDir, logsDir)
		}
	}
}

// TestGetDefaultDirs_RootLinux verifies the root, non-container branch
// resolves to the system-wide /etc, /var/lib, /var/log paths on Linux.
func TestGetDefaultDirs_RootLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the linux-specific root path layout")
	}
	withForcedDetection(t, false, true)

	configDir, dataDir, logsDir := GetDefaultDirs("gitmessages")

	if want := filepath.Join("/etc", OrgName, "gitmessages"); configDir != want {
		t.Errorf("configDir = %q, want %q", configDir, want)
	}
	if want := filepath.Join("/var/lib", OrgName, "gitmessages"); dataDir != want {
		t.Errorf("dataDir = %q, want %q", dataDir, want)
	}
	if want := filepath.Join("/var/log", OrgName, "gitmessages"); logsDir != want {
		t.Errorf("logsDir = %q, want %q", logsDir, want)
	}
}

// TestGetDefaultDirs_NonRootLinux verifies XDG-based resolution when running
// as a non-root user is honored via XDG_CONFIG_HOME/XDG_DATA_HOME.
func TestGetDefaultDirs_NonRootLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the linux-specific XDG path layout")
	}
	withForcedDetection(t, false, false)

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	configDir, dataDir, logsDir := GetDefaultDirs("gitmessages")

	wantConfig := filepath.Join(tmp, OrgName, "gitmessages")
	if configDir != wantConfig {
		t.Errorf("configDir = %q, want %q", configDir, wantConfig)
	}
	wantData := filepath.Join(tmp, OrgName, "gitmessages")
	if dataDir != wantData {
		t.Errorf("dataDir = %q, want %q", dataDir, wantData)
	}
	wantLogs := filepath.Join(tmp, OrgName, "gitmessages", "logs")
	if logsDir != wantLogs {
		t.Errorf("logsDir = %q, want %q", logsDir, wantLogs)
	}
}

// TestGetDefaultDirs_NonRootLinux_NoXDG verifies the fallback to
// ~/.config and ~/.local/share when XDG_CONFIG_HOME/XDG_DATA_HOME are unset.
func TestGetDefaultDirs_NonRootLinux_NoXDG(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the linux-specific XDG fallback layout")
	}
	withForcedDetection(t, false, false)

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	os.Unsetenv("XDG_CONFIG_HOME")
	os.Unsetenv("XDG_DATA_HOME")

	currentUser, err := user.Current()
	if err != nil {
		t.Skipf("user.Current() unavailable: %v", err)
	}

	configDir, dataDir, logsDir := GetDefaultDirs("gitmessages")

	wantConfig := filepath.Join(currentUser.HomeDir, ".config", OrgName, "gitmessages")
	if configDir != wantConfig {
		t.Errorf("configDir = %q, want %q", configDir, wantConfig)
	}
	wantData := filepath.Join(currentUser.HomeDir, ".local", "share", OrgName, "gitmessages")
	if dataDir != wantData {
		t.Errorf("dataDir = %q, want %q", dataDir, wantData)
	}
	wantLogs := filepath.Join(currentUser.HomeDir, ".local", "share", OrgName, "gitmessages", "logs")
	if logsDir != wantLogs {
		t.Errorf("logsDir = %q, want %q", logsDir, wantLogs)
	}
}

// TestGetDirectories is a smoke test: it must return non-empty paths for
// all three directories regardless of environment.
func TestGetDirectories(t *testing.T) {
	dirs := GetDirectories()
	if dirs.Config == "" {
		t.Error("Config dir is empty")
	}
	if dirs.Data == "" {
		t.Error("Data dir is empty")
	}
	if dirs.Logs == "" {
		t.Error("Logs dir is empty")
	}
}

// TestEnsureDir verifies a directory is created (and creation is
// idempotent when called twice on the same path).
func TestEnsureDir(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "nested", "dir")

	if err := EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir() error: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", target)
	}

	// Idempotent: calling again must not error.
	if err := EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir() second call error: %v", err)
	}
}

// TestEnsureDirectories verifies all three configured directories are
// created on disk.
func TestEnsureDirectories(t *testing.T) {
	tmp := t.TempDir()
	dirs := Directories{
		Config: filepath.Join(tmp, "config"),
		Data:   filepath.Join(tmp, "data"),
		Logs:   filepath.Join(tmp, "logs"),
	}

	if err := EnsureDirectories(dirs); err != nil {
		t.Fatalf("EnsureDirectories() error: %v", err)
	}

	for _, d := range []string{dirs.Config, dirs.Data, dirs.Logs} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("expected %q to exist: %v", d, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", d)
		}
	}
}

// TestGetBackupDir checks the hardcoded backup path is well-formed and
// includes the org/project names.
func TestGetBackupDir(t *testing.T) {
	got := GetBackupDir()
	want := filepath.Join("/mnt/Backups", OrgName, ProjectName)
	if got != want {
		t.Errorf("GetBackupDir() = %q, want %q", got, want)
	}
}

// TestIsRunningInContainer is a smoke test: it must not panic and must
// return a boolean consistent with the actual environment. We only assert
// it doesn't error/panic since the sandbox's container status is unknown.
func TestIsRunningInContainer(t *testing.T) {
	_ = IsRunningInContainer()
}
