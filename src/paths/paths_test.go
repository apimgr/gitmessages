package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetDefaultDirs_NonRootLinux verifies XDG-based resolution when running
// as a non-root user is honored via XDG_CONFIG_HOME/XDG_DATA_HOME.
func TestGetDefaultDirs_NonRootLinux(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, XDG non-root branch not exercised")
	}
	if IsRunningInContainer() {
		t.Skip("running inside a container, container branch takes precedence")
	}

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)

	configDir, dataDir, logsDir := GetDefaultDirs("gitmessages")

	wantConfig := filepath.Join(tmp, "gitmessages")
	if configDir != wantConfig {
		t.Errorf("configDir = %q, want %q", configDir, wantConfig)
	}
	wantData := filepath.Join(tmp, "gitmessages")
	if dataDir != wantData {
		t.Errorf("dataDir = %q, want %q", dataDir, wantData)
	}
	if logsDir == "" {
		t.Error("logsDir should not be empty")
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
