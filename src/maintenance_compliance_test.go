package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
)

func TestExportOwnerDataOmitsCredentialMaterial(t *testing.T) {
	ctx := context.Background()
	store, authStore, identity, rawToken := maintenanceComplianceStore(t)
	destination := filepath.Join(maintenanceTestDir(t), "export.json")
	if err := exportOwnerData(ctx, store, authStore, identity.OwnerID, destination); err != nil {
		t.Fatalf("exportOwnerData() error = %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var exported ownerDataExport
	if err := json.Unmarshal(contents, &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if exported.Format != 1 || exported.Owner.ID != identity.OwnerID || len(exported.Tokens) != 1 {
		t.Fatalf("export metadata = %+v", exported)
	}
	if strings.Contains(string(contents), rawToken) || strings.Contains(string(contents), "token_hash") {
		t.Fatal("export contains token credential material")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDeleteOwnerDataAnonymizesAuditInTransaction(t *testing.T) {
	ctx := context.Background()
	store, _, identity, _ := maintenanceComplianceStore(t)
	if err := deleteOwnerData(ctx, store, identity.OwnerID, true); err != nil {
		t.Fatalf("deleteOwnerData() error = %v", err)
	}
	var owners int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id = ?`, identity.OwnerID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 0 {
		t.Fatalf("deleted owner row count = %d, want 0", owners)
	}
	var originalAuditReferences int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE target_id = ?`, identity.OwnerID).Scan(&originalAuditReferences); err != nil {
		t.Fatal(err)
	}
	if originalAuditReferences != 0 {
		t.Fatalf("audit references to original owner = %d, want 0", originalAuditReferences)
	}
	var completed int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'compliance.data_deletion_completed' AND target_id <> ?`, identity.OwnerID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("anonymized deletion completion events = %d, want 1", completed)
	}
}

func TestRunComplianceReportRecordsSummary(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := maintenanceComplianceStore(t)
	configuration := config.DefaultConfig()
	configuration.Server.Compliance.GDPR = true
	configuration.Server.Privacy.Retention.Period = "30d"
	if err := runComplianceReport(ctx, []string{"report"}, store, configuration); err != nil {
		t.Fatalf("runComplianceReport() error = %v", err)
	}
	var reports int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'compliance.report_generated'`).Scan(&reports); err != nil {
		t.Fatal(err)
	}
	if reports != 1 {
		t.Fatalf("report audit rows = %d, want 1", reports)
	}
}

func TestRunDataMaintenanceRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"export"},
		{"export", "--owner"},
		{"export", "--owner", "one", "--owner", "two"},
		{"export", "--unknown", "value"},
		{"export", "--owner", "one", "--yes"},
		{"delete", "--owner", "one", "--output", "file.json"},
		{"unknown", "--owner", "one"},
	} {
		if err := runDataMaintenance(context.Background(), args, nil, nil); err == nil {
			t.Errorf("runDataMaintenance(%q) unexpectedly succeeded", args)
		}
	}
}

func TestRunDataMaintenanceExportsAndDeletesOwner(t *testing.T) {
	ctx := context.Background()
	store, authStore, identity, _ := maintenanceComplianceStore(t)
	tokens, err := authStore.List(ctx, identity.OwnerID)
	if err != nil || len(tokens) == 0 {
		t.Fatalf("list owner tokens = %v, %v", tokens, err)
	}
	ownerPrefix := tokens[0].Prefix
	exportPath := filepath.Join(maintenanceTestDir(t), "owner-export.json")
	if err := runDataMaintenance(ctx, []string{"export", "--owner", ownerPrefix, "--output", exportPath}, store, authStore); err != nil {
		t.Fatalf("runDataMaintenance(export) error = %v", err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("owner export was not written: %v", err)
	}
	if err := runDataMaintenance(ctx, []string{"delete", "--owner", ownerPrefix, "--yes"}, store, authStore); err != nil {
		t.Fatalf("runDataMaintenance(delete) error = %v", err)
	}
	var count int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id = ?`, identity.OwnerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("remaining owners = %d, want 0", count)
	}
}

func maintenanceComplianceStore(t *testing.T) (*database.Store, *auth.Store, auth.Identity, string) {
	t.Helper()
	directory := maintenanceTestDir(t)
	store, err := database.OpenSQLite(filepath.Join(directory, "server.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	authStore, err := auth.NewStore(store, "", key, 5, 0, 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rawToken, identity, err := authStore.IssueOwner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := authStore.List(context.Background(), identity.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("created tokens = %d, want 1", len(tokens))
	}
	return store, authStore, identity, rawToken
}
