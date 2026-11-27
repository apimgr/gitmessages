package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestNewestBackupFileSelectionUsesFullDateAndIncrementalModificationTime(t *testing.T) {
	backupDir := filepath.Join(maintenanceTestDir(t), "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	fullOlder := filepath.Join(backupDir, paths.ProjectName+"_backup_2025-01-01.tar.gz.enc")
	fullNewest := filepath.Join(backupDir, paths.ProjectName+"_backup_2025-01-02.tar.gz.enc")
	for _, path := range []string{fullOlder, fullNewest} {
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fullOlder, baseTime.Add(2*time.Hour), baseTime.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fullNewest, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}
	selected, err := newestBackupFile(backupDir)
	if err != nil || selected != fullNewest {
		t.Fatalf("newest full selection = %q, %v; want %q by filename date", selected, err, fullNewest)
	}
	if err := os.Remove(fullOlder); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fullNewest); err != nil {
		t.Fatal(err)
	}
	daily := filepath.Join(backupDir, paths.ProjectName+"-daily.tar.gz.enc")
	hourly := filepath.Join(backupDir, paths.ProjectName+"-hourly.tar.gz.enc")
	for _, path := range []string{daily, hourly} {
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(daily, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(hourly, baseTime.Add(time.Hour), baseTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	selected, err = newestBackupFile(backupDir)
	if err != nil || selected != hourly {
		t.Fatalf("newest incremental selection = %q, %v; want %q by modification time", selected, err, hourly)
	}
	full := filepath.Join(backupDir, paths.ProjectName+"_backup_2025-01-01.tar.gz.enc")
	if err := os.WriteFile(full, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(full, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}
	selected, err = newestBackupFile(backupDir)
	if err != nil || selected != hourly {
		t.Fatalf("newest archive with incremental = %q, %v; want %q by date and modification time", selected, err, hourly)
	}
	selected, err = newestFullBackupFile(backupDir)
	if err != nil || selected != full {
		t.Fatalf("newest full archive = %q, %v; want %q", selected, err, full)
	}
	newerFull := filepath.Join(backupDir, paths.ProjectName+"_backup_2025-01-02.tar.gz.enc")
	if err := os.WriteFile(newerFull, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newerFull, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}
	selected, err = newestBackupFile(backupDir)
	if err != nil || selected != newerFull {
		t.Fatalf("newest archive with later full date = %q, %v; want %q", selected, err, newerFull)
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

func TestBackupRetentionCategoriesAreExclusive(t *testing.T) {
	fulls := []scheduledFull{
		{path: "yearly", manifest: backuppkg.Manifest{SnapshotAt: time.Date(2023, time.January, 1, 2, 0, 0, 0, time.UTC)}},
		{path: "monthly", manifest: backuppkg.Manifest{SnapshotAt: time.Date(2023, time.February, 1, 2, 0, 0, 0, time.UTC)}},
		{path: "weekly", manifest: backuppkg.Manifest{SnapshotAt: time.Date(2023, time.January, 8, 2, 0, 0, 0, time.UTC)}},
		{path: "daily", manifest: backuppkg.Manifest{SnapshotAt: time.Date(2023, time.January, 2, 2, 0, 0, 0, time.UTC)}},
	}
	policy := config.BackupRetentionConfig{MaxBackups: 1, KeepWeekly: 1, KeepMonthly: 1, KeepYearly: 1}
	kept := backupRetentionSet(fulls, policy)
	if len(kept) != 4 {
		t.Fatalf("retained paths = %#v, want one backup per exclusive category", kept)
	}
	for _, path := range []string{"yearly", "monthly", "weekly", "daily"} {
		if _, ok := kept[path]; !ok {
			t.Errorf("retention did not keep %q", path)
		}
	}
	fulls = append(fulls, scheduledFull{path: "older-yearly", manifest: backuppkg.Manifest{SnapshotAt: time.Date(2022, time.January, 1, 2, 0, 0, 0, time.UTC)}})
	kept = backupRetentionSet(fulls, policy)
	if _, ok := kept["older-yearly"]; ok {
		t.Fatal("older January 1 backup exceeded the yearly retention count")
	}
}

func TestBackupSizeLimitPreservesIncrementalBasePair(t *testing.T) {
	root := maintenanceTestDir(t)
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(root, paths.ProjectName+"_backup_2026-01-01.tar.gz")
	if _, err := backuppkg.Create(context.Background(), backuppkg.CreateOptions{
		Destination: basePath,
		Inputs:      []backuppkg.Input{{Path: source, ArchivePath: "snapshot.txt"}},
	}); err != nil {
		t.Fatalf("create base backup: %v", err)
	}
	baseHash, err := hashBackupFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	incrementPath := filepath.Join(root, paths.ProjectName+"-daily.tar.gz")
	if _, err := backuppkg.Create(context.Background(), backuppkg.CreateOptions{
		Destination: incrementPath,
		Inputs:      []backuppkg.Input{{Path: source, ArchivePath: "changed.txt"}},
		Type:        "incremental",
		BaseBackup:  filepath.Base(basePath),
		BaseSHA256:  baseHash,
	}); err != nil {
		t.Fatalf("create incremental backup: %v", err)
	}
	extraPath := filepath.Join(root, paths.ProjectName+"_unrelated.tar.gz")
	if err := os.WriteFile(extraPath, []byte("unverified archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(extraPath, old, old); err != nil {
		t.Fatal(err)
	}
	removed, err := enforceBackupSizeLimit(context.Background(), root, "1B", config.DefaultConfig())
	if err != nil {
		t.Fatalf("enforce backup size cap: %v", err)
	}
	if len(removed) != 1 || removed[0] != filepath.Base(extraPath) {
		t.Fatalf("size-cap removals = %#v, want only the unrelated archive", removed)
	}
	for _, path := range []string{basePath, incrementPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("retained backup pair member %s is missing: %v", filepath.Base(path), err)
		}
	}
}

func TestBackupArchiveSuffixIsExact(t *testing.T) {
	for _, name := range []string{"gitmessages_backup_2026-11-26.tar.gz", "gitmessages_backup_2026-11-26.tar.gz.enc"} {
		if !isBackupArchive(name) {
			t.Errorf("expected valid backup archive suffix for %q", name)
		}
	}
	for _, name := range []string{"gitmessages_backup_2026-11-26.tar.gz.tmp", "gitmessages.tar.gz.notes", "other_backup.tar.gz.enc.old"} {
		if isBackupArchive(name) {
			t.Errorf("accepted invalid backup archive suffix for %q", name)
		}
	}
}
