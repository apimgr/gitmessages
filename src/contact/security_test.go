package contact

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/apimgr/gitmessages/src/database"
)

func TestInstallationSecretUsesBase64Storage(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	secret, err := InstallationSecret(context.Background(), store)
	if err != nil {
		t.Fatalf("generate installation secret: %v", err)
	}
	var stored string
	if err := store.Read().QueryRow("SELECT value FROM app_secrets WHERE name = ?", installationSecretName).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(stored)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("stored secret is not base64-encoded 32-byte data: len=%d err=%v", len(decoded), err)
	}
	if string(decoded) != string(secret) {
		t.Fatal("stored installation secret differs from returned secret")
	}
}

func TestInstallationSecretMigratesLegacyHexStorage(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES(?, ?, 'before')", installationSecretName, hex.EncodeToString(raw))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	secret, err := InstallationSecret(context.Background(), store)
	if err != nil {
		t.Fatalf("load legacy installation secret: %v", err)
	}
	if string(secret) != string(raw) {
		t.Fatal("legacy installation secret changed during migration")
	}
	var stored string
	if err := store.Read().QueryRow("SELECT value FROM app_secrets WHERE name = ?", installationSecretName).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == hex.EncodeToString(raw) {
		t.Fatal("legacy hex installation secret was not migrated")
	}
	decoded, err := base64.StdEncoding.DecodeString(stored)
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("migrated installation secret is invalid: %v", err)
	}
}

func TestInstallationSecretRejectsInvalidStoredValue(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES(?, 'invalid', 'now')", installationSecretName)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallationSecret(context.Background(), store); err == nil {
		t.Fatal("invalid stored installation secret was accepted")
	}
}
