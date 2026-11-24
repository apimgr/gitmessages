//go:build windows

package main

import "fmt"

func daemonizeProcess() error {
	fmt.Println("Warning: --daemon is not supported on Windows")
	fmt.Println("Use --service --install and --service start for Windows Service")
	return nil
}
