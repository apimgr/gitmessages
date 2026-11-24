//go:build windows

package paths

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const stillActiveExitCode uint32 = 259

func isProcessRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	return windows.GetExitCodeProcess(handle, &exitCode) == nil && exitCode == stillActiveExitCode
}

func isOurProcess(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return false
	}
	name := filepath.Base(windows.UTF16ToString(buffer[:size]))
	return strings.EqualFold(name, ProjectName+".exe") || strings.EqualFold(name, ProjectName)
}
