package paths

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureStartupDirectories creates the paths needed before configuration is read.
func EnsureStartupDirectories(dirs Directories, systemMode bool) error {
	generalMode := os.FileMode(0o700)
	if systemMode {
		generalMode = 0o755
	}
	for _, directory := range []string{dirs.Config, dirs.Data, dirs.Database, dirs.Cache, dirs.Logs, dirs.Backup} {
		if directory == "" {
			continue
		}
		if err := EnsureDirMode(directory, generalMode); err != nil {
			return fmt.Errorf("ensure startup directory %s: %w", directory, err)
		}
	}
	if dirs.PID != "" {
		if err := EnsureDirMode(filepath.Dir(dirs.PID), generalMode); err != nil {
			return fmt.Errorf("ensure PID directory: %w", err)
		}
	}
	for _, directory := range privateDirectories(dirs) {
		if directory == "" {
			continue
		}
		if err := EnsureDirMode(directory, 0o700); err != nil {
			return fmt.Errorf("ensure private directory %s: %w", directory, err)
		}
	}
	return EnforcePrivatePaths(dirs)
}

// EnforcePrivatePaths fixes existing private trees before configuration is read.
func EnforcePrivatePaths(dirs Directories) error {
	for _, path := range append(privateDirectories(dirs), filepath.Join(dirs.Config, "server.yml"), filepath.Join(dirs.Config, "server.yaml"), filepath.Join(dirs.Config, "secrets.yml")) {
		if path == "" {
			continue
		}
		if err := enforcePrivatePath(path); err != nil {
			return fmt.Errorf("enforce private path %s: %w", path, err)
		}
	}
	return nil
}

func privateDirectories(dirs Directories) []string {
	return []string{
		filepath.Join(dirs.Config, "yaml.d"),
		filepath.Join(dirs.Config, "assets"),
		filepath.Join(dirs.Config, "ssl"),
		filepath.Join(dirs.Config, "tor"),
		filepath.Join(dirs.Config, "i2p"),
		filepath.Join(dirs.Data, "security"),
		filepath.Join(dirs.Data, "tor"),
		filepath.Join(dirs.Data, "tor", "site"),
	}
}

func enforcePrivatePath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private path is a symbolic link")
	}
	if info.IsDir() {
		if err := enforcePrivateMode(path, 0o700); err != nil {
			return err
		}
		return filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("private tree contains a symbolic link")
			}
			mode := os.FileMode(0o600)
			if entry.IsDir() {
				mode = 0o700
			}
			return enforcePrivateMode(current, mode)
		})
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private path is not a regular file or directory")
	}
	return enforcePrivateMode(path, 0o600)
}
