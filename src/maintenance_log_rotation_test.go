package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestRotateLogFilesCompressesAndRetainsConfiguredArchive(t *testing.T) {
	logDir := maintenanceTestDir(t)
	logPath := filepath.Join(logDir, "access.log")
	if err := os.WriteFile(logPath, []byte("previous log entries\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	configuration := config.LoggingConfig{
		Defaults: config.LogDefaultsConfig{Rotate: "never", Keep: "none"},
		Access:   config.LogKindConfig{Rotate: "1B", Keep: "1", Compress: true},
	}
	if err := rotateLogFiles(logDir, configuration, now); err != nil {
		t.Fatalf("rotateLogFiles() error: %v", err)
	}
	current, err := os.ReadFile(logPath)
	if err != nil || len(current) != 0 {
		t.Fatalf("current log = %q, %v; want empty", current, err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("log directory entries = %d, want current log and one archive", len(entries))
	}
	archivePath := filepath.Join(logDir, entries[0].Name())
	if entries[0].Name() == "access.log" {
		archivePath = filepath.Join(logDir, entries[1].Name())
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "previous log entries\n" {
		t.Fatalf("decompressed log = %q", contents)
	}
}

func TestLogRotationPrunesArchivesWhenRetentionIsNone(t *testing.T) {
	logDir := maintenanceTestDir(t)
	logPath := filepath.Join(logDir, "server.log")
	if err := os.WriteFile(logPath, []byte("current entries"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldArchive := filepath.Join(logDir, "server.log.20261121T000000Z")
	if err := os.WriteFile(oldArchive, []byte("old entries"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rotateOneLog(logPath, "none", false, time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("rotateOneLog() error: %v", err)
	}
	if _, err := os.Stat(oldArchive); !os.IsNotExist(err) {
		t.Fatalf("old archive was not removed for keep=none: %v", err)
	}
	if err := pruneRotatedLogs(logDir, "server.log", "0", time.Now()); err != nil {
		t.Fatalf("pruneRotatedLogs() with no archives error: %v", err)
	}
}

func TestLogRotationPoliciesAndRetentionCutoffs(t *testing.T) {
	path := filepath.Join(maintenanceTestDir(t), "test.log")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !logRotationDue("weekly,1B", info, time.Now()) {
		t.Fatal("size policy 1B did not trigger rotation")
	}
	if logRotationDue("never,50MB", info, time.Now()) {
		t.Fatal("never policy rotated a small log")
	}
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	for policy, want := range map[string]time.Time{
		"4w":        now.AddDate(0, 0, -28),
		"weekly:4":  now.AddDate(0, 0, -28),
		"3m":        now.AddDate(0, -3, 0),
		"monthly:3": now.AddDate(0, -3, 0),
	} {
		got, ok := logRetentionCutoff(policy, now)
		if !ok || !got.Equal(want) {
			t.Errorf("logRetentionCutoff(%q) = %v, %t; want %v", policy, got, ok, want)
		}
	}
}
