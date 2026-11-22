package paths

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

const (
	OrgName     = "apimgr"
	ProjectName = "gitmessages"
)

type Directories struct {
	Config string
	Data   string
	Cache  string
	Logs   string
	Backup string
	PID    string
}

var isContainer = IsRunningInContainer
var isRootUser = func() bool {
	if runtime.GOOS == "windows" {
		return os.Getenv("USERDOMAIN") == os.Getenv("COMPUTERNAME")
	}
	return os.Geteuid() == 0
}

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
	return defaultDirs(runtime.GOOS, ProjectName, isContainer(), isRootUser(), home)
}

func defaultDirs(goos, projectName string, container, root bool, home string) Directories {
	if container {
		base := filepath.Join("/data", projectName)
		return Directories{
			Config: filepath.Join("/config", projectName),
			Data:   base,
			Cache:  filepath.Join(base, "cache"),
			Logs:   filepath.Join("/data", "log", projectName),
			Backup: filepath.Join("/data", "backups", projectName),
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
			return Directories{Config: base, Data: filepath.Join(base, "data"), Cache: filepath.Join(base, "cache"), Logs: filepath.Join(base, "logs"), Backup: filepath.Join(programData, "Backups", OrgName, projectName), PID: filepath.Join(base, projectName+".pid")}
		case "darwin":
			base := filepath.Join("/Library/Application Support", OrgName, projectName)
			return Directories{Config: base, Data: filepath.Join(base, "data"), Cache: filepath.Join("/Library/Caches", OrgName, projectName), Logs: filepath.Join("/Library/Logs", OrgName, projectName), Backup: filepath.Join("/Library/Backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName+".pid")}
		case "freebsd", "openbsd", "netbsd":
			return Directories{Config: filepath.Join("/usr/local/etc", OrgName, projectName), Data: filepath.Join("/var/db", OrgName, projectName), Cache: filepath.Join("/var/cache", OrgName, projectName), Logs: filepath.Join("/var/log", OrgName, projectName), Backup: filepath.Join("/var/backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName+".pid")}
		default:
			return Directories{Config: filepath.Join("/etc", OrgName, projectName), Data: filepath.Join("/var/lib", OrgName, projectName), Cache: filepath.Join("/var/cache", OrgName, projectName), Logs: filepath.Join("/var/log", OrgName, projectName), Backup: filepath.Join("/mnt/Backups", OrgName, projectName), PID: filepath.Join("/var/run", OrgName, projectName+".pid")}
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
		return Directories{Config: filepath.Join(appData, OrgName, projectName), Data: base, Cache: filepath.Join(base, "cache"), Logs: filepath.Join(base, "logs"), Backup: filepath.Join(localAppData, "Backups", OrgName, projectName), PID: filepath.Join(base, projectName+".pid")}
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support", OrgName, projectName)
		return Directories{Config: base, Data: base, Cache: filepath.Join(home, "Library", "Caches", OrgName, projectName), Logs: filepath.Join(home, "Library", "Logs", OrgName, projectName), Backup: filepath.Join(home, "Library", "Backups", OrgName, projectName), PID: filepath.Join(base, projectName+".pid")}
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
		return Directories{Config: filepath.Join(configBase, OrgName, projectName), Data: data, Cache: filepath.Join(cacheBase, OrgName, projectName), Logs: filepath.Join(home, ".local", "log", OrgName, projectName), Backup: filepath.Join(dataBase, "Backups", OrgName, projectName), PID: filepath.Join(data, projectName+".pid")}
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
	permission := os.FileMode(0700)
	if isRootUser() {
		permission = 0755
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs} {
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
	permission := os.FileMode(0700)
	if isRootUser() {
		permission = 0755
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
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	return string(data) == "tini\n" || string(data) == "tini" || string(data) == "dumb-init\n"
}

func GetBackupDir() string {
	dirs := GetDirectories()
	if isContainer() || !isRootUser() {
		return dirs.Backup
	}
	systemBackup := filepath.Join("/mnt/Backups", OrgName, ProjectName)
	if directoryWritable(systemBackup) {
		return systemBackup
	}
	return filepath.Join(dirs.Data, "backup")
}

func directoryWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(path, ".write-test-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}
