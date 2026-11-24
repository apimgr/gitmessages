//go:build freebsd || netbsd || openbsd

package main

import "errors"

func guiSupported() bool { return false }

func runGUI(string, string, *clientConfig) error {
	return errors.New("GUI mode is not supported on BSD platforms")
}
