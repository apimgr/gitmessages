package paths

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

const (
	OrgName     = "apimgr"
	ProjectName = "gitmessages"
)

type Directories struct {
	Config   string
	Data     string
	Database string
	Cache    string
	Logs     string
	Backup   string
	PID      string
}

// EnsurePrivateTempParent creates and validates an owner-only project temp parent.
func EnsurePrivateTempParent(parent string) error {
	return ensurePrivateTempParent(parent)
}

// SafeDirPath validates a filesystem directory override without changing its
// absolute or relative meaning.
func SafeDirPath(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("empty path")
	}
	for _, character := range input {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("path contains control characters")
		}
	}
	for _, segment := range strings.Split(filepath.ToSlash(input), "/") {
		if segment == ".." {
			return "", fmt.Errorf("path contains traversal segment")
		}
	}
	return filepath.Clean(input), nil
}

var (
	isContainer = IsRunningInContainer
	isRootUser  = func() bool {
		if runtime.GOOS == "windows" {
			return os.Getenv("USERDOMAIN") == os.Getenv("COMPUTERNAME")
		}
		return os.Geteuid() == 0
	}
)

func GetDirectories() Directories {
	currentUser, _ := user.Current()
	home := ""
	if currentUser != nil {
		home = currentUser.HomeDir
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	container := isContainer()
	root := isRootUser()
	dirs := defaultDirs(runtime.GOOS, ProjectName, container, root, home)
	if databaseDir := os.Getenv("DATABASE_DIR"); databaseDir != "" {
		if safe, err := SafeDirPath(databaseDir); err == nil {
			if absolute, err := filepath.Abs(safe); err == nil {
				dirs.Database = absolute
			}
		}
	}
	if backup := os.Getenv("BACKUP_DIR"); backup != "" {
		if safe, err := SafeDirPath(backup); err == nil {
			if absolute, err := filepath.Abs(safe); err == nil {
				dirs.Backup = absolute
			}
		}
	} else if !container {
		dirs.Backup = defaultBackupDir(dirs.Data, root, home)
	}
	return dirs
}

func defaultDirs(goos, projectName string, container, root bool, home string) Directories {
	if container {
		base := filepath.Join("/data", projectName)
		return Directories{
			Config:   filepath.Join("/config", projectName),
			Data:     base,
			Database: filepath.Join("/data", "db", "sqlite"),
			Cache:    filepath.Join(base, "cache"),
			Logs:     filepath.Join("/data", "log", projectName),
			Backup:   filepath.Join("/data", "backups", projectName),
		}
	}
	if root {
		switch goos {
		case "windows":
			programData := os.Getenv("ProgramData")
			if programData == "" {
				programData = `C:\ProgramData`
			}
			base := filepath.Join(programData, OrgName, projectName)
			data := filepath.Join(base, "data")
			return Directories{Config: base, Data: data, Database: filepath.Join(data, "db"), Cache: filepath.Join(base, "cache"), Logs: filepath.Join(base, "logs"), Backup: filepath.Join(programData, "Backups", OrgName, projectName)}
		case "darwin":
			base := filepath.Join("/Library/Application Support", OrgName, projectName)
			data := filepath.Join(base, "data")
			return Directories{Config: base, Data: data, Database: filepath.Join(data, "db"), Cache: filepath.Join("/Library/Caches", OrgName, projectName), Logs: filepath.Join("/Library/Logs", OrgName, projectName), Backup: filepath.Join("/Library/Backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName, projectName+".pid")}
		case "freebsd", "openbsd", "netbsd":
			data := filepath.Join("/var/db", OrgName, projectName)
			return Directories{Config: filepath.Join("/usr/local/etc", OrgName, projectName), Data: data, Database: filepath.Join(data, "db"), Cache: filepath.Join("/var/cache", OrgName, projectName), Logs: filepath.Join("/var/log", OrgName, projectName), Backup: filepath.Join("/var/backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName, projectName+".pid")}
		default:
			data := filepath.Join("/var/lib", OrgName, projectName)
			return Directories{Config: filepath.Join("/etc", OrgName, projectName), Data: data, Database: filepath.Join(data, "db"), Cache: filepath.Join("/var/cache", OrgName, projectName), Logs: filepath.Join("/var/log", OrgName, projectName), Backup: filepath.Join("/mnt/Backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName, projectName+".pid")}
		}
	}
	if home == "" {
		home = "."
	}
	switch goos {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		base := filepath.Join(localAppData, OrgName, projectName)
		return Directories{Config: filepath.Join(appData, OrgName, projectName), Data: base, Database: filepath.Join(base, "db"), Cache: filepath.Join(base, "cache"), Logs: filepath.Join(base, "logs"), Backup: filepath.Join(localAppData, "Backups", OrgName, projectName)}
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support", OrgName, projectName)
		return Directories{Config: base, Data: base, Database: filepath.Join(base, "db"), Cache: filepath.Join(home, "Library", "Caches", OrgName, projectName), Logs: filepath.Join(home, "Library", "Logs", OrgName, projectName), Backup: filepath.Join(home, "Library", "Backups", OrgName, projectName), PID: filepath.Join(base, projectName+".pid")}
	default:
		configBase := os.Getenv("XDG_CONFIG_HOME")
		if configBase == "" {
			configBase = filepath.Join(home, ".config")
		}
		dataBase := os.Getenv("XDG_DATA_HOME")
		if dataBase == "" {
			dataBase = filepath.Join(home, ".local", "share")
		}
		data := filepath.Join(dataBase, OrgName, projectName)
		cacheBase := os.Getenv("XDG_CACHE_HOME")
		if cacheBase == "" {
			cacheBase = filepath.Join(home, ".cache")
		}
		return Directories{Config: filepath.Join(configBase, OrgName, projectName), Data: data, Database: filepath.Join(data, "db"), Cache: filepath.Join(cacheBase, OrgName, projectName), Logs: filepath.Join(home, ".local", "log", OrgName, projectName), Backup: filepath.Join(dataBase, "Backups", OrgName, projectName), PID: filepath.Join(data, projectName+".pid")}
	}
}

func GetDefaultDirs(projectName string) (configDir, dataDir, logsDir string) {
	dirs := GetDirectoriesFor(projectName)
	return dirs.Config, dirs.Data, dirs.Logs
}

func GetDirectoriesFor(projectName string) Directories {
	currentUser, _ := user.Current()
	home := ""
	if currentUser != nil {
		home = currentUser.HomeDir
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return defaultDirs(runtime.GOOS, projectName, isContainer(), isRootUser(), home)
}

func EnsureDirectories(dirs Directories) error {
	permission := os.FileMode(0o700)
	if isRootUser() {
		permission = 0o755
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Database, dirs.Cache, dirs.Logs, dirs.Backup} {
		if dir == "" {
			continue
		}
		if err := EnsureDirMode(dir, permission); err != nil {
			return err
		}
	}
	if dirs.PID != "" {
		if err := EnsureDirMode(filepath.Dir(dirs.PID), permission); err != nil {
			return err
		}
	}
	return nil
}

func EnsureDir(path string) error {
	permission := os.FileMode(0o700)
	if isRootUser() {
		permission = 0o755
	}
	return EnsureDirMode(path, permission)
}

func EnsureDirMode(path string, permission os.FileMode) error {
	if err := os.MkdirAll(path, permission); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.Chmod(path, permission); err != nil {
		return fmt.Errorf("set directory permissions: %w", err)
	}
	probe, err := os.CreateTemp(path, ".write-test-*")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close directory write check: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove directory write check: %w", err)
	}
	return nil
}

func IsRunningInContainer() bool {
	pid1 := processName(1)
	if isSystemManagerHost(os.Getenv("INVOCATION_ID"), os.Getenv("RC_SVCNAME"), pid1, pathExists("/run/openrc")) {
		return false
	}
	return detectContainerSignals(os.Getenv("container"), os.Getenv("KUBERNETES_SERVICE_HOST"), pid1, processName(os.Getppid()), readFile("/proc/1/cgroup"), pathExists)
}

func detectContainerSignals(containerEnv, kubernetesEnv, pid1, parentName, cgroup string, exists func(string) bool) bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv", "/dev/lxc"} {
		if exists(marker) {
			return true
		}
	}
	if strings.TrimSpace(containerEnv) != "" || strings.TrimSpace(kubernetesEnv) != "" {
		return true
	}
	containerInit := map[string]bool{"tini": true, "dumb-init": true, "s6-svscan": true, "runsv": true, "runsvdir": true, "catatonit": true}
	if containerInit[strings.ToLower(strings.TrimSpace(pid1))] || containerInit[strings.ToLower(strings.TrimSpace(parentName))] {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(parentName), ProjectName) {
		return true
	}
	cgroup = strings.ToLower(cgroup)
	for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "lxc"} {
		if strings.Contains(cgroup, marker) {
			return true
		}
	}
	return false
}

func isSystemManagerHost(invocationID, rcServiceName, pid1 string, openRCRunning bool) bool {
	pid1 = strings.TrimSpace(pid1)
	return strings.TrimSpace(invocationID) != "" || strings.TrimSpace(rcServiceName) != "" || pid1 == "systemd" || pid1 == "openrc-init" || pid1 == "init" && openRCRunning
}

func processName(pid int) string {
	if pid < 1 || runtime.GOOS == "windows" {
		return ""
	}
	return strings.TrimSpace(readFile(filepath.Join("/proc", fmt.Sprint(pid), "comm")))
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func GetBackupDir() string {
	return GetDirectories().Backup
}

func defaultBackupDir(dataDir string, root bool, home string) string {
	systemBackup := systemBackupDir()
	if parentWritable(systemBackup) {
		return systemBackup
	}
	if root {
		return filepath.Join(dataDir, "backup")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Backups", OrgName, ProjectName)
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Backups", OrgName, ProjectName)
	default:
		return filepath.Join(home, ".local", "share", "Backups", OrgName, ProjectName)
	}
}

func systemBackupDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join("/Library/Backups", OrgName, ProjectName)
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "Backups", OrgName, ProjectName)
	case "freebsd", "openbsd", "netbsd":
		return filepath.Join("/var/backups", OrgName, ProjectName)
	default:
		return filepath.Join("/mnt/Backups", OrgName, ProjectName)
	}
}

func parentWritable(path string) bool {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(parent, ".write-test-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}
