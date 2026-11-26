//go:build freebsd || netbsd || openbsd

package gui

import "errors"

func IsGUIAvailable() bool { return false }

func LaunchGUI(*Config) error {
	return errors.New("GUI mode is not supported on BSD platforms")
}
