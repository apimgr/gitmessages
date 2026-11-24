package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/gitmessages/src/paths"
)

func clientTestDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(parent, paths.ProjectName+"-client-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
