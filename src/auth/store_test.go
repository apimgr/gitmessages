package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	operator := "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := Open(filepath.Join(projectTestDir(t), "server.db"), operator, key, 25, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, operator
}

func TestIssueAuthenticateAndOperatorToken(t *testing.T) {
	store, operator := testStore(t)
	ctx := context.Background()
	raw, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != len("own_")+32 {
		t.Fatalf("owner token length = %d", len(raw))
	}
	got, err := store.Authenticate(ctx, raw)
	if err != nil || got.OwnerID != identity.OwnerID || got.Kind != "primary" {
		t.Fatalf("authenticate primary: %#v, %v", got, err)
	}
	if _, err := store.Authenticate(ctx, "own_invalid"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token error = %v", err)
	}
	admin, err := store.AuthenticateBearer(ctx, operator)
	if err != nil || !admin.Admin || admin.Kind != "operator" {
		t.Fatalf("authenticate operator: %#v, %v", admin, err)
	}
	if _, err := store.AuthenticateBearer(ctx, operator+"x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong operator token error = %v", err)
	}
}

func TestAuthenticationObserverUsesBoundedMethodAndStatusValues(t *testing.T) {
	store, operator := testStore(t)
	var events []struct {
		method  string
		success bool
	}
	store.SetAuthObserver(func(method string, success bool) {
		events = append(events, struct {
			method  string
			success bool
		}{method: method, success: success})
	})
	ownerToken, _, err := store.IssueOwner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateBearer(context.Background(), ownerToken); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateBearer(context.Background(), "own_invalid"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid owner token error = %v", err)
	}
	if _, err := store.AuthenticateBearer(context.Background(), operator); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateBearer(context.Background(), operator+"x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid operator token error = %v", err)
	}
	want := []struct {
		method  string
		success bool
	}{{"owner_token", true}, {"owner_token", false}, {"operator_token", true}, {"operator_token", false}}
	if len(events) != len(want) {
		t.Fatalf("authentication events = %+v, want %+v", events, want)
	}
	for index, event := range events {
		if event != want[index] {
			t.Errorf("authentication event %d = %+v, want %+v", index, event, want[index])
		}
	}
}

func TestLinkedTokenManagementRotationRevocationAndRestore(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linked, linkedToken, err := store.CreateLinked(ctx, identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Kind != "primary" || linkedToken.Kind != "linked" || store.IsPrimary(identity) == false {
		t.Fatal("primary identity was not recognized")
	}
	linkedIdentity, err := store.Authenticate(ctx, linked)
	if err != nil || store.IsPrimary(linkedIdentity) {
		t.Fatalf("linked token identity: %#v, %v", linkedIdentity, err)
	}
	if _, err := store.Restore(ctx, linked); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("linked token restored identity: %v", err)
	}

	rotatedPrimary, _, err := store.Rotate(ctx, identity.OwnerID, identity.TokenID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, primary); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old primary remained valid: %v", err)
	}
	if _, err := store.Authenticate(ctx, linked); err != nil {
		t.Fatalf("primary rotation invalidated linked token: %v", err)
	}
	if _, err := store.Restore(ctx, rotatedPrimary); err != nil {
		t.Fatalf("restore primary: %v", err)
	}

	rotatedLinked, _, err := store.Rotate(ctx, identity.OwnerID, linkedToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, linked); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old linked token remained valid: %v", err)
	}
	if _, err := store.Authenticate(ctx, rotatedPrimary); err != nil {
		t.Fatalf("linked rotation invalidated primary: %v", err)
	}
	if _, err := store.Authenticate(ctx, rotatedLinked); err != nil {
		t.Fatalf("rotated linked token invalid: %v", err)
	}
	if err := store.RevokeLinked(ctx, identity.OwnerID, linkedToken.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, rotatedLinked); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked linked token remained valid: %v", err)
	}
}

func TestExpiredLinkedTokenStaysListedButCannotAuthenticate(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	_, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	raw, token, err := store.CreateLinked(ctx, identity.OwnerID, "expires", &expires)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).Unix(), token.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired linked token authenticated: %v", err)
	}
	tokens, err := store.List(ctx, identity.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tokens {
		if listed.ID == token.ID && listed.Status == "expired" {
			return
		}
	}
	t.Fatal("expired linked token was not retained as expired")
}

func TestOperatorRevokingPrimaryRevokesWholeOwner(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linked, _, err := store.CreateLinked(ctx, identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeByPrefix(ctx, tokenPrefix(primary)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{primary, linked} {
		if _, err := store.Authenticate(ctx, raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("owner token remained valid after primary revocation: %v", err)
		}
	}
	var owners, tokens int
	if err := store.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id = ?`, identity.OwnerID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, identity.OwnerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || tokens != 0 {
		t.Fatalf("primary revocation left owner or tokens: owners=%d tokens=%d", owners, tokens)
	}
	var details string
	if err := store.db.Read().QueryRowContext(ctx, `SELECT details FROM audit_log WHERE action = 'token.revoked' AND target_id = ?`, identity.OwnerID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"scope":"owner"`, `"tokens":2`, `"resources":0`, `"source":"cli"`} {
		if !strings.Contains(details, expected) {
			t.Errorf("primary revocation audit omitted %s: %s", expected, details)
		}
	}
}

func TestTokenLastFourIsStoredBackfilledAndMaskedWithoutDecryption(t *testing.T) {
	store, operator := testStore(t)
	ctx := context.Background()
	raw, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_last4 = '' WHERE id = ?`, identity.TokenID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	migrated, err := NewStore(store.db, operator, key, 25, 0, time.Hour)
	if err != nil {
		t.Fatalf("backfill token suffix: %v", err)
	}
	tokens, err := migrated.List(ctx, identity.OwnerID)
	if err != nil || len(tokens) != 1 || tokens[0].Last4 != raw[len(raw)-4:] {
		t.Fatalf("migrated token suffix = %#v, %v", tokens, err)
	}
	if err := store.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_enc = 'invalid-ciphertext' WHERE id = ?`, identity.TokenID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	masked, err := migrated.MaskedValue(ctx, identity.OwnerID, identity.TokenID)
	if err != nil || !strings.HasSuffix(masked, raw[len(raw)-4:]) {
		t.Fatalf("masked value depended on decrypting the token: %q, %v", masked, err)
	}
}

func TestRevealMasksAndUpdatesLinkedTokens(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revealed, err := store.Reveal(ctx, identity.OwnerID, identity.TokenID)
	if err != nil || revealed.Value != primary {
		t.Fatalf("Reveal() = %#v, %v", revealed, err)
	}
	masked, err := store.MaskedValue(ctx, identity.OwnerID, identity.TokenID)
	if err != nil || !strings.HasSuffix(masked, primary[len(primary)-4:]) || strings.Contains(masked, primary[:12]) {
		t.Fatalf("MaskedValue() = %q, %v", masked, err)
	}
	linked, linkedToken, err := store.CreateLinked(ctx, identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(24 * time.Hour)
	if err := store.UpdateLinked(ctx, identity.OwnerID, linkedToken.ID, "release bot", &expires); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, token := range all {
		if token.ID == linkedToken.ID {
			found = token.OwnerID == identity.OwnerID && token.Name == "release bot" && token.ExpiresAt != nil && token.Value == ""
		}
	}
	if !found {
		t.Fatalf("ListAll() omitted safe metadata for linked token: %#v", all)
	}
	if _, err := store.Authenticate(ctx, linked); err != nil {
		t.Fatalf("updating linked token metadata invalidated it: %v", err)
	}
}

func TestUpdateLinkedRejectsInvalidAndDuplicateChanges(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	_, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := store.CreateLinked(ctx, identity.OwnerID, "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := store.CreateLinked(ctx, identity.OwnerID, "second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateLinked(ctx, identity.OwnerID, first.ID, " ", nil); err == nil {
		t.Fatal("empty linked-token name was accepted")
	}
	past := time.Now().Add(-time.Minute)
	if err := store.UpdateLinked(ctx, identity.OwnerID, first.ID, "first", &past); err == nil {
		t.Fatal("past linked-token expiry was accepted")
	}
	if err := store.UpdateLinked(ctx, identity.OwnerID, first.ID, "second", nil); err == nil {
		t.Fatal("duplicate linked-token name was accepted")
	}
	if err := store.UpdateLinked(ctx, identity.OwnerID, second.ID+100, "missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token update error = %v", err)
	}
}

func TestIdleOwnerPurgeAndStoreAccessors(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	if store.Database() != store.db || store.CookieMaxAge() != time.Hour {
		t.Fatal("store accessors did not return their configured values")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	store.RunCleanup(stopped)
	if count, err := store.PurgeIdleOwners(ctx); err != nil || count != 0 {
		t.Fatalf("disabled idle purge = %d, %v", count, err)
	}
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE owners SET last_seen_at = ? WHERE id = ?`, time.Now().Add(-48*time.Hour).Unix(), identity.OwnerID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	store.idlePurgeDays = 1
	if count, err := store.PurgeIdleOwners(ctx); err != nil || count != 1 {
		t.Fatalf("idle purge = %d, %v; want 1 owner", count, err)
	}
	store.RunCleanup(stopped)
	if _, err := store.Authenticate(ctx, primary); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("purged owner's token state was unexpected: %v", err)
	}
}

func TestReencryptTokenDataMovesOwnerAndLinkedCopiesToCurrentKey(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linked, _, err := store.CreateLinked(ctx, identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := decodeEncryptionKey(base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	current := []byte("abcdef0123456789abcdef0123456789")
	if err := ReencryptTokenData(ctx, store.db, previous, current); err != nil {
		t.Fatal(err)
	}
	rows, err := store.db.Read().QueryContext(ctx, `SELECT owner_id,kind,token_enc FROM api_tokens ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]string{}
	for rows.Next() {
		var owner, kind, encrypted string
		if err := rows.Scan(&owner, &kind, &encrypted); err != nil {
			t.Fatal(err)
		}
		plain, err := decryptTokenWithKey(current, encrypted, owner, kind)
		if err != nil {
			t.Fatalf("current key cannot open %s token: %v", kind, err)
		}
		seen[kind] = plain
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen["primary"] != primary || seen["linked"] != linked {
		t.Fatalf("re-encrypted token values changed: %#v", seen)
	}
}

func TestReloadEncryptionKeyReadsGraceKeyAndReencryptsOnReveal(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	primary, identity, err := store.IssueOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous := []byte("0123456789abcdef0123456789abcdef")
	current := []byte("abcdef0123456789abcdef0123456789")
	if err := store.db.StorePreviousEncryptionKey(ctx, previous, current, 30*24*time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.ReloadEncryptionKey(ctx, base64.RawURLEncoding.EncodeToString(current), 2); err != nil {
		t.Fatal(err)
	}
	var tokenID int64
	if err := store.db.Read().QueryRowContext(ctx, `SELECT id FROM api_tokens WHERE owner_id=? AND kind='primary'`, identity.OwnerID).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	revealed, err := store.Reveal(ctx, identity.OwnerID, tokenID)
	if err != nil || revealed.Value != primary {
		t.Fatalf("grace-key token reveal = %q, %v", revealed.Value, err)
	}
	var encrypted string
	if err := store.db.Read().QueryRowContext(ctx, `SELECT token_enc FROM api_tokens WHERE id=?`, tokenID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptTokenWithKey(current, encrypted, identity.OwnerID, "primary"); err != nil {
		t.Fatalf("revealed token was not re-encrypted under current key: %v", err)
	}
}
