//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func replaceAndRestart(executable, stagedPath string) error {
	backupFile, err := os.CreateTemp(filepath.Dir(executable), ".gitmessages-previous-*")
	if err != nil {
		return err
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	if err := os.Rename(executable, backupPath); err != nil {
		return err
	}
	restorePrevious := func(cause error) error {
		_ = os.Remove(executable)
		if restoreErr := os.Rename(backupPath, executable); restoreErr != nil {
			return fmt.Errorf("%w; restore previous executable: %v", cause, restoreErr)
		}
		return cause
	}
	if err := os.Rename(stagedPath, executable); err != nil {
		return restorePrevious(err)
	}
	arguments := stripUpdateArguments(os.Args)
	if err := syscall.Exec(executable, arguments, os.Environ()); err != nil {
		return restorePrevious(err)
	}
	return nil
}

func stripUpdateArguments(arguments []string) []string {
	result := []string{arguments[0]}
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--update" {
			if index+1 < len(arguments) && (arguments[index+1] == "yes" || arguments[index+1] == "check" || arguments[index+1] == "branch") {
				index++
				if arguments[index] == "branch" && index+1 < len(arguments) {
					index++
				}
			}
			continue
		}
		if strings.HasPrefix(argument, "--update=") {
			continue
		}
		if argument == "--maintenance" && index+1 < len(arguments) && arguments[index+1] == "update" {
			index++
			continue
		}
		result = append(result, argument)
	}
	return result
}
