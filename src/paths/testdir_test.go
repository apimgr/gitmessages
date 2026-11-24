package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func projectTestDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(os.TempDir(), OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(parent, ProjectName+"-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
