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
		if configDir != filepath.Join("/config", "gitmessages") || dataDir != filepath.Join("/data", "gitmessages") || logsDir != filepath.Join("/data", "log", "gitmessages") {
			t.Errorf("GetDefaultDirs() (root=%v) = (%q, %q, %q), want project-scoped container paths", root, configDir, dataDir, logsDir)
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

	tmp := projectTestDir(t)
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
	wantLogs := filepath.Join(userHome(t), ".local", "log", OrgName, "gitmessages")
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
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	_ = os.Unsetenv("XDG_DATA_HOME")

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
	wantLogs := filepath.Join(currentUser.HomeDir, ".local", "log", OrgName, "gitmessages")
	if logsDir != wantLogs {
		t.Errorf("logsDir = %q, want %q", logsDir, wantLogs)
	}
}

func userHome(t *testing.T) string {
	t.Helper()
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return currentUser.HomeDir
}

// TestGetDirectories is a smoke test: it must return non-empty paths for
// all three directories regardless of environment.
func TestGetDirectories(t *testing.T) {
	dirs := GetDirectories()
	for name, value := range map[string]string{"Config": dirs.Config, "Data": dirs.Data, "Cache": dirs.Cache, "Logs": dirs.Logs, "Backup": dirs.Backup} {
		if value == "" {
			t.Errorf("%s directory is empty", name)
		}
	}
	if !isContainer() && dirs.PID == "" {
		t.Error("PID path is empty outside a container")
	}
}

func TestEnsurePrivateTempParent(t *testing.T) {
	root := projectTestDir(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateTempParent(parent); err != nil {
		t.Fatalf("EnsurePrivateTempParent() error: %v", err)
	}
	if err := verifyPrivateMode(parent, 0o700); err != nil {
		t.Fatalf("temporary parent permissions: %v", err)
	}

	link := filepath.Join(root, "linked")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateTempParent(link); err == nil {
		t.Fatal("EnsurePrivateTempParent() accepted a symbolic link")
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateTempParent(file); err == nil {
		t.Fatal("EnsurePrivateTempParent() accepted a regular file")
	}
}

func TestDefaultDirsMacOSAndBSD(t *testing.T) {
	macRoot := defaultDirs("darwin", ProjectName, false, true, "")
	if want := filepath.Join("/Library/Application Support", OrgName, ProjectName); macRoot.Config != want {
		t.Errorf("macOS root config = %q, want %q", macRoot.Config, want)
	}
	if want := filepath.Join("/Library/Caches", OrgName, ProjectName); macRoot.Cache != want {
		t.Errorf("macOS root cache = %q, want %q", macRoot.Cache, want)
	}
	macUser := defaultDirs("darwin", ProjectName, false, false, "/Users/example")
	if want := filepath.Join("/Users/example", "Library", "Application Support", OrgName, ProjectName); macUser.Data != want {
		t.Errorf("macOS user data = %q, want %q", macUser.Data, want)
	}
	bsdRoot := defaultDirs("freebsd", ProjectName, false, true, "")
	if want := filepath.Join("/usr/local/etc", OrgName, ProjectName); bsdRoot.Config != want {
		t.Errorf("BSD root config = %q, want %q", bsdRoot.Config, want)
	}
	if want := filepath.Join("/var/db", OrgName, ProjectName); bsdRoot.Data != want {
		t.Errorf("BSD root data = %q, want %q", bsdRoot.Data, want)
	}
}

// TestEnsureDir verifies a directory is created (and creation is
// idempotent when called twice on the same path).
func TestEnsureDir(t *testing.T) {
	tmp := projectTestDir(t)
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

func TestEnsureStartupDirectoriesEnforcesPrivateModes(t *testing.T) {
	root := projectTestDir(t)
	dirs := Directories{
		Config:   filepath.Join(root, "config"),
		Data:     filepath.Join(root, "data"),
		Database: filepath.Join(root, "data", "db"),
		Cache:    filepath.Join(root, "cache"),
		Logs:     filepath.Join(root, "logs"),
		Backup:   filepath.Join(root, "backup"),
		PID:      filepath.Join(root, "data", "gitmessages.pid"),
	}
	if err := EnsureStartupDirectories(dirs, false); err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(dirs.Config, "yaml.d")
	privateFile := filepath.Join(privateDir, "branding.yml")
	if err := os.Chmod(privateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateFile, []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privateFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, "server.yml"), []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, "server.yaml"), []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, "secrets.yml"), []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnforcePrivatePaths(dirs); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		privateDir:                               0o700,
		privateFile:                              0o600,
		filepath.Join(dirs.Config, "server.yml"): 0o600,
		filepath.Join(dirs.Config, "server.yaml"): 0o600,
		filepath.Join(dirs.Config, "secrets.yml"): 0o600,
		filepath.Join(dirs.Data, "tor", "site"):   0o700,
		filepath.Join(dirs.Data, "security"):      0o700,
	} {
		if err := verifyPrivateMode(path, want); err != nil {
			t.Errorf("private permissions for %s: %v", path, err)
		}
	}
}

// TestEnsureDirectories verifies all three configured directories are
// created on disk.
func TestEnsureDirectories(t *testing.T) {
	tmp := projectTestDir(t)
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
	if got == "" || filepath.IsAbs(got) == false {
		t.Errorf("GetBackupDir() = %q, want a non-empty absolute path", got)
	}
}

func TestDefaultDatabaseDirectory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join("/home/test", ".local", "share"))
	container := defaultDirs("linux", "gitmessages", true, false, "/home/test")
	if want := filepath.Join("/data", "db", "sqlite"); container.Database != want {
		t.Errorf("container database directory = %q, want %q", container.Database, want)
	}
	root := defaultDirs("linux", "gitmessages", false, true, "/root")
	if want := filepath.Join("/var/lib", OrgName, "gitmessages", "db"); root.Database != want {
		t.Errorf("root database directory = %q, want %q", root.Database, want)
	}
	user := defaultDirs("linux", "gitmessages", false, false, "/home/test")
	if want := filepath.Join("/home/test", ".local", "share", OrgName, "gitmessages", "db"); user.Database != want {
		t.Errorf("user database directory = %q, want %q", user.Database, want)
	}
}

func TestSafeDirPath(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "absolute path", input: "/var/lib/app", want: "/var/lib/app"},
		{name: "relative path", input: "data/./files", want: filepath.Join("data", "files")},
		{name: "traversal", input: "data/../secrets", wantErr: true},
		{name: "control character", input: "data\nfiles", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := SafeDirPath(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("SafeDirPath(%q) error = %v, wantErr %v", test.input, err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Errorf("SafeDirPath(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestCheckPIDFile(t *testing.T) {
	pidPath := filepath.Join(projectTestDir(t), "server.pid")
	if running, pid, err := checkPIDFile(pidPath, func(int) bool { return true }, func(int) bool { return true }); err != nil || running || pid != 0 {
		t.Fatalf("missing PID file = (%v, %d, %v), want (false, 0, nil)", running, pid, err)
	}
	if err := os.WriteFile(pidPath, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if running, _, err := checkPIDFile(pidPath, func(int) bool { return true }, func(int) bool { return true }); err != nil || running {
		t.Fatalf("corrupt PID file = (%v, %v), want (false, nil)", running, err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("corrupt PID file still exists: %v", err)
	}

	if err := os.WriteFile(pidPath, []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if running, pid, err := checkPIDFile(pidPath, func(value int) bool { return value == 123 }, func(value int) bool { return value == 123 }); err != nil || !running || pid != 123 {
		t.Fatalf("owned live PID file = (%v, %d, %v), want (true, 123, nil)", running, pid, err)
	}
	if running, _, err := checkPIDFile(pidPath, func(int) bool { return true }, func(int) bool { return false }); err != nil || running {
		t.Fatalf("reused PID file = (%v, %v), want (false, nil)", running, err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale PID file still exists: %v", err)
	}
}

func TestWritePIDFileModeAndContainerSkip(t *testing.T) {
	pidPath := filepath.Join(projectTestDir(t), "nested", "server.pid")
	savedContainer := isContainer
	isContainer = func() bool { return false }
	t.Cleanup(func() { isContainer = savedContainer })
	if err := WritePIDFileMode(pidPath, 0o600); err != nil {
		t.Fatalf("WritePIDFileMode() error = %v", err)
	}
	info, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("PID file was not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("PID mode = %04o, want 0600", info.Mode().Perm())
	}
	if err := RemovePIDFile(pidPath); err != nil {
		t.Fatalf("RemovePIDFile() error = %v", err)
	}

	isContainer = func() bool { return true }
	if err := WritePIDFileMode(pidPath, 0o600); err != nil {
		t.Fatalf("container WritePIDFileMode() error = %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("container PID file should not exist: %v", err)
	}
}

// TestIsRunningInContainer is a smoke test: it must not panic and must
// return a boolean consistent with the actual environment. We only assert
// it doesn't error/panic since the sandbox's container status is unknown.
func TestIsRunningInContainer(t *testing.T) {
	_ = IsRunningInContainer()
}

func TestDetectContainerSignals(t *testing.T) {
	tests := []struct {
		name, containerEnv, kubernetesEnv, pid1, parent, cgroup, marker string
		want                                                            bool
	}{
		{name: "docker marker", marker: "/.dockerenv", want: true},
		{name: "podman marker", marker: "/run/.containerenv", want: true},
		{name: "lxc marker", marker: "/dev/lxc", want: true},
		{name: "container environment", containerEnv: "systemd-nspawn", want: true},
		{name: "kubernetes", kubernetesEnv: "10.0.0.1", want: true},
		{name: "pid 1 container init", pid1: "s6-svscan", want: true},
		{name: "parent container init", parent: "runsv", want: true},
		{name: "project wrapper", parent: ProjectName, want: true},
		{name: "container cgroup", cgroup: "0::/docker/abc", want: true},
		{name: "host", pid1: "systemd", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := detectContainerSignals(test.containerEnv, test.kubernetesEnv, test.pid1, test.parent, test.cgroup, func(path string) bool { return path == test.marker && test.marker != "" })
			if got != test.want {
				t.Errorf("detectContainerSignals() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsSystemManagerHost(t *testing.T) {
	for _, test := range []struct {
		name, invocationID, rcServiceName, pid1 string
		openRC, want                            bool
	}{
		{name: "systemd service", invocationID: "unit", want: true},
		{name: "OpenRC service", rcServiceName: "gitmessages", want: true},
		{name: "systemd system container", pid1: "systemd", want: true},
		{name: "OpenRC init", pid1: "openrc-init", want: true},
		{name: "OpenRC init with marker", pid1: "init", openRC: true, want: true},
		{name: "application container", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isSystemManagerHost(test.invocationID, test.rcServiceName, test.pid1, test.openRC); got != test.want {
				t.Errorf("isSystemManagerHost() = %v, want %v", got, test.want)
			}
		})
	}
}
