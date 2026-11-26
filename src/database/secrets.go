package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

type generatedSecretPolicy struct {
	name        string
	rotationAge time.Duration
	grace       time.Duration
}

var generatedSecretPolicies = []generatedSecretPolicy{
	{name: "installation_secret", grace: 7 * 24 * time.Hour},
	{name: "cookie_signing_key", rotationAge: 90 * 24 * time.Hour, grace: 7 * 24 * time.Hour},
	{name: "csrf_token_secret", rotationAge: 180 * 24 * time.Hour},
}

func (s *Store) initializeAppSecrets(ctx context.Context) error {
	now := time.Now().UTC()
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, policy := range generatedSecretPolicies {
			var value string
			var version int64
			var updatedAt string
			err := tx.QueryRowContext(ctx, `SELECT value, version, updated_at FROM app_secrets WHERE name = ?`, policy.name).Scan(&value, &version, &updatedAt)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("read generated secret %s: %w", policy.name, err)
			}
			if err == sql.ErrNoRows {
				raw := make([]byte, 32)
				if _, err := rand.Read(raw); err != nil {
					return fmt.Errorf("generate %s: %w", policy.name, err)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name, value, version, updated_at) VALUES(?, ?, 1, ?) ON CONFLICT(name) DO NOTHING`, policy.name, base64.StdEncoding.EncodeToString(raw), now.Format(time.RFC3339)); err != nil {
					return fmt.Errorf("store generated secret %s: %w", policy.name, err)
				}
				continue
			}
			if policy.rotationAge == 0 {
				continue
			}
			updated, parseErr := time.Parse(time.RFC3339Nano, updatedAt)
			if parseErr != nil || now.Sub(updated) < policy.rotationAge {
				continue
			}
			if policy.grace == 0 {
				if err := rotateAppSecretImmediatelyTx(ctx, tx, policy.name, version, now); err != nil {
					return err
				}
				continue
			}
			if err := rotateAppSecretTx(ctx, tx, policy.name, value, version, now, policy.grace); err != nil {
				return err
			}
		}
		return nil
	})
}

// RotateAppSecret rotates a supported automatically managed application key.
func (s *Store) RotateAppSecret(ctx context.Context, name string) error {
	policy, ok := secretPolicy(name)
	if !ok || (name != "installation_secret" && name != "cookie_signing_key" && name != "csrf_token_secret") {
		return fmt.Errorf("unsupported automatic secret %q", name)
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		var value string
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT value, version FROM app_secrets WHERE name=?`, name).Scan(&value, &version); err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		now := time.Now().UTC()
		if policy.grace == 0 {
			return rotateAppSecretImmediatelyTx(ctx, tx, name, version, now)
		}
		var previousUntil string
		err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name+"_previous_until").Scan(&previousUntil)
		if err == nil {
			until, parseErr := time.Parse(time.RFC3339Nano, previousUntil)
			if parseErr == nil && now.Before(until) {
				return fmt.Errorf("cannot rotate %s while its previous key remains in grace", name)
			}
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read %s grace deadline: %w", name, err)
		}
		return rotateAppSecretTx(ctx, tx, name, value, version, now, policy.grace)
	})
}

// RotateAppSecretTo rotates installation_secret to a caller-staged value after storing its grace copy.
func (s *Store) RotateAppSecretTo(ctx context.Context, name string, next []byte) error {
	if name != "installation_secret" || len(next) != 32 {
		return fmt.Errorf("unsupported manual application secret rotation %q", name)
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		var previous string
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT value,version FROM app_secrets WHERE name=?`, name).Scan(&previous, &version); err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		now := time.Now().UTC()
		var until string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name+"_previous_until").Scan(&until); err == nil {
			deadline, parseErr := time.Parse(time.RFC3339Nano, until)
			if parseErr == nil && now.Before(deadline) {
				return fmt.Errorf("cannot rotate %s while its previous secret remains in grace", name)
			}
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("read %s grace deadline: %w", name, err)
		}
		return rotateAppSecretWithValueTx(ctx, tx, name, previous, version, next, now, 7*24*time.Hour)
	})
}

// StorePreviousEncryptionKey stores the old data key wrapped under the replacement key.
func (s *Store) StorePreviousEncryptionKey(ctx context.Context, previous, current []byte, grace time.Duration, targetVersion int) error {
	if len(previous) != 32 || len(current) != 32 || grace <= 0 || targetVersion < 1 {
		return errors.New("encryption key rotation requires two 32-byte keys and a positive grace period")
	}
	now := time.Now().UTC()
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		var encoded, until string
		err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous'`).Scan(&encoded)
		if err == nil {
			if err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous_until'`).Scan(&until); err != nil {
				return err
			}
			deadline, parseErr := time.Parse(time.RFC3339Nano, until)
			if parseErr != nil {
				return fmt.Errorf("invalid encryption_key grace deadline: %w", parseErr)
			}
			if _, err := unwrapPreviousEncryptionKey(encoded, current); err != nil {
				return fmt.Errorf("cannot verify existing encryption_key grace state: %w", err)
			}
			if now.Before(deadline) {
				return errors.New("cannot rotate encryption_key while its previous key remains in grace")
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		wrapped, err := wrapPreviousEncryptionKey(previous, current)
		if err != nil {
			return err
		}
		stamp := now.Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES('encryption_key_previous',?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=excluded.version,updated_at=excluded.updated_at`, wrapped, targetVersion, stamp); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES('encryption_key_previous_until',?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=app_secrets.version+1,updated_at=excluded.updated_at`, now.Add(grace).Format(time.RFC3339Nano), 1, stamp)
		return err
	}); err != nil {
		return fmt.Errorf("store previous encryption key: %w", err)
	}
	return nil
}

// PreviousEncryptionKey returns the unwrapped grace key while its deadline remains active.
func (s *Store) PreviousEncryptionKey(ctx context.Context, current []byte, now time.Time, configVersion int) ([]byte, error) {
	var encoded, until string
	var targetVersion int
	err := s.Read().QueryRowContext(ctx, `SELECT value,version FROM app_secrets WHERE name='encryption_key_previous'`).Scan(&encoded, &targetVersion)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if configVersion < targetVersion {
		return nil, fmt.Errorf("encryption key grace state requires config version %d (current %d)", targetVersion, configVersion)
	}
	if err := s.Read().QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous_until'`).Scan(&until); err != nil {
		return nil, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, until)
	if err != nil || !now.Before(deadline) {
		return nil, nil
	}
	return unwrapPreviousEncryptionKey(encoded, current)
}

func wrapPreviousEncryptionKey(previous, current []byte) (string, error) {
	block, err := aes.NewCipher(current)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, previous, []byte("gitmessages/encryption-key-previous/v1"))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func unwrapPreviousEncryptionKey(encoded string, current []byte) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(current) != 32 {
		return nil, errors.New("stored previous encryption key is invalid")
	}
	block, err := aes.NewCipher(current)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return nil, errors.New("stored previous encryption key is invalid")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("gitmessages/encryption-key-previous/v1"))
	if err != nil || len(plain) != 32 {
		return nil, errors.New("stored previous encryption key is invalid")
	}
	return plain, nil
}

// RetireExpiredAppSecrets removes previous keys after their configured grace period.
func (s *Store) RetireExpiredAppSecrets(ctx context.Context, now time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, policy := range generatedSecretPolicies {
			if policy.grace == 0 {
				continue
			}
			var until string
			err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, policy.name+"_previous_until").Scan(&until)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("read %s grace deadline: %w", policy.name, err)
			}
			if err == nil {
				expires, parseErr := time.Parse(time.RFC3339Nano, until)
				if parseErr == nil && !now.Before(expires) {
					if _, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?, ?)`, policy.name+"_previous", policy.name+"_previous_until"); err != nil {
						return fmt.Errorf("retire %s previous key: %w", policy.name, err)
					}
				}
			}
		}
		var until string
		err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous_until'`).Scan(&until)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read encryption_key grace deadline: %w", err)
		}
		if err == nil {
			deadline, parseErr := time.Parse(time.RFC3339Nano, until)
			if parseErr == nil && !now.Before(deadline) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN ('encryption_key_previous','encryption_key_previous_until')`); err != nil {
					return fmt.Errorf("retire previous encryption key: %w", err)
				}
			}
		}
		return nil
	})
}

func rotateAppSecretImmediatelyTx(ctx context.Context, tx *sql.Tx, name string, version int64, now time.Time) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate rotated %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_secrets SET value=?,version=?,updated_at=? WHERE name=?`, base64.StdEncoding.EncodeToString(raw), version+1, now.Format(time.RFC3339), name); err != nil {
		return fmt.Errorf("activate rotated %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?, ?)`, name+"_previous", name+"_previous_until"); err != nil {
		return fmt.Errorf("remove stale %s grace rows: %w", name, err)
	}
	return nil
}

func secretPolicy(name string) (generatedSecretPolicy, bool) {
	for _, policy := range generatedSecretPolicies {
		if policy.name == name {
			return policy, true
		}
	}
	return generatedSecretPolicy{}, false
}

func rotateAppSecretTx(ctx context.Context, tx *sql.Tx, name, previous string, version int64, now time.Time, grace time.Duration) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate rotated %s: %w", name, err)
	}
	return rotateAppSecretWithValueTx(ctx, tx, name, previous, version, raw, now, grace)
}

func rotateAppSecretWithValueTx(ctx context.Context, tx *sql.Tx, name, previous string, version int64, raw []byte, now time.Time, grace time.Duration) error {
	if grace <= 0 || len(raw) != 32 {
		return fmt.Errorf("secret %s has invalid rotation parameters", name)
	}
	stamp := now.Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=excluded.version,updated_at=excluded.updated_at`, name+"_previous", previous, version, stamp); err != nil {
		return fmt.Errorf("store previous %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=app_secrets.version+1,updated_at=excluded.updated_at`, name+"_previous_until", now.Add(grace).Format(time.RFC3339), stamp); err != nil {
		return fmt.Errorf("store %s grace deadline: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_secrets SET value=?,version=version+1,updated_at=? WHERE name=?`, base64.StdEncoding.EncodeToString(raw), stamp, name); err != nil {
		return fmt.Errorf("activate rotated %s: %w", name, err)
	}
	return nil
}
