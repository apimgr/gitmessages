package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CheckPIDFile reports whether a live process using this binary owns the PID file.
func CheckPIDFile(pidPath string) (bool, int, error) {
	if isContainer() {
		return false, 0, nil
	}
	return checkPIDFile(pidPath, isProcessRunning, isOurProcess)
}

func checkPIDFile(pidPath string, processRunning, processOwned func(int) bool) (bool, int, error) {
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("read PID file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		if removeErr := os.Remove(pidPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return false, 0, fmt.Errorf("remove invalid PID file: %w", removeErr)
		}
		return false, 0, nil
	}
	if !processRunning(pid) || !processOwned(pid) {
		if removeErr := os.Remove(pidPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return false, 0, fmt.Errorf("remove stale PID file: %w", removeErr)
		}
		return false, 0, nil
	}
	return true, pid, nil
}

// WritePIDFile creates a PID file, skipping it inside containers.
func WritePIDFile(pidPath string) error {
	permission := os.FileMode(0o600)
	if isRootUser() {
		permission = 0o644
	}
	return WritePIDFileMode(pidPath, permission)
}

// WritePIDFileMode writes the current process ID with a startup-locked mode.
func WritePIDFileMode(pidPath string, permission os.FileMode) error {
	if isContainer() || pidPath == "" {
		return nil
	}
	running, pid, err := CheckPIDFile(pidPath)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("already running (pid %d)", pid)
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return fmt.Errorf("create PID directory: %w", err)
	}
	file, err := os.OpenFile(pidPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, permission)
	if err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		return fmt.Errorf("write PID: %w", err)
	}
	if err := file.Chmod(permission); err != nil {
		_ = file.Close()
		return fmt.Errorf("set PID file permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close PID file: %w", err)
	}
	return nil
}

// RemovePIDFile removes a PID file and accepts an already-removed file.
func RemovePIDFile(pidPath string) error {
	if pidPath == "" {
		return nil
	}
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove PID file: %w", err)
	}
	return nil
}
