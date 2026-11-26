package database

import (
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedAppSecretsInitializeOnceAndRotateWithGrace(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "secrets.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, name := range []string{"installation_secret", "cookie_signing_key", "csrf_token_secret"} {
		var value string
		if err := store.Read().QueryRow(`SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value); err != nil {
			t.Fatalf("generated secret %s is missing: %v", name, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("generated secret %s is invalid: length=%d err=%v", name, len(decoded), err)
		}
	}
	var original string
	if err := store.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='cookie_signing_key'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("repeat schema initialization: %v", err)
	}
	var unchanged string
	if err := store.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='cookie_signing_key'`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != original {
		t.Fatal("startup initialization replaced an existing generated secret")
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE app_secrets SET updated_at=? WHERE name='cookie_signing_key'`, time.Now().UTC().Add(-91*24*time.Hour).Format(time.RFC3339))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("automatic secret rotation: %v", err)
	}
	var previous, current string
	var version int64
	if err := store.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='cookie_signing_key_previous'`).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if err := store.Read().QueryRow(`SELECT value,version FROM app_secrets WHERE name='cookie_signing_key'`).Scan(&current, &version); err != nil {
		t.Fatal(err)
	}
	if previous != original || current == original || version < 2 {
		t.Fatalf("rotated key state previous=%q current_changed=%v version=%d", previous, current != original, version)
	}
	var csrfOriginal string
	if err := store.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='csrf_token_secret'`).Scan(&csrfOriginal); err != nil {
		t.Fatal(err)
	}
	if err := store.RotateAppSecret(context.Background(), "csrf_token_secret"); err != nil {
		t.Fatalf("first CSRF secret rotation: %v", err)
	}
	if err := store.RotateAppSecret(context.Background(), "csrf_token_secret"); err != nil {
		t.Fatalf("second CSRF secret rotation during grace: %v", err)
	}
	var previousCSRF int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE name IN ('csrf_token_secret_previous','csrf_token_secret_previous_until')`).Scan(&previousCSRF); err != nil {
		t.Fatal(err)
	}
	if previousCSRF != 0 || csrfOriginal == "" {
		t.Fatalf("CSRF rotations should not retain a previous signing key: rows=%d", previousCSRF)
	}
	if err := store.RetireExpiredAppSecrets(context.Background(), time.Now().UTC().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE name IN ('cookie_signing_key_previous','cookie_signing_key_previous_until')`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expired previous key rows remain: %d", remaining)
	}
}

func TestPreviousEncryptionKeyUsesConfigVersionAndGrace(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "encryption-rotation.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	previous := make([]byte, 32)
	current := make([]byte, 32)
	for index := range previous {
		previous[index] = byte(index + 1)
		current[index] = byte(index + 33)
	}
	if err := store.StorePreviousEncryptionKey(context.Background(), previous, current, 30*24*time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.PreviousEncryptionKey(context.Background(), current, time.Now().UTC(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSecretBytes(recovered, previous) {
		t.Fatal("stored grace key did not match the previous key")
	}
	if _, err = store.PreviousEncryptionKey(context.Background(), current, time.Now().UTC(), 1); err == nil {
		t.Fatal("stale config version should be rejected while preserving grace key state")
	}
	var retained int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE name IN ('encryption_key_previous','encryption_key_previous_until')`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 2 {
		t.Fatalf("stale config removed shared encryption grace state: rows=%d", retained)
	}
	staleCurrent := make([]byte, 32)
	for index := range staleCurrent {
		staleCurrent[index] = byte(index + 65)
	}
	if err := store.StorePreviousEncryptionKey(context.Background(), current, staleCurrent, 30*24*time.Hour, 3); err == nil {
		t.Fatal("rotation with a stale current key should be rejected")
	}
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE name IN ('encryption_key_previous','encryption_key_previous_until')`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 2 {
		t.Fatalf("stale rotation removed shared encryption grace state: rows=%d", retained)
	}
	recovered, err = store.PreviousEncryptionKey(context.Background(), current, time.Now().UTC(), 2)
	if err != nil || !equalSecretBytes(recovered, previous) {
		t.Fatalf("stale rotation damaged recoverable grace key: recovered=%v err=%v", equalSecretBytes(recovered, previous), err)
	}
}

func equalSecretBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
