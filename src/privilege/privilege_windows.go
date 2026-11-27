//go:build windows

package privilege

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func CanEscalate() bool {
	return !IsElevated()
}

func ExecElevated(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("elevated command is empty")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for elevation: %w", err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory for elevation: %w", err)
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	arguments, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args[1:]))
	if err != nil {
		return err
	}
	directory, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, arguments, directory, windows.SW_NORMAL)
}

func DropPrivileges(int, int) error {
	return nil
}

func ChownTree([]string, int, int) error {
	return nil
}
