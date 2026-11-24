//go:build !gui && !freebsd && !netbsd && !openbsd

package main

import "errors"

func guiSupported() bool { return false }

func runGUI(string, string, *clientConfig) error {
	return errors.New("GUI mode is not present in this CLI build")
}
