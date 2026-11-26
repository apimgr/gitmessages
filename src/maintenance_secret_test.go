package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
)

func TestEncryptionKeyRecoveryReencryptsSensitiveReports(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "secret-rotation.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	previous := make([]byte, 32)
	current := make([]byte, 32)
	for index := range previous {
		previous[index] = byte(index + 3)
		current[index] = byte(index + 41)
	}
	if err := store.StorePreviousEncryptionKey(context.Background(), previous, current, 30*24*time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	oldBody, err := encryptDataCiphertext(previous, []byte("report body"))
	if err != nil {
		t.Fatal(err)
	}
	oldContact, err := encryptDataCiphertext(previous, []byte("researcher contact"))
	if err != nil {
		t.Fatal(err)
	}
	pgpBody := []byte("PGP ciphertext remains unchanged")
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,contact_enc,token_hash) VALUES('aes-report',1,'high','api',?,'aes-256-gcm',?,'hash')`, oldBody, oldContact); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,token_hash) VALUES('pgp-report',1,'low','web',?,'pgp','hash')`, pgpBody)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	configuration := &config.Config{}
	configuration.Server.Security.EncryptionKey = base64.RawURLEncoding.EncodeToString(current)
	configuration.Server.Security.EncryptionKeyVersion = 2
	if err := recoverEncryptionKeyRotation(ctx, store, configuration); err != nil {
		t.Fatal(err)
	}
	var body, contactData, preserved []byte
	if err := store.Read().QueryRowContext(ctx, `SELECT body_enc,contact_enc FROM security_reports WHERE tracking_id='aes-report'`).Scan(&body, &contactData); err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptDataCiphertext(current, body); err != nil || string(plain) != "report body" {
		t.Fatalf("report body was not re-encrypted: %q, %v", plain, err)
	}
	if plain, err := decryptDataCiphertext(current, contactData); err != nil || string(plain) != "researcher contact" {
		t.Fatalf("researcher contact was not re-encrypted: %q, %v", plain, err)
	}
	if err := store.Read().QueryRowContext(ctx, `SELECT body_enc FROM security_reports WHERE tracking_id='pgp-report'`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(preserved, pgpBody) {
		t.Fatal("PGP-encrypted report body was changed during AES key rotation")
	}
}
