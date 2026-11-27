package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"

	"github.com/apimgr/gitmessages/src/paths"
)

func TestAuthorizeApplicationRestore(t *testing.T) {
	if err := authorizeApplicationRestore("", "", true, false, false, nil, nil); err != nil {
		t.Fatalf("first-run restore authorization failed: %v", err)
	}
	if err := authorizeApplicationRestore("secret", "secret", false, false, true, nil, nil); err != nil {
		t.Fatalf("service-user authorization failed: %v", err)
	}
	if err := authorizeApplicationRestore("secret", "wrong", false, false, true, nil, nil); err == nil {
		t.Fatal("service-user restore accepted an invalid operator token")
	}
	if err := authorizeApplicationRestore("secret", "secret", false, false, false, nil, nil); err == nil {
		t.Fatal("restore accepted a random user's token")
	}
	if err := authorizeApplicationRestore("", "", false, true, false, strings.NewReader("RESTORE\n"), &bytes.Buffer{}); err != nil {
		t.Fatalf("confirmed root restore authorization failed: %v", err)
	}
	if err := authorizeApplicationRestore("", "", false, true, false, strings.NewReader("no\n"), &bytes.Buffer{}); err == nil {
		t.Fatal("root restore accepted a declined confirmation")
	}
}

func TestConfirmDefaultRestore(t *testing.T) {
	var prompt bytes.Buffer
	if err := confirmDefaultRestore(strings.NewReader("RESTORE\n"), &prompt); err != nil {
		t.Fatalf("confirmed default restore failed: %v", err)
	}
	if prompt.Len() == 0 {
		t.Fatal("default restore confirmation did not prompt")
	}
	if err := confirmDefaultRestore(strings.NewReader("no\n"), &bytes.Buffer{}); err == nil {
		t.Fatal("default restore accepted a declined confirmation")
	}
	if err := confirmDefaultRestore(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("default restore accepted a missing confirmation input")
	}
}

func TestReplaceRestoreTargets(t *testing.T) {
	root := maintenanceTestDir(t)
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "value"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "value"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceRestoreTargets([]restoreTarget{{destination: destination, source: source}}, root+"-stage"); err != nil {
		t.Fatalf("replaceRestoreTargets() error: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "value"))
	if err != nil || string(contents) != "new" {
		t.Fatalf("replacement value = %q, %v; want new", contents, err)
	}
}

func TestRestoreApplicationBackupReplacesConfigDataAndDatabase(t *testing.T) {
	root := maintenanceTestDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backups")
	seedDBDir := filepath.Join(root, "seed-db")
	databaseDir := filepath.Join(root, "live-db")
	for _, directory := range []string{configDir, dataDir, backupDir, databaseDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "catalog.txt"), []byte("restored data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedStore, err := database.OpenSQLite(filepath.Join(seedDBDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, "restore-test.tar.gz")
	manifest, err := createApplicationBackup(context.Background(), seedStore, config.DefaultConfig(), configDir, dataDir, backupDir, backupPath, false, true)
	closeErr := seedStore.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create source backup: manifest=%+v err=%v close=%v", manifest, err, closeErr)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "catalog.txt"), []byte("old data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(databaseDir, "sentinel"), []byte("old database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreApplicationBackup(context.Background(), backupPath, configDir, dataDir, databaseDir, "", "127.0.0.1", "1", config.DefaultConfig().Server.Restore, ""); err != nil {
		t.Fatalf("restoreApplicationBackup() error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(configDir, "server.yml")); err != nil || string(got) != "server:\n  port: 8080\n" {
		t.Fatalf("restored server.yml = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dataDir, "catalog.txt")); err != nil || string(got) != "restored data\n" {
		t.Fatalf("restored data file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(databaseDir, "server.db")); err != nil {
		t.Fatalf("restored database is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(databaseDir, "sentinel")); !os.IsNotExist(err) {
		t.Fatalf("old database content remains after restore: %v", err)
	}
}

func TestApplicationBackupIncludesServerSSLOnlyWhenRequested(t *testing.T) {
	root := maintenanceTestDir(t)
	configDir := filepath.Join(root, "config")
	databaseDir := filepath.Join(root, "database")
	backupDir := filepath.Join(root, "backups")
	sslDir := filepath.Join(configDir, "ssl")
	for _, directory := range []string{configDir, databaseDir, backupDir, sslDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"server.crt": "certificate", "server.key": "private key"} {
		if err := os.WriteFile(filepath.Join(sslDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	configuration := config.DefaultConfig()
	configuration.Server.SSL.Cert = "ssl/server.crt"
	configuration.Server.SSL.Key = "ssl/server.key"
	for _, includeSSL := range []bool{false, true} {
		archivePath := filepath.Join(backupDir, fmt.Sprintf("backup-%t.tar.gz", includeSSL))
		manifest, err := createApplicationBackup(t.Context(), store, configuration, configDir, filepath.Join(root, "data"), backupDir, archivePath, includeSSL, false)
		if err != nil {
			t.Fatalf("createApplicationBackup(includeSSL=%t): %v", includeSSL, err)
		}
		found := map[string]bool{}
		for _, file := range manifest.Files {
			found[file.Path] = true
		}
		for _, path := range []string{"ssl/server.crt", "ssl/server.key"} {
			if found[path] != includeSSL {
				t.Errorf("includeSSL=%t archive contains %q: %t", includeSSL, path, found[path])
			}
		}
	}
}

func TestApplicationDatabaseEmpty(t *testing.T) {
	missingPath := filepath.Join(maintenanceTestDir(t), "missing", "server.db")
	empty, err := applicationDatabaseEmpty(missingPath)
	if err != nil || !empty {
		t.Fatalf("missing database empty = %t, %v; want true, nil", empty, err)
	}

	databasePath := filepath.Join(maintenanceTestDir(t), "server.db")
	store, err := database.OpenSQLite(databasePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	ownerStore, err := auth.NewStore(store, "", key, 5, 0, time.Hour)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, _, err := ownerStore.IssueOwner(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	empty, err = applicationDatabaseEmpty(databasePath)
	if err != nil || empty {
		t.Fatalf("populated database empty = %t, %v; want false, nil", empty, err)
	}
}

func maintenanceTestDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(parent, paths.ProjectName+"-maintenance-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
