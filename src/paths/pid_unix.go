//go:build !windows

package paths

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isOurProcess(pid int) bool {
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err == nil {
		return filepath.Base(executable) == ProjectName
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	return err == nil && filepath.Base(strings.TrimSpace(string(output))) == ProjectName
}
