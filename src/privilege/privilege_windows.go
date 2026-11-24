//go:build windows

package privilege

import "golang.org/x/sys/windows"

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func DropPrivileges(int, int) error {
	return nil
}

func ChownTree([]string, int, int) error {
	return nil
}
