//go:build !gui && !freebsd && !netbsd && !openbsd

package gui

import "errors"

func IsGUIAvailable() bool { return false }

func LaunchGUI(*Config) error { return errors.New("GUI mode is not present in this CLI build") }
