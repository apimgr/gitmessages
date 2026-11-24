package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/paths"
)

func TestCreateVerifyAndRestore(t *testing.T) {
	root := backupTestDir(t)
	source := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(source, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "server.yml"), []byte("server:\n  port: 64000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "yaml.d", "web.yml"), []byte("web:\n  baseurl: /\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inputs, err := InputsFromPath(source, "config")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "backup.tar.gz")
	wantSnapshot := time.Date(2026, 11, 22, 2, 0, 0, 0, time.UTC)
	created, err := Create(context.Background(), CreateOptions{Destination: archivePath, Inputs: inputs, AppVersion: "1.2.3", SnapshotAt: wantSnapshot})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.Type != "full" || created.SnapshotAt != wantSnapshot || created.Encrypted {
		t.Fatalf("Create() manifest = %#v", created)
	}
	verified, err := Verify(context.Background(), archivePath, "", DefaultRestoreLimits())
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if len(verified.Files) != 2 || verified.Checksum == "" {
		t.Fatalf("Verify() manifest = %#v", verified)
	}
	destination := filepath.Join(root, "restored")
	if _, err := Restore(context.Background(), archivePath, "", destination, DefaultRestoreLimits()); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(destination, "config", "server.yml"):        "server:\n  port: 64000\n",
		filepath.Join(destination, "config", "yaml.d", "web.yml"): "web:\n  baseurl: /\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestEncryptedBackupRequiresPasswordAndRejectsWrongPassword(t *testing.T) {
	root := backupTestDir(t)
	source := filepath.Join(root, "server.yml")
	if err := os.WriteFile(source, []byte("private: value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "backup.tar.gz.enc")
	_, err := Create(context.Background(), CreateOptions{
		Destination: archivePath,
		Inputs:      []Input{{Path: source, ArchivePath: "server.yml"}},
		Password:    "correct horse battery staple",
		Compliance:  true,
	})
	if err != nil {
		t.Fatalf("Create() encrypted error: %v", err)
	}
	if _, err := Verify(context.Background(), archivePath, "", DefaultRestoreLimits()); err == nil || !strings.Contains(err.Error(), "requires a password") {
		t.Fatalf("Verify() without password error = %v", err)
	}
	if _, err := Verify(context.Background(), archivePath, "incorrect password value", DefaultRestoreLimits()); err == nil || !strings.Contains(err.Error(), "invalid backup password") {
		t.Fatalf("Verify() with wrong password error = %v", err)
	}
	verified, err := Verify(context.Background(), archivePath, "correct horse battery staple", DefaultRestoreLimits())
	if err != nil || !verified.Encrypted || verified.EncryptionMethod != "AES-256-GCM" {
		t.Fatalf("Verify() encrypted manifest = %#v, %v", verified, err)
	}
}

func TestIncrementalBackupVerifiesBaseAndRestoresCumulativeState(t *testing.T) {
	root := backupTestDir(t)
	baseSource := filepath.Join(root, "base")
	if err := os.MkdirAll(baseSource, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"keep.txt": "old\n", "delete.txt": "remove\n"} {
		if err := os.WriteFile(filepath.Join(baseSource, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	basePath := filepath.Join(root, "full.tar.gz")
	baseInputs, err := InputsFromPath(baseSource, "data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), CreateOptions{Destination: basePath, Inputs: baseInputs}); err != nil {
		t.Fatal(err)
	}
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	baseSum := sha256.Sum256(baseData)
	changed := filepath.Join(root, "changed.txt")
	if err := os.WriteFile(changed, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	incrementalPath := filepath.Join(root, "daily.tar.gz")
	_, err = Create(context.Background(), CreateOptions{
		Destination: incrementalPath,
		Inputs:      []Input{{Path: changed, ArchivePath: "data/keep.txt"}},
		Type:        "incremental",
		BaseBackup:  filepath.Base(basePath),
		BaseSHA256:  hex.EncodeToString(baseSum[:]),
		Deleted:     []string{"data/delete.txt"},
	})
	if err != nil {
		t.Fatalf("Create() incremental error: %v", err)
	}
	if _, err := Verify(context.Background(), incrementalPath, "", DefaultRestoreLimits()); err != nil {
		t.Fatalf("Verify() incremental error: %v", err)
	}
	destination := filepath.Join(root, "restored")
	if _, err := Restore(context.Background(), incrementalPath, "", destination, DefaultRestoreLimits()); err != nil {
		t.Fatalf("Restore() incremental error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "data", "keep.txt")); err != nil || string(got) != "new\n" {
		t.Fatalf("restored changed file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "data", "delete.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file remains after restore: %v", err)
	}
}

func TestRestoreRejectsExistingDestinationAndConfiguredLimits(t *testing.T) {
	root := backupTestDir(t)
	source := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "backup.tar.gz")
	if _, err := Create(context.Background(), CreateOptions{Destination: archivePath, Inputs: []Input{{Path: source, ArchivePath: "payload.txt"}}}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "exists")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), archivePath, "", destination, DefaultRestoreLimits()); err == nil {
		t.Fatal("Restore() accepted an existing destination")
	}
	limits := DefaultRestoreLimits()
	limits.MaxExpandedSize = 1
	if _, err := Verify(context.Background(), archivePath, "", limits); err == nil {
		t.Fatal("Verify() accepted an archive over the expanded-size limit")
	}
}

func backupTestDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := os.MkdirTemp(parent, paths.ProjectName+"-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}
