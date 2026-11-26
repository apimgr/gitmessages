//go:build !windows

package display

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func (environment *DisplayEnv) detectPlatformDisplay() {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		environment.HasDisplay = true
		environment.DisplayType = "wayland"
		return
	}
	if os.Getenv("DISPLAY") != "" {
		environment.HasDisplay = true
		environment.DisplayType = "x11"
		return
	}
	if runtime.GOOS == "darwin" && !environment.IsSSH {
		if os.Getenv("__CFBundleIdentifier") != "" {
			environment.HasDisplay = true
			environment.DisplayType = "macos"
			return
		}
		if output, err := exec.Command("launchctl", "managername").Output(); err == nil && strings.Contains(string(output), "Aqua") {
			environment.HasDisplay = true
			environment.DisplayType = "macos"
			return
		}
	}
	environment.DisplayType = "none"
}
