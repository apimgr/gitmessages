package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	backuppkg "github.com/apimgr/gitmessages/src/backup"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
)

func TestScheduledBackupCreatesCumulativeDailyIncremental(t *testing.T) {
	root := maintenanceTestDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(filepath.Join(configDir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "assets", "changed.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "assets", "removed.txt"), []byte("remove\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenSQLite(filepath.Join(root, "db", "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configuration := config.DefaultConfig()
	configuration.Server.Backup.DiskThreshold = 100
	configuration.Server.Backup.Retention.MaxTotalSize = "0"
	directories := paths.Directories{Config: configDir, Data: dataDir, Backup: backupDir}
	if err := runScheduledBackup(context.Background(), store, configuration, directories, false); err != nil {
		t.Fatalf("first scheduled backup: %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("first run archives = %d, %v; want one full archive", len(entries), err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "assets", "changed.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(configDir, "assets", "removed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := runScheduledBackup(context.Background(), store, configuration, directories, false); err != nil {
		t.Fatalf("second scheduled backup: %v", err)
	}
	incrementalPath := filepath.Join(backupDir, paths.ProjectName+"-daily.tar.gz")
	manifest, err := backuppkg.Verify(context.Background(), incrementalPath, "", backuppkg.DefaultRestoreLimits())
	if err != nil {
		t.Fatalf("verify daily incremental: %v", err)
	}
	if manifest.Type != "incremental" || len(manifest.Files) == 0 || len(manifest.Deleted) != 1 || manifest.Deleted[0] != "assets/removed.txt" {
		t.Fatalf("daily incremental manifest = %+v", manifest)
	}
	if _, err := verifyApplicationBackup(context.Background(), incrementalPath, backupDir, "", config.DefaultConfig().Server.Restore); err != nil {
		t.Fatalf("verify application incremental backup: %v", err)
	}
	if newest, err := newestBackupFile(backupDir); err != nil || newest == "" {
		t.Fatalf("newestBackupFile() = %q, %v", newest, err)
	}
	restored := filepath.Join(root, "restored")
	if _, err := backuppkg.Restore(context.Background(), incrementalPath, "", restored, backuppkg.DefaultRestoreLimits()); err != nil {
		t.Fatalf("restore full and incremental: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(restored, "assets", "changed.txt")); err != nil || string(got) != "after\n" {
		t.Fatalf("restored changed asset = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(restored, "assets", "removed.txt")); !os.IsNotExist(err) {
		t.Fatalf("removed asset remains after incremental restore: %v", err)
	}
}

func TestBackupSizeParsingAndHardRetentionLimit(t *testing.T) {
	for input, expected := range map[string]int64{"1": 1, "2K": 2048, "3M": 3 << 20, "4G": 4 << 30, "1T": 1 << 40, "50MB": 50 << 20, "1GB": 1 << 30, "2TB": 2 << 40} {
		got, err := parseBackupSize(input)
		if err != nil || got != expected {
			t.Errorf("parseBackupSize(%q) = %d, %v; want %d", input, got, err, expected)
		}
	}
	for _, input := range []string{"", "-1", "bad", "999999999999999999999999G"} {
		if _, err := parseBackupSize(input); err == nil {
			t.Errorf("parseBackupSize(%q) accepted invalid size", input)
		}
	}
	root := maintenanceTestDir(t)
	backupPath := filepath.Join(root, paths.ProjectName+"_test.tar.gz")
	if err := os.WriteFile(backupPath, []byte("archive-placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := config.DefaultConfig()
	removed, err := enforceBackupSizeLimit(context.Background(), root, "1B", configuration)
	if err != nil {
		t.Fatalf("enforceBackupSizeLimit() error: %v", err)
	}
	if len(removed) != 1 || removed[0] != filepath.Base(backupPath) {
		t.Fatalf("size-limit removals = %#v", removed)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("over-limit archive remains: %v", err)
	}
	if _, err := enforceBackupSizeLimit(context.Background(), root, "invalid", configuration); err == nil {
		t.Fatal("invalid retention size limit was accepted")
	}
}
