package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

const tokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrPrimaryOnly  = errors.New("primary token required")
	ErrTokenLimit   = errors.New("linked token limit reached")
	ErrConflict     = errors.New("valid token already presented")
	ErrNotFound     = errors.New("token not found")
)

type Identity struct {
	OwnerID string
	TokenID int64
	Kind    string
	Admin   bool
}

type Token struct {
	ID         int64      `json:"id"`
	OwnerID    string     `json:"-"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Status     string     `json:"status"`
	Value      string     `json:"-"`
}

type Store struct {
	db              *database.Store
	operatorHash    [32]byte
	operatorEnabled bool
	encryptionKey   []byte
	maxLinked       int
	idlePurgeDays   int
	cookieMaxAge    time.Duration
}

// Open creates an auth store backed by the shared local SQLite database.
func Open(path, operatorToken, encryptionKey string, maxLinked, idlePurgeDays int, cookieMaxAge time.Duration) (*Store, error) {
	db, err := database.OpenSQLite(path, 5)
	if err != nil {
		return nil, err
	}
	store, err := NewStore(db, operatorToken, encryptionKey, maxLinked, idlePurgeDays, cookieMaxAge)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewStore binds token management to the process-wide SQLite store.
func NewStore(db *database.Store, operatorToken, encryptionKey string, maxLinked, idlePurgeDays int, cookieMaxAge time.Duration) (*Store, error) {
	if db == nil {
		return nil, errors.New("database store is required")
	}
	key, err := decodeEncryptionKey(encryptionKey)
	if err != nil {
		return nil, err
	}
	if maxLinked <= 0 {
		maxLinked = 25
	}
	if cookieMaxAge <= 0 {
		cookieMaxAge = 365 * 24 * time.Hour
	}
	return &Store{db: db, operatorHash: sha256.Sum256([]byte(operatorToken)), operatorEnabled: operatorToken != "", encryptionKey: key, maxLinked: maxLinked, idlePurgeDays: idlePurgeDays, cookieMaxAge: cookieMaxAge}, nil
}

// Database returns the shared database handle for server modules that need it.
func (s *Store) Database() *database.Store { return s.db }

// CookieMaxAge reports the configured sliding owner-cookie lifetime.
func (s *Store) CookieMaxAge() time.Duration { return s.cookieMaxAge }

// RunCleanup applies idle-owner retention on the token-cleanup cadence.
func (s *Store) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	if s.idlePurgeDays > 0 {
		if _, err := s.PurgeIdleOwners(ctx); err != nil {
			log.Printf("token idle cleanup failed: %v", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.idlePurgeDays > 0 {
				if _, err := s.PurgeIdleOwners(ctx); err != nil {
					log.Printf("token idle cleanup failed: %v", err)
				}
			}
		}
	}
}

// PurgeIdleOwners removes owners whose tokens have not been used within policy.
func (s *Store) PurgeIdleOwners(ctx context.Context) (int64, error) {
	if s.idlePurgeDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-time.Duration(s.idlePurgeDays) * 24 * time.Hour).Unix()
	var count int64
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM owners WHERE last_seen_at < ?`, cutoff)
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if count > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(level, category, action, details) VALUES ('info','security','token.idle_purged',?)`, fmt.Sprintf(`{"count":%d}`, count))
		}
		return err
	})
	return count, err
}

func decodeEncryptionKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("server.security.encryption_key must contain 32 encoded bytes")
	}
	return key, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Authenticate validates an owner token. The operator token is checked by
// AuthenticateBearer so it can be recognized without a database lookup.
func (s *Store) Authenticate(ctx context.Context, raw string) (Identity, error) {
	if len(raw) == 0 {
		return Identity{}, ErrInvalidToken
	}
	hash := tokenHash(raw)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var identity Identity
	var storedHash, kind string
	var lastUsed sql.NullInt64
	err := s.db.Read().QueryRowContext(ctx, `SELECT id, owner_id, kind, token_hash, last_used_at FROM api_tokens WHERE token_hash = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, hash, time.Now().Unix()).Scan(&identity.TokenID, &identity.OwnerID, &kind, &storedHash, &lastUsed)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(storedHash)) != 1 {
		return Identity{}, ErrInvalidToken
	}
	identity.Kind = kind
	if !lastUsed.Valid || time.Now().Unix()-lastUsed.Int64 >= 60 {
		if err := s.db.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), identity.TokenID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE owners SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), identity.OwnerID)
			return err
		}); err != nil {
			return Identity{}, err
		}
	}
	return identity, nil
}

// AuthenticateBearer validates operator and owner bearer credentials.
func (s *Store) AuthenticateBearer(ctx context.Context, raw string) (Identity, error) {
	if s.operatorEnabled && raw != "" {
		got := sha256.Sum256([]byte(raw))
		if subtle.ConstantTimeCompare(got[:], s.operatorHash[:]) == 1 {
			return Identity{Kind: "operator", Admin: true}, nil
		}
	}
	return s.Authenticate(ctx, raw)
}

// IssueOwner creates a credential-only owner and its primary token.
func (s *Store) IssueOwner(ctx context.Context) (string, Identity, error) {
	ownerID, err := randomID()
	if err != nil {
		return "", Identity{}, err
	}
	raw, err := newToken()
	if err != nil {
		return "", Identity{}, err
	}
	encoded, err := s.encrypt(raw, ownerID, "primary")
	if err != nil {
		return "", Identity{}, err
	}
	hash := tokenHash(raw)
	var tokenID int64
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO owners(id) VALUES (?)`, ownerID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id, kind, token_hash, token_prefix, token_enc) VALUES (?, 'primary', ?, ?, ?)`, ownerID, hash, tokenPrefix(raw), encoded)
		if err != nil {
			return err
		}
		tokenID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return audit(tx, ctx, "token.created", ownerID, tokenPrefix(raw), "issue")
	})
	if err != nil {
		return "", Identity{}, err
	}
	return raw, Identity{OwnerID: ownerID, TokenID: tokenID, Kind: "primary"}, nil
}

// Restore accepts only a valid primary token, never a linked automation token.
func (s *Store) Restore(ctx context.Context, raw string) (Identity, error) {
	identity, err := s.Authenticate(ctx, raw)
	if err != nil || identity.Kind != "primary" {
		return Identity{}, ErrInvalidToken
	}
	if err := s.auditAction(ctx, "token.restored", identity.OwnerID, tokenPrefix(raw), "restore"); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (s *Store) List(ctx context.Context, ownerID string) ([]Token, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, kind, COALESCE(name,''), token_prefix, created_at, last_used_at, expires_at, revoked_at FROM api_tokens WHERE owner_id = ? ORDER BY CASE kind WHEN 'primary' THEN 0 ELSE 1 END, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tokens []Token
	now := time.Now().Unix()
	for rows.Next() {
		var token Token
		var created int64
		var lastUsed, expires, revoked sql.NullInt64
		if err := rows.Scan(&token.ID, &token.Kind, &token.Name, &token.Prefix, &created, &lastUsed, &expires, &revoked); err != nil {
			return nil, err
		}
		token.CreatedAt = time.Unix(created, 0).UTC()
		if lastUsed.Valid {
			value := time.Unix(lastUsed.Int64, 0).UTC()
			token.LastUsedAt = &value
		}
		if expires.Valid {
			value := time.Unix(expires.Int64, 0).UTC()
			token.ExpiresAt = &value
		}
		if revoked.Valid {
			value := time.Unix(revoked.Int64, 0).UTC()
			token.RevokedAt = &value
		}
		switch {
		case token.RevokedAt != nil:
			token.Status = "revoked"
		case token.ExpiresAt != nil && token.ExpiresAt.Unix() <= now:
			token.Status = "expired"
		default:
			token.Status = "active"
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// ListAll returns token metadata for operator maintenance; token values and hashes are omitted.
func (s *Store) ListAll(ctx context.Context) ([]Token, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, owner_id, kind, COALESCE(name,''), token_prefix, created_at, last_used_at, expires_at, revoked_at FROM api_tokens ORDER BY owner_id, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tokens []Token
	now := time.Now().Unix()
	for rows.Next() {
		var token Token
		var created int64
		var lastUsed, expires, revoked sql.NullInt64
		if err := rows.Scan(&token.ID, &token.OwnerID, &token.Kind, &token.Name, &token.Prefix, &created, &lastUsed, &expires, &revoked); err != nil {
			return nil, err
		}
		token.CreatedAt = time.Unix(created, 0).UTC()
		if lastUsed.Valid {
			value := time.Unix(lastUsed.Int64, 0).UTC()
			token.LastUsedAt = &value
		}
		if expires.Valid {
			value := time.Unix(expires.Int64, 0).UTC()
			token.ExpiresAt = &value
		}
		if revoked.Valid {
			value := time.Unix(revoked.Int64, 0).UTC()
			token.RevokedAt = &value
		}
		switch {
		case token.RevokedAt != nil:
			token.Status = "revoked"
		case token.ExpiresAt != nil && token.ExpiresAt.Unix() <= now:
			token.Status = "expired"
		default:
			token.Status = "active"
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// RevokeByPrefix revokes one linked token or the complete owner represented by a primary token.
func (s *Store) RevokeByPrefix(ctx context.Context, prefix string) error {
	var id int64
	var ownerID, kind string
	if err := s.db.Read().QueryRowContext(ctx, `SELECT id, owner_id, kind FROM api_tokens WHERE token_prefix = ? AND revoked_at IS NULL`, prefix).Scan(&id, &ownerID, &kind); err != nil {
		return ErrNotFound
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if kind == "primary" {
			if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'operator_revoked' WHERE owner_id = ? AND revoked_at IS NULL`, time.Now().Unix(), ownerID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'operator_revoked' WHERE id = ? AND revoked_at IS NULL`, time.Now().Unix(), id); err != nil {
			return err
		}
		return audit(tx, ctx, "token.revoked", ownerID, prefix, "operator")
	})
}

func (s *Store) Reveal(ctx context.Context, ownerID string, tokenID int64) (Token, error) {
	var token Token
	var created int64
	var encoded string
	var lastUsed, expires, revoked sql.NullInt64
	err := s.db.Read().QueryRowContext(ctx, `SELECT id, kind, COALESCE(name,''), token_prefix, token_enc, created_at, last_used_at, expires_at, revoked_at FROM api_tokens WHERE owner_id = ? AND id = ?`, ownerID, tokenID).Scan(&token.ID, &token.Kind, &token.Name, &token.Prefix, &encoded, &created, &lastUsed, &expires, &revoked)
	if err != nil {
		return Token{}, ErrNotFound
	}
	value, err := s.decrypt(encoded, ownerID, token.Kind)
	if err != nil {
		return Token{}, err
	}
	token.Value = value
	token.CreatedAt = time.Unix(created, 0).UTC()
	if lastUsed.Valid {
		v := time.Unix(lastUsed.Int64, 0).UTC()
		token.LastUsedAt = &v
	}
	if expires.Valid {
		v := time.Unix(expires.Int64, 0).UTC()
		token.ExpiresAt = &v
	}
	if revoked.Valid {
		v := time.Unix(revoked.Int64, 0).UTC()
		token.RevokedAt = &v
	}
	if err := s.auditAction(ctx, "token.revealed", ownerID, token.Prefix, "reveal"); err != nil {
		return Token{}, err
	}
	return token, nil
}

// MaskedValue returns a token's required masked display without recording a reveal event.
func (s *Store) MaskedValue(ctx context.Context, ownerID string, tokenID int64) (string, error) {
	var kind, encoded string
	if err := s.db.Read().QueryRowContext(ctx, `SELECT kind, token_enc FROM api_tokens WHERE owner_id = ? AND id = ?`, ownerID, tokenID).Scan(&kind, &encoded); err != nil {
		return "", ErrNotFound
	}
	raw, err := s.decrypt(encoded, ownerID, kind)
	if err != nil {
		return "", err
	}
	if len(raw) < 4 {
		return "", ErrInvalidToken
	}
	return "own_••••••••••••••••••••••••" + raw[len(raw)-4:], nil
}

func (s *Store) Rotate(ctx context.Context, ownerID string, tokenID int64) (string, Token, error) {
	var kind, name string
	var expires sql.NullInt64
	if err := s.db.Read().QueryRowContext(ctx, `SELECT kind, COALESCE(name,''), expires_at FROM api_tokens WHERE owner_id = ? AND id = ? AND revoked_at IS NULL`, ownerID, tokenID).Scan(&kind, &name, &expires); err != nil {
		return "", Token{}, ErrNotFound
	}
	raw, err := newToken()
	if err != nil {
		return "", Token{}, err
	}
	encoded, err := s.encrypt(raw, ownerID, kind)
	if err != nil {
		return "", Token{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ?, token_prefix = ?, token_enc = ?, rotated_at = ?, last_used_at = NULL WHERE owner_id = ? AND id = ? AND revoked_at IS NULL`, tokenHash(raw), tokenPrefix(raw), encoded, time.Now().Unix(), ownerID, tokenID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return ErrNotFound
		}
		return audit(tx, ctx, "token.rotated", ownerID, tokenPrefix(raw), "rotate")
	})
	if err != nil {
		return "", Token{}, err
	}
	token := Token{ID: tokenID, Kind: kind, Name: name, Prefix: tokenPrefix(raw), Value: raw, Status: "active", CreatedAt: time.Now().UTC()}
	if expires.Valid {
		v := time.Unix(expires.Int64, 0).UTC()
		token.ExpiresAt = &v
	}
	return raw, token, nil
}

func (s *Store) CreateLinked(ctx context.Context, ownerID, name string, expiresAt *time.Time) (string, Token, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 64 {
		return "", Token{}, errors.New("name must be 1 to 64 characters")
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return "", Token{}, errors.New("expiry must be in the future")
	}
	raw, err := newToken()
	if err != nil {
		return "", Token{}, err
	}
	encoded, err := s.encrypt(raw, ownerID, "linked")
	if err != nil {
		return "", Token{}, err
	}
	var id int64
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var count int
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND name = ?`, ownerID, name).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate > 0 {
			return errors.New("token name must be unique for this owner")
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, ownerID, time.Now().Unix()).Scan(&count); err != nil {
			return err
		}
		if count >= s.maxLinked {
			return ErrTokenLimit
		}
		var expiry any
		if expiresAt != nil {
			expiry = expiresAt.UTC().Unix()
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id, kind, name, token_hash, token_prefix, token_enc, expires_at) VALUES (?, 'linked', ?, ?, ?, ?, ?)`, ownerID, name, tokenHash(raw), tokenPrefix(raw), encoded, expiry)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return audit(tx, ctx, "token.created", ownerID, tokenPrefix(raw), "linked")
	})
	if err != nil {
		return "", Token{}, err
	}
	token := Token{ID: id, Kind: "linked", Name: name, Prefix: tokenPrefix(raw), Value: raw, CreatedAt: time.Now().UTC(), Status: "active"}
	if expiresAt != nil {
		v := expiresAt.UTC()
		token.ExpiresAt = &v
	}
	return raw, token, nil
}

func (s *Store) UpdateLinked(ctx context.Context, ownerID string, tokenID int64, name string, expiresAt *time.Time) error {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 64 {
		return errors.New("name must be 1 to 64 characters")
	}
	var expiry any
	if expiresAt != nil {
		if !expiresAt.After(time.Now()) {
			return errors.New("expiry must be in the future")
		}
		expiry = expiresAt.UTC().Unix()
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND name = ? AND id <> ?`, ownerID, name, tokenID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate > 0 {
			return errors.New("token name must be unique for this owner")
		}
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET name = ?, expires_at = ? WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, name, expiry, tokenID, ownerID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return ErrNotFound
		}
		return audit(tx, ctx, "token.updated", ownerID, fmt.Sprint(tokenID), "update")
	})
}

func (s *Store) RevokeLinked(ctx context.Context, ownerID string, tokenID int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var prefix string
		if err := tx.QueryRowContext(ctx, `SELECT token_prefix FROM api_tokens WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, tokenID, ownerID).Scan(&prefix); err != nil {
			return ErrNotFound
		}
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'owner_revoked' WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, time.Now().Unix(), tokenID, ownerID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return ErrNotFound
		}
		return audit(tx, ctx, "token.revoked", ownerID, prefix, "owner_revoked")
	})
}

func (s *Store) IsPrimary(identity Identity) bool {
	return identity.OwnerID != "" && identity.Kind == "primary"
}

func (s *Store) auditAction(ctx context.Context, action, ownerID, prefix, source string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error { return audit(tx, ctx, action, ownerID, prefix, source) })
}

func audit(tx *sql.Tx, ctx context.Context, action, ownerID, prefix, source string) error {
	details := fmt.Sprintf(`{"prefix":%q,"source":%q}`, prefix, source)
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level, category, action, target_type, target_id, details, success) VALUES ('info','security',?,'owner',?,?,1)`, action, ownerID, details)
	return err
}

func (s *Store) encrypt(raw, ownerID, kind string) (string, error) {
	block, err := aes.NewCipher(s.encryptionKey)
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
	sealed := gcm.Seal(nonce, nonce, []byte(raw), []byte(ownerID+":"+kind))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Store) decrypt(encoded, ownerID, kind string) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted token")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(ownerID+":"+kind))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func tokenPrefix(raw string) string {
	if len(raw) > 12 {
		return raw[:12]
	}
	return raw
}

func newToken() (string, error) {
	const length = 32
	var output strings.Builder
	output.Grow(4 + length)
	output.WriteString("own_")
	for output.Len() < 4+length {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(len(tokenAlphabet))))
		if err != nil {
			return "", err
		}
		output.WriteByte(tokenAlphabet[value.Int64()])
	}
	return output.String(), nil
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
