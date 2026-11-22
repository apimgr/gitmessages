//go:build !windows

package update

import (
	"os"
	"strings"
	"syscall"
)

func replaceAndRestart(executable, stagedPath string) error {
	if err := os.Rename(stagedPath, executable); err != nil {
		return err
	}
	arguments := stripUpdateArguments(os.Args)
	return syscall.Exec(executable, arguments, os.Environ())
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
