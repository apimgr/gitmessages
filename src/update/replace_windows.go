//go:build windows

package update

import "fmt"

func replaceAndRestart(executable, stagedPath string) error {
	return fmt.Errorf("safe in-place replacement of a running Windows executable is not available")
}
