package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/privilege"
)

type startupIdentity struct {
	system bool
	uid    int
	gid    int
}

func prepareStartupDirectories(dirs paths.Directories) (startupIdentity, error) {
	identity := startupIdentity{system: privilege.IsElevated()}
	if identity.system && runtime.GOOS != "windows" {
		if err := paths.EnsureDirMode(dirs.Data, 0o755); err != nil {
			return identity, fmt.Errorf("create service account home directory: %w", err)
		}
		uid, gid, err := privilege.EnsureServiceAccount(dirs.Data)
		if err != nil {
			return identity, err
		}
		identity.uid, identity.gid = uid, gid
	}
	if err := paths.EnsureStartupDirectories(dirs, identity.system); err != nil {
		return identity, err
	}
	if identity.system && runtime.GOOS != "windows" {
		ownedPaths := []string{dirs.Config, dirs.Data, dirs.Database, dirs.Cache, dirs.Logs, dirs.Backup}
		if dirs.PID != "" {
			ownedPaths = append(ownedPaths, filepath.Dir(dirs.PID))
		}
		if err := privilege.ChownTree(ownedPaths, identity.uid, identity.gid); err != nil {
			return identity, err
		}
	}
	if err := paths.EnforcePrivatePaths(dirs); err != nil {
		return identity, err
	}
	return identity, nil
}

func dropToServiceAccount(identity startupIdentity, dataDir string) error {
	if !identity.system || runtime.GOOS == "windows" {
		return nil
	}
	if err := privilege.DropPrivileges(identity.uid, identity.gid); err != nil {
		return err
	}
	if err := os.Setenv("HOME", dataDir); err != nil {
		return fmt.Errorf("set service HOME: %w", err)
	}
	return nil
}
