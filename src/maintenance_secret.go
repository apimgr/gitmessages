package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/contact"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/pgp"
)

func handleSecretMaintenance(args []string, databaseDir, configDir, logsDir string, configuration *config.Config) error {
	if len(args) != 2 || args[0] != "rotate" || (args[1] != "installation_secret" && args[1] != "encryption_key") {
		return errors.New("usage: --maintenance secret rotate installation_secret|encryption_key")
	}
	store, err := database.OpenConfigured(configuration.Server.Database, filepath.Join(databaseDir, "server.db"), 5)
	if err != nil {
		return fmt.Errorf("open secret maintenance database: %w", err)
	}
	defer func() { _ = store.Close() }()
	identity, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, configuration.Server.Tokens.MaxLinked, configuration.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(configuration.Server.Tokens.CookieMaxAge))
	if err != nil {
		return err
	}
	operation := "Rotate the installation secret"
	if args[1] == "encryption_key" {
		operation = "Rotate the encryption key"
	}
	if err := authorizeSensitivePGPOperation(identity, configuration, operation); err != nil {
		return err
	}
	if args[1] == "encryption_key" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return rotateEncryptionKey(ctx, store, logsDir, configuration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	oldSecret, err := contact.InstallationSecret(ctx, store)
	if err != nil {
		return err
	}
	nextSecret := make([]byte, 32)
	if _, err := rand.Read(nextSecret); err != nil {
		return fmt.Errorf("generate installation secret: %w", err)
	}
	securityDir := filepath.Join(configDir, "security")
	privatePath := filepath.Join(securityDir, "pgp.priv.asc.enc")
	stagePath := filepath.Join(securityDir, "pgp.priv.secret-new")
	private, privateErr := readPGPFile(privatePath)
	if privateErr == nil {
		wrapped, err := pgp.RewrapPrivateKey(private, oldSecret, nextSecret)
		if err != nil {
			return fmt.Errorf("rewrap PGP private key: %w", err)
		}
		if err := writePGPState(stagePath, wrapped); err != nil {
			return fmt.Errorf("stage rewrapped PGP private key: %w", err)
		}
	} else if !errors.Is(privateErr, os.ErrNotExist) {
		return privateErr
	}
	if err := store.RotateAppSecretTo(ctx, "installation_secret", nextSecret); err != nil {
		_ = os.Remove(stagePath)
		return err
	}
	if privateErr == nil {
		if err := os.Rename(stagePath, privatePath); err != nil {
			return fmt.Errorf("activate rewrapped PGP private key: %w", err)
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	if err := appendSecurityLog(logsDir, map[string]any{"timestamp": stamp, "action": "security.installation_secret_rotated"}); err != nil {
		return err
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','security','security.installation_secret_rotated','local','secret','installation_secret','{}',1)`)
		return err
	}); err != nil {
		return err
	}
	fmt.Println("Installation secret rotated; in-flight signed contact forms and security links remain valid for the 7-day grace period.")
	return nil
}

func rotateEncryptionKey(ctx context.Context, store *database.Store, logsDir string, configuration *config.Config) error {
	previous, err := decodeConfiguredEncryptionKey(configuration.Server.Security.EncryptionKey)
	if err != nil {
		return err
	}
	current := make([]byte, 32)
	if _, err := rand.Read(current); err != nil {
		return fmt.Errorf("generate encryption key: %w", err)
	}
	targetVersion := configuration.Server.Security.EncryptionKeyVersion + 1
	if err := store.StorePreviousEncryptionKey(ctx, previous, current, 30*24*time.Hour, targetVersion); err != nil {
		return err
	}
	encoded := base64.RawURLEncoding.EncodeToString(current)
	if err := config.SaveEncryptionKey(encoded); err != nil {
		return err
	}
	configuration.Server.Security.EncryptionKey = encoded
	configuration.Server.Security.EncryptionKeyVersion = targetVersion
	if err := recoverEncryptionKeyRotation(ctx, store, configuration); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	if err := appendSecurityLog(logsDir, map[string]any{"timestamp": stamp, "action": "security.encryption_key_rotated"}); err != nil {
		return err
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','security','security.encryption_key_rotated','local','secret','encryption_key','{}',1)`)
		return err
	}); err != nil {
		return err
	}
	fmt.Println("Encryption key rotated; stored token and security-report data has been re-encrypted, with a 30-day grace key retained.")
	return nil
}

func recoverEncryptionKeyRotation(ctx context.Context, store *database.Store, configuration *config.Config) error {
	current, err := decodeConfiguredEncryptionKey(configuration.Server.Security.EncryptionKey)
	if err != nil {
		return err
	}
	previous, err := store.PreviousEncryptionKey(ctx, current, time.Now().UTC(), configuration.Server.Security.EncryptionKeyVersion)
	if err != nil || len(previous) == 0 {
		return err
	}
	if err := auth.ReencryptTokenData(ctx, store, previous, current); err != nil {
		return err
	}
	return reencryptSecurityReportData(ctx, store, previous, current)
}

func reencryptSecurityReportData(ctx context.Context, store *database.Store, previous, current []byte) error {
	rows, err := store.Read().QueryContext(ctx, `SELECT tracking_id,body_alg,body_enc,contact_enc FROM security_reports ORDER BY tracking_id`)
	if err != nil {
		return err
	}
	type reportCipher struct {
		id            string
		body, contact []byte
		updateBody    bool
	}
	updates := make([]reportCipher, 0)
	for rows.Next() {
		var id, algorithm string
		var body, contactData []byte
		if err := rows.Scan(&id, &algorithm, &body, &contactData); err != nil {
			_ = rows.Close()
			return err
		}
		update := reportCipher{id: id, body: body, contact: contactData}
		if algorithm == "aes-256-gcm" {
			_, currentErr := decryptDataCiphertext(current, body)
			if currentErr != nil {
				plain, decryptErr := decryptDataCiphertext(previous, body)
				if decryptErr != nil {
					_ = rows.Close()
					return fmt.Errorf("decrypt security report %s with current or grace key: %w", id, decryptErr)
				}
				update.body, err = encryptDataCiphertext(current, plain)
				if err != nil {
					_ = rows.Close()
					return err
				}
				update.updateBody = true
			}
		}
		if len(contactData) > 0 {
			_, currentErr := decryptDataCiphertext(current, contactData)
			if currentErr != nil {
				plain, decryptErr := decryptDataCiphertext(previous, contactData)
				if decryptErr != nil {
					_ = rows.Close()
					return fmt.Errorf("decrypt researcher contact for report %s with current or grace key: %w", id, decryptErr)
				}
				update.contact, err = encryptDataCiphertext(current, plain)
				if err != nil {
					_ = rows.Close()
					return err
				}
			}
		}
		if update.updateBody || !equalBytes(update.contact, contactData) {
			updates = append(updates, update)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		for _, update := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET body_enc=?,contact_enc=?,updated_at=? WHERE tracking_id=?`, update.body, update.contact, time.Now().UTC().Format(time.RFC3339), update.id); err != nil {
				return fmt.Errorf("re-encrypt security report %s: %w", update.id, err)
			}
		}
		return nil
	})
}

func encryptDataCiphertext(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decryptDataCiphertext(key, encrypted []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < gcm.NonceSize() {
		return nil, errors.New("invalid encrypted data")
	}
	return gcm.Open(nil, encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():], nil)
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
