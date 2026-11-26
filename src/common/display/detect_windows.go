//go:build windows

package display

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func (environment *DisplayEnv) detectPlatformDisplay() {
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID); err != nil || sessionID == 0 {
		environment.DisplayType = "none"
		return
	}
	environment.HasDisplay = true
	if strings.HasPrefix(strings.ToLower(os.Getenv("SESSIONNAME")), "rdp-") {
		environment.DisplayType = "windows-rdp"
		return
	}
	environment.DisplayType = "windows"
}
