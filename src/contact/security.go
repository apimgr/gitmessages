package contact

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

const installationSecretName = "installation_secret"

// InstallationSecret returns the stable HMAC key stored in the shared database.
func InstallationSecret(ctx context.Context, store *database.Store) ([]byte, error) {
	if store == nil {
		return nil, errors.New("database store is required")
	}
	var secret []byte
	err := store.Write(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, "SELECT value FROM app_secrets WHERE name = ?", installationSecretName).Scan(&existing)
		if err == nil {
			decoded, legacy, decodeErr := decodeInstallationSecret(existing)
			if decodeErr != nil {
				return decodeErr
			}
			secret = decoded
			if legacy {
				if _, err := tx.ExecContext(ctx, "UPDATE app_secrets SET value = ?, updated_at = ? WHERE name = ?", base64.StdEncoding.EncodeToString(decoded), time.Now().UTC().Format(time.RFC3339Nano), installationSecretName); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		if _, err := tx.ExecContext(ctx, "INSERT INTO app_secrets(name, value, updated_at) VALUES(?, ?, ?)", installationSecretName, encoded, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		secret = raw
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load installation secret: %w", err)
	}
	return secret, nil
}

func decodeInstallationSecret(value string) ([]byte, bool, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, false, nil
	}
	decoded, err = hex.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, true, nil
	}
	return nil, false, errors.New("stored installation secret is invalid")
}

// SecurityID returns the rotating security form token for the current or adjacent 48-hour window.
func SecurityID(secret []byte, now time.Time) string {
	window := now.Unix() / 172800
	return securityIDForWindow(secret, window)
}

func securityIDForWindow(secret []byte, window int64) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%d", window)
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// ValidSecurityID accepts the current and previous 48-hour token windows.
func ValidSecurityID(secret []byte, supplied string, now time.Time) bool {
	if len(supplied) != 16 {
		return false
	}
	window := now.Unix() / 172800
	for _, candidateWindow := range []int64{window, window - 1} {
		candidate := securityIDForWindow(secret, candidateWindow)
		if hmac.Equal([]byte(candidate), []byte(supplied)) {
			return true
		}
	}
	return false
}

// InvalidSecurityIDFingerprint safely identifies a bad supplied value in logs.
func InvalidSecurityIDFingerprint(supplied string) string {
	digest := sha256.Sum256([]byte(supplied))
	return hex.EncodeToString(digest[:])[:8]
}

// FormToken issues a stateless single-use token. Callers own the replay set.
func FormToken(secret []byte, now time.Time) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	issued := now.Unix()
	nonceText := hex.EncodeToString(nonce)
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "contact-form|%d|%s", issued, nonceText)
	return fmt.Sprintf("%d.%s.%s", issued, nonceText, hex.EncodeToString(mac.Sum(nil))), nil
}

// ReviewToken binds the type and normalized payload to a fresh confirmation token.
func ReviewToken(secret []byte, now time.Time, normalized any, honeypot bool) (string, error) {
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	flags := "-"
	if honeypot {
		flags = "d"
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	issued, nonceText, fieldHash := now.Unix(), hex.EncodeToString(nonce), hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "contact-review|%d|%s|%s|%s", issued, nonceText, flags, fieldHash)
	return fmt.Sprintf("%d.%s.%s.%s", issued, nonceText, flags, hex.EncodeToString(mac.Sum(nil))), nil
}

func normalizedText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
