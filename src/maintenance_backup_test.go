package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/apimgr/gitmessages/src/database"
)

func TestRecordBackupEventStoresHistoryAndAudit(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(maintenanceTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := recordBackupEvent(context.Background(), store, "backup.created", "/backups/daily.tar.gz", 4096, true, true, "verified"); err != nil {
		t.Fatal(err)
	}
	var action, filename, details string
	var size, encrypted, success int
	if err := store.Read().QueryRow(`SELECT action,filename,size,encrypted,success,details FROM backups`).Scan(&action, &filename, &size, &encrypted, &success, &details); err != nil {
		t.Fatalf("read backup history: %v", err)
	}
	if action != "backup.created" || filename != "daily.tar.gz" || size != 4096 || encrypted != 1 || success != 1 || details != "verified" {
		t.Fatalf("backup history = %q, %q, %d, %d, %d, %q", action, filename, size, encrypted, success, details)
	}
	var auditCount int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category='backup' AND action='backup.created'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("backup audit count = %d, %v", auditCount, err)
	}
}
