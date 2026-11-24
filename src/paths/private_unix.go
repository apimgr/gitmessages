//go:build !windows

package paths

import (
	"fmt"
	"os"
	"os/user"
	"reflect"
	"strconv"
)

func ensurePrivateTempParent(parent string) error {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("temporary parent is not a real directory")
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	uidField := value.FieldByName("Uid")
	if !uidField.IsValid() {
		return fmt.Errorf("cannot verify temporary parent owner")
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("identify temporary parent owner: %w", err)
	}
	currentUID, err := strconv.ParseUint(current.Uid, 10, 64)
	if err != nil || uidField.Uint() != currentUID {
		return fmt.Errorf("temporary parent is not owned by the current user")
	}
	return os.Chmod(parent, 0o700)
}

func enforcePrivateMode(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}

func verifyPrivateMode(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("mode is %04o, want %04o", info.Mode().Perm(), mode.Perm())
	}
	return nil
}
