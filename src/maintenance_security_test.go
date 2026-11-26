package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
)

func TestSecurityReportMaintenanceListExportAndDisclosure(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "security.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,credit_pref,credit_name,token_hash,created_at,updated_at) VALUES('track-1',1,'high','api','confirmed',?,'pgp','name','Researcher','hash','2026-01-01','2026-01-01')`, []byte("ciphertext"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := listSecurityReports(ctx, store, []string{"--status", "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if err := exportSecurityReport(ctx, store, []string{"track-1", "--output", filepath.Join(t.TempDir(), "report.asc")}); err != nil {
		t.Fatal(err)
	}
	if err := updateSecurityReport(ctx, store, []string{"track-1", "--status", "disclosed", "--comment", "Patch is available", "--disclosure-date", "2026-02-03", "--severity", "medium"}, t.TempDir(), &config.Config{}); err != nil {
		t.Fatal(err)
	}
	report, err := loadSecurityReport(ctx, store, "track-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "disclosed" || report.Severity != "medium" || report.ResearcherComment != "Patch is available" || !strings.HasPrefix(report.DisclosureDate.String, "2026-02-03") {
		t.Fatalf("disclosure metadata was not persisted: %+v", report)
	}
	var thanks string
	if err := store.Read().QueryRowContext(ctx, `SELECT credit FROM security_thanks WHERE tracking_id='track-1'`).Scan(&thanks); err != nil {
		t.Fatal(err)
	}
	if thanks != "Researcher" {
		t.Fatalf("unexpected hall-of-fame credit %q", thanks)
	}
	if err := updateSecurityReport(ctx, store, []string{"track-1", "--status", "patching"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("terminal disclosure status accepted a backward transition")
	}
}

func TestSecurityReportMaintenanceRejectsInvalidFiltersAndWritesPrivateCiphertext(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "security.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,token_hash) VALUES('track-2',1,'low','web','received',?,'aes-256-gcm','hash')`, []byte("encrypted-payload"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := listSecurityReports(ctx, store, []string{"--status", "invalid"}); err == nil {
		t.Fatal("invalid status filter was accepted")
	}
	path := filepath.Join(t.TempDir(), "track-2.enc")
	if err := exportSecurityReport(ctx, store, []string{"track-2", "--output", path}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "encrypted-payload" {
		t.Fatalf("export did not preserve ciphertext: %q", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("encrypted export mode = %o, want 600", info.Mode().Perm())
	}
	if err := updateSecurityReport(ctx, store, []string{"track-2", "--severity", "urgent"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("invalid severity was accepted")
	}
	if err := updateSecurityReport(ctx, store, []string{"track-2", "--disclosure-date", "tomorrow"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("invalid disclosure date was accepted")
	}
}

func TestSecurityReportCipherHelpersAndStatusTransitions(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	encrypted, err := encryptDataCiphertext(key, []byte("private report"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptDataCiphertext(key, encrypted)
	if err != nil || string(plain) != "private report" {
		t.Fatalf("cipher round trip failed: %q, %v", plain, err)
	}
	if _, err := decryptDataCiphertext(key, []byte("short")); err == nil {
		t.Fatal("short ciphertext was accepted")
	}
	if _, err := decryptDataCiphertext(make([]byte, 16), encrypted); err == nil {
		t.Fatal("invalid AES key length was accepted")
	}
	if !equalBytes(key, append([]byte(nil), key...)) || equalBytes(key, encrypted) {
		t.Fatal("constant-time byte equality returned an unexpected result")
	}
	if !forwardSecurityStatus("received", "disclosed") || !forwardSecurityStatus("triaged", "wont_fix") || forwardSecurityStatus("patching", "confirmed") || forwardSecurityStatus("disclosed", "wont_fix") {
		t.Fatal("security status transition policy is inconsistent")
	}
	if !validSecuritySeverity("informational") || validSecuritySeverity("urgent") || !validSecurityReportStatus("received") || validSecurityReportStatus("unknown") {
		t.Fatal("security report value validation is inconsistent")
	}
	if chooseString("new", "old") != "new" || chooseString("", "old") != "old" {
		t.Fatal("fallback string selection is incorrect")
	}
}

func TestSecurityReportMaintenanceRejectsMalformedCommandsAndMissingReports(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "security-errors.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := listSecurityReports(ctx, store, []string{"--status", "received", "unexpected"}); err == nil {
		t.Fatal("unexpected list argument was accepted")
	}
	if _, err := loadSecurityReport(ctx, store, "missing"); err == nil {
		t.Fatal("missing report was loaded")
	}
	if err := exportSecurityReport(ctx, store, nil); err == nil {
		t.Fatal("export without a tracking ID was accepted")
	}
	if err := exportSecurityReport(ctx, store, []string{"missing"}); err == nil {
		t.Fatal("missing report was exported")
	}
	if err := updateSecurityReport(ctx, store, nil, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("update without a tracking ID was accepted")
	}
	if err := updateSecurityReport(ctx, store, []string{"missing"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("missing report was updated")
	}
	if err := updateSecurityReport(ctx, store, []string{"missing", "--unknown"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("unknown update option was accepted")
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,token_hash) VALUES('validation',1,'low','web','patching',?,'pgp','hash')`, []byte("ciphertext"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := updateSecurityReport(ctx, store, []string{"validation", "--comment", strings.Repeat("x", 2001)}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("overlong status comment was accepted")
	}
	if err := updateSecurityReport(ctx, store, []string{"validation", "--status", "unknown"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("invalid status was accepted")
	}
	if err := updateSecurityReport(ctx, store, []string{"validation", "--status", "confirmed"}, t.TempDir(), &config.Config{}); err == nil {
		t.Fatal("backward status transition was accepted")
	}
}

func TestAnonymousDisclosureAndStatusNotice(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "anonymous-disclosure.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := []byte("0123456789abcdef0123456789abcdef")
	contactCiphertext, err := encryptDataCiphertext(key, []byte(`{"email":"researcher@example.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,contact_enc,credit_pref,token_hash) VALUES('anonymous',1,'medium','api','received',?,'aes-256-gcm',?,'anon','hash')`, []byte("ciphertext"), contactCiphertext)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	configuration := &config.Config{}
	configuration.Server.Security.EncryptionKey = base64.RawURLEncoding.EncodeToString(key)
	if err := updateSecurityReport(ctx, store, []string{"anonymous", "--status", "disclosed"}, t.TempDir(), configuration); err != nil {
		t.Fatal(err)
	}
	var credit string
	if err := store.Read().QueryRowContext(ctx, `SELECT credit FROM security_thanks WHERE tracking_id='anonymous'`).Scan(&credit); err != nil {
		t.Fatal(err)
	}
	if credit != "Anonymous Researcher #1" {
		t.Fatalf("anonymous credit = %q", credit)
	}
}

func TestSecurityMaintenanceShowAndUnknownAction(t *testing.T) {
	databaseDir := t.TempDir()
	store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,token_hash,created_at,updated_at) VALUES('show-report',1,'high','api','triaged',?,'pgp','hash','2026-01-01','2026-01-02')`, []byte("ciphertext"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	configuration := &config.Config{}
	configuration.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	configuration.Server.Security.EncryptionKey = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := handleSecurityMaintenance([]string{"show", "show-report"}, databaseDir, t.TempDir(), configuration); err != nil {
		t.Fatal(err)
	}
	if err := handleSecurityMaintenance([]string{"unknown"}, databaseDir, t.TempDir(), configuration); err == nil {
		t.Fatal("unknown security maintenance action was accepted")
	}
}
