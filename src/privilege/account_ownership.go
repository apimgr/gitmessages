package privilege

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const accountOwnershipContents = "gitmessages service account created by this application\n"

func writeAccountOwnershipMarker(homeDir string, groupCreated bool) error {
	directory := filepath.Join(homeDir, accountOwnershipDirectory)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		_ = os.Remove(directory)
		return err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode().Perm() != 0o700 || fileOwnerUID(directoryInfo) != os.Geteuid() {
		_ = os.Remove(directory)
		return errors.New("service account ownership directory is not private and owned by the current elevated user")
	}
	path := filepath.Join(directory, accountOwnershipMarker)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.Remove(directory)
		return err
	}
	contents := accountOwnershipContents + "group-created=" + fmt.Sprint(groupCreated) + "\n"
	_, writeErr := io.WriteString(file, contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		_ = os.Remove(directory)
		return writeErr
	}
	if syncErr != nil {
		_ = os.Remove(path)
		_ = os.Remove(directory)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		_ = os.Remove(directory)
		return closeErr
	}
	return nil
}

func accountDeletionCommands(goos string, groupCreated bool) ([][]string, error) {
	groupCommands := map[string][]string{
		"linux":   {"groupdel", serviceName},
		"darwin":  {"dscl", ".", "-delete", "/Groups/" + serviceName},
		"freebsd": {"pw", "groupdel", "-n", serviceName},
		"openbsd": {"groupdel", serviceName},
		"netbsd":  {"groupdel", serviceName},
	}
	switch goos {
	case "linux":
		commands := [][]string{{"userdel", serviceName}}
		if groupCreated {
			commands = append(commands, groupCommands[goos])
		}
		return commands, nil
	case "darwin":
		commands := [][]string{{"dscl", ".", "-delete", "/Users/" + serviceName}}
		if groupCreated {
			commands = append(commands, groupCommands[goos])
		}
		return commands, nil
	case "freebsd":
		commands := [][]string{{"pw", "userdel", "-n", serviceName}}
		if groupCreated {
			commands = append(commands, groupCommands[goos])
		}
		return commands, nil
	case "openbsd", "netbsd":
		commands := [][]string{{"userdel", serviceName}}
		if groupCreated {
			commands = append(commands, groupCommands[goos])
		}
		return commands, nil
	default:
		return nil, fmt.Errorf("service account deletion is unsupported on %s", goos)
	}
}

func deleteServiceAccount(goos, homeDir string, run commandRunner) error {
	if homeDir == "" {
		return errors.New("service account home directory is required")
	}
	ownershipDir := filepath.Join(homeDir, accountOwnershipDirectory)
	directoryInfo, err := os.Lstat(ownershipDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect service account ownership directory: %w", err)
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode().Perm() != 0o700 || fileOwnerUID(directoryInfo) != os.Geteuid() {
		return errors.New("service account ownership directory is not private and owned by the current elevated user")
	}
	markerPath := filepath.Join(ownershipDir, accountOwnershipMarker)
	markerInfo, err := os.Lstat(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect service account ownership marker: %w", err)
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0o600 || fileOwnerUID(markerInfo) != os.Geteuid() {
		return errors.New("service account ownership marker is not a private regular file")
	}
	marker, err := os.Open(markerPath)
	if err != nil {
		return fmt.Errorf("open service account ownership marker: %w", err)
	}
	openedInfo, statErr := marker.Stat()
	contents, readErr := io.ReadAll(marker)
	closeErr := marker.Close()
	if statErr != nil {
		return fmt.Errorf("stat service account ownership marker: %w", statErr)
	}
	if !os.SameFile(markerInfo, openedInfo) {
		return errors.New("service account ownership marker changed during inspection")
	}
	if readErr != nil {
		return fmt.Errorf("read service account ownership marker: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close service account ownership marker: %w", closeErr)
	}
	fields := strings.Split(string(contents), "\n")
	if len(fields) != 3 || fields[0] != "gitmessages service account created by this application" || fields[2] != "" {
		return errors.New("service account ownership marker has unexpected contents")
	}
	var groupCreated bool
	switch fields[1] {
	case "group-created=true":
		groupCreated = true
	case "group-created=false":
	default:
		return errors.New("service account ownership marker has unexpected contents")
	}
	commands, err := accountDeletionCommands(goos, groupCreated)
	if err != nil {
		return err
	}
	for _, command := range commands {
		if err := run(command[0], command[1:]...); err != nil {
			var exitError interface{ ExitCode() int }
			if goos == "linux" && command[0] == "groupdel" && errors.As(err, &exitError) && exitError.ExitCode() == 6 {
				continue
			}
			return fmt.Errorf("delete service account %s: %w", serviceName, err)
		}
	}
	currentInfo, err := os.Lstat(markerPath)
	if err != nil {
		return fmt.Errorf("recheck service account ownership marker: %w", err)
	}
	if !os.SameFile(markerInfo, currentInfo) {
		return errors.New("service account ownership marker changed before removal")
	}
	if err := os.Remove(markerPath); err != nil {
		return fmt.Errorf("remove service account ownership marker: %w", err)
	}
	if err := os.Remove(ownershipDir); err != nil {
		return fmt.Errorf("remove service account ownership directory: %w", err)
	}
	return nil
}

func DeleteServiceAccount(homeDir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if homeDir == "" {
		return errors.New("service account home directory is required")
	}
	return deleteServiceAccount(runtime.GOOS, filepath.Clean(homeDir), runCommand)
}
