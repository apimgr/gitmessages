//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func daemonizeProcess() error {
	if os.Getenv("_DAEMON_CHILD") != "" || os.Getppid() == 1 {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	arguments := make([]string, 0, len(os.Args)-1)
	for _, argument := range os.Args[1:] {
		if argument != "--daemon" {
			arguments = append(arguments, argument)
		}
	}
	command := exec.Command(executable, arguments...)
	command.Env = append(os.Environ(), "_DAEMON_CHILD=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start detached process: %w", err)
	}
	fmt.Printf("Daemon started with PID %d\n", command.Process.Pid)
	_ = command.Process.Release()
	os.Exit(0)
	return nil
}
