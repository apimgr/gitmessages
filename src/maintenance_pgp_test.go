package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/contact"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/pgp"
)

func TestPGPKeyRotationReencryptsReportsAndRetainsPriorKey(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "pgp.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	directory := filepath.Join(t.TempDir(), "security")
	configuration := &config.Config{}
	configuration.Server.Branding.Title = "Git Messages"
	configuration.Server.Contact.Security.Email = "security@example.test"
	secret := make([]byte, 32)
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	oldPair, err := generatePGPKey(configuration, secret, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := pgp.WriteCurrent(directory, oldPair); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := pgp.Encrypt(oldPair.Public, []byte("confidential report"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,token_hash) VALUES('report-1',1,'high','api',?,'pgp','hash')`, ciphertext)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := rotatePGPKey(ctx, store, configuration, directory, secret); err != nil {
		t.Fatal(err)
	}
	newPublic, err := os.ReadFile(filepath.Join(directory, "pgp.pub.asc"))
	if err != nil {
		t.Fatal(err)
	}
	newMetadata, err := pgp.MetadataForPublic(newPublic)
	if err != nil {
		t.Fatal(err)
	}
	if newMetadata.Fingerprint == oldPair.Metadata.Fingerprint {
		t.Fatal("rotation retained the old public key")
	}
	newPrivateEncrypted, err := os.ReadFile(filepath.Join(directory, "pgp.priv.asc.enc"))
	if err != nil {
		t.Fatal(err)
	}
	var newCiphertext []byte
	if err := store.Read().QueryRowContext(ctx, `SELECT body_enc FROM security_reports WHERE tracking_id='report-1'`).Scan(&newCiphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := pgp.Decrypt(newCiphertext, newPrivateEncrypted, secret)
	if err != nil || string(plain) != "confidential report" {
		t.Fatalf("retained report did not decrypt after rotation: %q, %v", plain, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "pgp.priv.old.asc.enc")); err != nil {
		t.Fatalf("prior private key was not retained: %v", err)
	}
	if err := rotatePGPKey(ctx, store, configuration, directory, secret); err == nil || !strings.Contains(err.Error(), "still within its 30-day retention") {
		t.Fatalf("second rotation was not refused during retention: %v", err)
	}
}

func TestPGPKeyserverHostValidationAndProgress(t *testing.T) {
	valid := []string{"keys.openpgp.org", "pgp.example.test:8443"}
	for _, host := range valid {
		if !validPGPKeyserverHost(host) {
			t.Errorf("valid keyserver host %q was rejected", host)
		}
	}
	invalid := []string{"https://keys.example.test", "user@keys.example.test", "keys.example.test/path", "keys.example.test?x=1", "keys.example.test\n.evil.test"}
	for _, host := range invalid {
		if validPGPKeyserverHost(host) {
			t.Errorf("invalid keyserver host %q was accepted", host)
		}
	}
	state := &pgpKeyserverState{}
	first := keyserverProgress(state, "keys.example.test")
	first.Attempts = 2
	second := keyserverProgress(state, "KEYS.EXAMPLE.TEST")
	if first != second || len(state.Servers) != 1 || second.Attempts != 2 {
		t.Fatal("keyserver progress was not matched case-insensitively")
	}
}

func TestRecoverySeedsActiveMetadataAndActivatesInstallationSecretStage(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "recovery.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configDir := t.TempDir()
	directory := filepath.Join(configDir, "security")
	secret, err := contact.InstallationSecret(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &config.Config{}
	configuration.Server.Branding.Title = "Recovery Test"
	configuration.Server.Contact.Security.Email = "security@example.test"
	pair, err := generatePGPKey(configuration, secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := pgp.WriteCurrent(directory, pair); err != nil {
		t.Fatal(err)
	}
	if err := recoverPGPKeyState(context.Background(), store, configDir); err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	if err := store.Read().QueryRow(`SELECT fingerprint FROM pgp_key_metadata WHERE id=1`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if fingerprint != pair.Metadata.Fingerprint {
		t.Fatalf("recovered metadata fingerprint %q, want %q", fingerprint, pair.Metadata.Fingerprint)
	}
	nextSecret := make([]byte, 32)
	for index := range nextSecret {
		nextSecret[index] = byte(index + 47)
	}
	wrapped, err := pgp.RewrapPrivateKey(pair.EncryptedPrivate, secret, nextSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePGPState(filepath.Join(directory, "pgp.priv.secret-new"), wrapped); err != nil {
		t.Fatal(err)
	}
	if err := store.RotateAppSecretTo(context.Background(), "installation_secret", nextSecret); err != nil {
		t.Fatal(err)
	}
	if err := recoverInstallationSecretPGPKey(context.Background(), store, configDir); err != nil {
		t.Fatal(err)
	}
	active, err := readPGPFile(filepath.Join(directory, "pgp.priv.asc.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pgp.DecryptPrivate(active, nextSecret); err != nil {
		t.Fatalf("recovered PGP private key cannot be opened with the current installation secret: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "pgp.priv.secret-new")); !os.IsNotExist(err) {
		t.Fatalf("staged private key still exists after recovery: %v", err)
	}
}

func TestRecoveryDiscardsInstallationSecretStageForOldSecret(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "recovery.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configDir := t.TempDir()
	directory := filepath.Join(configDir, "security")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "pgp.priv.secret-new"), []byte("invalid stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverInstallationSecretPGPKey(context.Background(), store, configDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "pgp.priv.secret-new")); !os.IsNotExist(err) {
		t.Fatalf("invalid stage was not discarded: %v", err)
	}
}

func TestPGPImportAndPrivateExportPreservePermissions(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "import-export.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configuration := &config.Config{}
	configuration.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	configuration.Server.Security.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"
	identity, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, 25, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := contact.InstallationSecret(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := generatePGPKey(configuration, secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	private, err := pgp.ExportPrivate(pair.EncryptedPrivate, secret)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "security")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	importPath := filepath.Join(t.TempDir(), "import.asc")
	if err := os.WriteFile(importPath, private, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := importPGPKey(ctx, []string{importPath}, store, directory, secret, identity, configuration); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "pgp.priv.asc.enc"), pair.EncryptedPrivate, 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(t.TempDir(), "operator-reason-*")
	if err != nil {
		t.Fatal(err)
	}
	inputPath := input.Name()
	if _, err := input.WriteString("maintenance export test\n"); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(inputPath); err != nil || string(contents) != "maintenance export test\n" {
		t.Fatalf("operator reason input = %q, %v", contents, err)
	}
	originalStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = stdin.Close()
	})
	exportPath := filepath.Join(t.TempDir(), "private.asc")
	if err := exportPGPKey(ctx, []string{"private", exportPath}, store, directory, secret, identity, configuration); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private export mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := pgp.ParseArmoredPrivate(private); err != nil {
		t.Fatalf("exported private key is invalid: %v", err)
	}
}

func TestPGPStateAndExportFilesAreAtomicPrivateAndNonOverwriting(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "security", "keyservers.state")
	if err := writePGPState(statePath, []byte(`{"servers":[]}`)); err != nil {
		t.Fatal(err)
	}
	state, err := readPGPFile(statePath)
	if err != nil || string(state) != `{"servers":[]}` {
		t.Fatalf("keyserver state = %q, %v", state, err)
	}
	stateInfo, err := os.Stat(statePath)
	if err != nil || stateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("keyserver state permissions = %v, %v", stateInfo, err)
	}
	unsafeDir := filepath.Join(directory, "folder")
	if err := os.Mkdir(unsafeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readPGPFile(unsafeDir); err == nil {
		t.Fatal("directory was accepted as a PGP file")
	}
	linkPath := filepath.Join(directory, "state-link")
	if err := os.Symlink(statePath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readPGPFile(linkPath); err == nil {
		t.Fatal("symlink was accepted as a PGP file")
	}
	exportPath := filepath.Join(directory, "exports", "public.asc")
	if err := writePGPExport(exportPath, []byte("public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePGPExport(exportPath, []byte("replacement"), 0o644); err == nil {
		t.Fatal("existing export was overwritten")
	}
	public, err := os.ReadFile(exportPath)
	if err != nil || string(public) != "public key" {
		t.Fatalf("existing export changed: %q, %v", public, err)
	}
}

func TestPGPRecoveryHandlesEmptyStateAndContactIdentityFallbacks(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "empty-state.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configDir := t.TempDir()
	if err := recoverPGPKeyState(context.Background(), store, configDir); err != nil {
		t.Fatalf("empty PGP state recovery failed: %v", err)
	}
	if err := recoverInstallationSecretPGPKey(context.Background(), store, configDir); err != nil {
		t.Fatalf("missing installation-secret stage should be a no-op: %v", err)
	}
	configuration := &config.Config{}
	t.Setenv("DOMAIN", "git.example.test")
	if got := pgpContactEmail(configuration); got != "security@git.example.test" {
		t.Fatalf("resolved security contact = %q", got)
	}
	configuration.Server.FQDN = "git.example.test"
	t.Setenv("DOMAIN", "public.example.test")
	if got := pgpContactEmail(configuration); got != "security@public.example.test" {
		t.Fatalf("server.fqdn overrode the resolved security contact = %q", got)
	}
}

func TestMaintenanceCommandUsageAndEncryptionKeyValidation(t *testing.T) {
	configuration := &config.Config{}
	if err := handlePGPMaintenance(nil, t.TempDir(), t.TempDir(), configuration); err == nil {
		t.Fatal("empty PGP maintenance command was accepted")
	}
	if err := handleSecretMaintenance(nil, t.TempDir(), t.TempDir(), t.TempDir(), configuration); err == nil {
		t.Fatal("empty secret maintenance command was accepted")
	}
	if err := handleSecurityMaintenance(nil, t.TempDir(), t.TempDir(), configuration); err == nil {
		t.Fatal("empty security maintenance command was accepted")
	}
	if _, err := decodeConfiguredEncryptionKey("bad-key"); err == nil {
		t.Fatal("invalid configured encryption key was accepted")
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	if decoded, err := decodeConfiguredEncryptionKey(base64.StdEncoding.EncodeToString(key)); err != nil || !equalBytes(decoded, key) {
		t.Fatalf("standard-base64 encryption key decode = %x, %v", decoded, err)
	}
}
