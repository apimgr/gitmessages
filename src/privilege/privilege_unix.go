//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func IsElevated() bool {
	return os.Geteuid() == 0
}

func DropPrivileges(uid, gid int) error {
	return dropPrivilegesWith(uid, gid, syscall.Setgroups, syscall.Setgid, syscall.Setuid, os.Getuid, os.Geteuid, os.Getgid)
}

func dropPrivilegesWith(uid, gid int, setgroups func([]int) error, setgid, setuid func(int) error, getuid, geteuid, getgid func() int) error {
	if uid < 0 || gid < 0 || uid == 0 {
		return fmt.Errorf("invalid service UID/GID %d:%d", uid, gid)
	}
	if err := setgroups([]int{gid}); err != nil {
		return fmt.Errorf("set supplementary service group: %w", err)
	}
	if err := setgid(gid); err != nil {
		return fmt.Errorf("set service group: %w", err)
	}
	if err := setuid(uid); err != nil {
		return fmt.Errorf("set service user: %w", err)
	}
	if getuid() == 0 || geteuid() == 0 {
		return fmt.Errorf("privilege drop failed: process still has root identity")
	}
	if getuid() != uid || geteuid() != uid || getgid() != gid {
		return fmt.Errorf("privilege drop verification failed for service UID/GID %d:%d", uid, gid)
	}
	return nil
}

func ChownTree(paths []string, uid, gid int) error {
	return chownTree(paths, os.Chown, uid, gid)
}

func chownTree(paths []string, chown func(string, int, int) error, uid, gid int) error {
	for _, root := range paths {
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() && filepath.Clean(path) == filepath.Join(filepath.Clean(root), accountOwnershipDirectory) {
				return fs.SkipDir
			}
			if err := chown(path, uid, gid); err != nil {
				return fmt.Errorf("change ownership of %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("change ownership below %s: %w", root, err)
		}
	}
	return nil
}
