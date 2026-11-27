package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/contact"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/pgp"
	"github.com/apimgr/gitmessages/src/web"
)

type pgpKeyserverState struct {
	Fingerprint string                 `json:"fingerprint"`
	Servers     []pgpKeyserverProgress `json:"servers"`
}

type pgpKeyserverProgress struct {
	Host       string     `json:"host"`
	Attempts   int        `json:"attempts"`
	NextRetry  *time.Time `json:"next_retry,omitempty"`
	Published  *time.Time `json:"published_at,omitempty"`
	LastStatus int        `json:"last_status,omitempty"`
}

func handlePGPMaintenance(args []string, configDir, databaseDir string, configuration *config.Config) error {
	if len(args) == 0 {
		return errors.New("usage: --maintenance pgp generate|rotate|publish|export|import|delete")
	}
	store, err := database.OpenConfigured(configuration.Server.Database, filepath.Join(databaseDir, "server.db"), 5)
	if err != nil {
		return fmt.Errorf("open PGP maintenance database: %w", err)
	}
	defer func() { _ = store.Close() }()
	identity, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, configuration.Server.Tokens.MaxLinked, configuration.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(configuration.Server.Tokens.CookieMaxAge))
	if err != nil {
		return fmt.Errorf("initialize PGP maintenance authorization: %w", err)
	}
	if err := authorizeTokenMaintenance(identity); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	directory := filepath.Join(configDir, "security")
	secret, err := contact.InstallationSecret(ctx, store)
	if err != nil {
		return err
	}
	switch args[0] {
	case "generate":
		if len(args) != 1 {
			return errors.New("usage: --maintenance pgp generate")
		}
		pair, err := generatePGPKey(configuration, secret, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := pgp.WriteCurrent(directory, pair); err != nil {
			return err
		}
		if err := persistPGPMetadata(ctx, store, pair.Metadata, nil, false); err != nil {
			return err
		}
		if err := recordPGPEvent(ctx, store, "security.pgp_generated", pair.Metadata.Fingerprint); err != nil {
			return err
		}
		fmt.Printf("Generated PGP key %s\n", pair.Metadata.Fingerprint)
		if !configuration.Web.Security.PublishPGPKey {
			return nil
		}
		return publishPGPKey(ctx, store, configuration, directory)
	case "rotate":
		if len(args) != 1 {
			return errors.New("usage: --maintenance pgp rotate")
		}
		unlock := contact.LockSecurityReportPipeline()
		defer unlock()
		return rotatePGPKey(ctx, store, configuration, directory, secret)
	case "publish":
		if len(args) != 1 {
			return errors.New("usage: --maintenance pgp publish")
		}
		if !configuration.Web.Security.PublishPGPKey {
			return nil
		}
		return publishPGPKey(ctx, store, configuration, directory)
	case "export":
		return exportPGPKey(ctx, args[1:], store, directory, secret, identity, configuration)
	case "import":
		return importPGPKey(ctx, args[1:], store, directory, secret, identity, configuration)
	case "delete":
		if len(args) != 1 {
			return errors.New("usage: --maintenance pgp delete")
		}
		if err := authorizeSensitivePGPOperation(identity, configuration, "Delete the project PGP keypair"); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Deleting the PGP key makes retained PGP-encrypted reports unreadable. Type DELETE PGP to continue: ")
		var confirmation string
		if _, err := fmt.Fscanln(os.Stdin, &confirmation); err != nil || confirmation != "DELETE PGP" {
			return errors.New("PGP deletion cancelled")
		}
		if err := deletePGPKey(ctx, store, configuration, directory); err != nil {
			return err
		}
		return recordPGPEvent(ctx, store, "security.pgp_deleted", "keypair deleted")
	default:
		return fmt.Errorf("unknown PGP maintenance action %q", args[0])
	}
}

func generatePGPKey(configuration *config.Config, secret []byte, now time.Time) (pgp.KeyPair, error) {
	name := strings.TrimSpace(configuration.Server.Branding.Title)
	if name == "" {
		name = "Git Messages"
	}
	email := strings.TrimSpace(configuration.Server.Contact.Security.Email)
	if email == "" {
		host := web.ResolveStartupFQDN()
		if host == "" {
			host = "gitmessages.invalid"
		}
		email = "security@" + host
	}
	return pgp.Generate(name+" Security", email, secret, now)
}

func rotatePGPKey(ctx context.Context, store *database.Store, configuration *config.Config, directory string, secret []byte) error {
	privatePath := filepath.Join(directory, "pgp.priv.asc.enc")
	publicPath := filepath.Join(directory, "pgp.pub.asc")
	oldPrivate, err := readPGPFile(privatePath)
	if err != nil {
		return fmt.Errorf("load current PGP private key: %w", err)
	}
	oldPublic, err := readPGPFile(publicPath)
	if err != nil {
		return fmt.Errorf("load current PGP public key: %w", err)
	}
	oldMetadata, err := pgp.MetadataForPublic(oldPublic)
	if err != nil {
		return err
	}
	oldFile := filepath.Join(directory, "pgp.priv.old.asc.enc")
	if info, err := os.Stat(oldFile); err == nil {
		if time.Since(info.ModTime()) < 30*24*time.Hour {
			return errors.New("cannot rotate while the prior PGP private key is still within its 30-day retention")
		}
		if err := os.Remove(oldFile); err != nil {
			return fmt.Errorf("remove expired prior PGP private key: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect prior PGP private key: %w", err)
	}
	pair, err := pgp.Rotate(oldPrivate, configuration.Server.Branding.Title+" Security", pgpContactEmail(configuration), secret, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := pgp.VerifyPublicKeyCertification(pair.Public, oldPublic); err != nil {
		return err
	}
	if err := pgp.WriteStaged(directory, pair); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = pgp.DiscardStaged(directory)
		}
	}()
	rows, err := store.Read().QueryContext(ctx, `SELECT tracking_id,body_enc FROM security_reports WHERE body_alg='pgp' ORDER BY tracking_id`)
	if err != nil {
		return fmt.Errorf("read PGP-encrypted reports for re-encryption: %w", err)
	}
	type encryptedReport struct {
		id      string
		content []byte
	}
	var reports []encryptedReport
	for rows.Next() {
		var report encryptedReport
		if err := rows.Scan(&report.id, &report.content); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read encrypted report: %w", err)
		}
		plain, err := pgp.Decrypt(report.content, oldPrivate, secret)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("decrypt retained security report %s: %w", report.id, err)
		}
		report.content, err = pgp.Encrypt(pair.Public, plain)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("reencrypt retained security report %s: %w", report.id, err)
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate encrypted security reports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close encrypted security reports: %w", err)
	}
	rotatedAt := time.Now().UTC().Format(time.RFC3339)
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		for _, report := range reports {
			if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET body_enc=?,updated_at=? WHERE tracking_id=? AND body_alg='pgp'`, report.content, rotatedAt, report.id); err != nil {
				return fmt.Errorf("update encrypted report %s: %w", report.id, err)
			}
		}
		return writePGPMetadataTx(ctx, tx, pair.Metadata, &oldMetadata, false, []string{})
	}); err != nil {
		return fmt.Errorf("commit PGP key rotation: %w", err)
	}
	committed = true
	recovered, err := pgp.RecoverStaged(directory, pair.Metadata.Fingerprint, secret)
	if err != nil || !recovered {
		return fmt.Errorf("activate committed PGP key rotation: %w", err)
	}
	if err := recordPGPEvent(ctx, store, "security.pgp_rotated", pair.Metadata.Fingerprint); err != nil {
		return err
	}
	return publishPGPKey(ctx, store, configuration, directory)
}

func pgpContactEmail(configuration *config.Config) string {
	if email := strings.TrimSpace(configuration.Server.Contact.Security.Email); email != "" {
		return email
	}
	host := web.ResolveStartupFQDN()
	if host == "" {
		host = "gitmessages.invalid"
	}
	return "security@" + host
}

func exportPGPKey(ctx context.Context, args []string, store *database.Store, directory string, secret []byte, authStore *auth.Store, configuration *config.Config) error {
	if len(args) == 0 || len(args) > 2 || args[0] != "public" && args[0] != "private" {
		return errors.New("usage: --maintenance pgp export public [path] | private <path>")
	}
	keyType := args[0]
	path := ""
	if len(args) == 2 {
		path = args[1]
	}
	if keyType == "public" {
		key, err := readPGPFile(filepath.Join(directory, "pgp.pub.asc"))
		if err != nil {
			return err
		}
		if path == "" || path == "-" {
			_, err = os.Stdout.Write(key)
			return err
		}
		return writePGPExport(path, key, 0o644)
	}
	if path == "" || path == "-" {
		return errors.New("private key export requires a file path")
	}
	if err := authorizeSensitivePGPOperation(authStore, configuration, "Export the project PGP private key"); err != nil {
		return err
	}
	var reason string
	fmt.Fprint(os.Stderr, "Reason for private-key export: ")
	var err error
	reason, err = bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && reason == "" || strings.TrimSpace(reason) == "" {
		return errors.New("a reason is required for private-key export")
	}
	reason = strings.TrimSpace(reason)
	var recent int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE category='security' AND action='security.private_key_exported' AND timestamp>=?`, time.Now().Add(-time.Hour).Unix()).Scan(&recent); err != nil {
		return fmt.Errorf("check private-key export rate limit: %w", err)
	}
	if recent > 0 {
		return errors.New("private-key export is limited to one per hour")
	}
	encrypted, err := readPGPFile(filepath.Join(directory, "pgp.priv.asc.enc"))
	if err != nil {
		return err
	}
	private, err := pgp.ExportPrivate(encrypted, secret)
	if err != nil {
		return err
	}
	if err := writePGPExport(path, private, 0o600); err != nil {
		return err
	}
	return recordPGPEvent(ctx, store, "security.private_key_exported", reason)
}

func importPGPKey(ctx context.Context, args []string, store *database.Store, directory string, secret []byte, authStore *auth.Store, configuration *config.Config) error {
	if len(args) != 1 {
		return errors.New("usage: --maintenance pgp import <file>")
	}
	if err := authorizeSensitivePGPOperation(authStore, configuration, "Import a project PGP private key"); err != nil {
		return err
	}
	private, err := readPGPFile(args[0])
	if err != nil {
		return err
	}
	pair, err := pgp.Import(private, secret)
	if err != nil {
		return err
	}
	brand := strings.TrimSpace(configuration.Server.Branding.Title)
	if brand == "" {
		brand = "Git Messages"
	}
	expectedIdentity := brand + " Security <" + pgpContactEmail(configuration) + ">"
	if pair.Metadata.Identity != expectedIdentity {
		fmt.Fprintf(os.Stderr, "Warning: imported identity %q does not match expected identity %q. Type IMPORT KEY to continue: ", pair.Metadata.Identity, expectedIdentity)
		var confirmation string
		if _, err := fmt.Fscanln(os.Stdin, &confirmation); err != nil || confirmation != "IMPORT KEY" {
			return errors.New("PGP import identity mismatch; import cancelled")
		}
	}
	if current, err := readPGPFile(filepath.Join(directory, "pgp.pub.asc")); err == nil {
		metadata, parseErr := pgp.MetadataForPublic(current)
		if parseErr != nil || metadata.Fingerprint != pair.Metadata.Fingerprint {
			return errors.New("imported PGP identity differs from the active key; rotate or delete the active key before import")
		}
		return errors.New("the matching PGP key is already installed")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := pgp.WriteCurrent(directory, pair); err != nil {
		return err
	}
	if err := persistPGPMetadata(ctx, store, pair.Metadata, nil, false); err != nil {
		return err
	}
	if err := recordPGPEvent(ctx, store, "security.pgp_imported", pair.Metadata.Fingerprint); err != nil {
		return err
	}
	return publishPGPKey(ctx, store, configuration, directory)
}

func deletePGPKey(ctx context.Context, store *database.Store, configuration *config.Config, directory string) error {
	paths := []string{"pgp.pub.asc", "pgp.priv.asc.enc", "pgp.priv.old.asc.enc", "pgp.pub.asc.new", "pgp.priv.asc.enc.new"}
	for _, name := range paths {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove PGP file %s: %w", name, err)
		}
	}
	configuration.Web.Security.PublishPGPKey = false
	if err := config.SaveWebSecurity(); err != nil {
		return fmt.Errorf("disable PGP publication: %w", err)
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE pgp_key_metadata SET revoked=1,keyservers_published='[]' WHERE id=1`)
		return err
	})
}

func publishPGPKey(ctx context.Context, store *database.Store, configuration *config.Config, directory string) error {
	if !configuration.Web.Security.PublishPGPKey {
		return nil
	}
	statePath := filepath.Join(directory, "keyservers.state")
	state := pgpKeyserverState{}
	if existing, err := readPGPFile(statePath); err == nil {
		if json.Unmarshal(existing, &state) != nil {
			return errors.New("keyserver state file is invalid")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	public, err := readPGPFile(filepath.Join(directory, "pgp.pub.asc"))
	if err != nil {
		return err
	}
	metadata, err := pgp.MetadataForPublic(public)
	if err != nil {
		return err
	}
	if state.Fingerprint != metadata.Fingerprint {
		state = pgpKeyserverState{Fingerprint: metadata.Fingerprint}
	}
	for _, host := range configuration.Web.Security.Keyservers {
		if !validPGPKeyserverHost(host) {
			return fmt.Errorf("invalid PGP keyserver host %q", host)
		}
		progress := keyserverProgress(&state, host)
		if progress.Published != nil || progress.Attempts >= 8 || progress.NextRetry != nil && time.Now().Before(*progress.NextRetry) {
			continue
		}
		status, publishErr := submitPGPKey(ctx, host, public)
		progress.Attempts++
		progress.LastStatus = status
		if publishErr == nil && status == http.StatusOK {
			now := time.Now().UTC()
			progress.Published = &now
			progress.NextRetry = nil
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 || status == 0 {
			if progress.Attempts < 8 {
				delay := time.Minute * time.Duration(1<<(progress.Attempts-1))
				if delay > 6*time.Hour {
					delay = 6 * time.Hour
				}
				next := time.Now().UTC().Add(delay)
				progress.NextRetry = &next
			}
		} else {
			progress.Attempts = 8
			progress.NextRetry = nil
			if eventErr := recordPGPEvent(ctx, store, "security.pgp_keyserver_rejected", fmt.Sprintf("host=%s status=%d", host, status)); eventErr != nil {
				return eventErr
			}
		}
		if publishErr != nil && (status == http.StatusTooManyRequests || status >= 500 || status == 0) {
			if eventErr := recordPGPEvent(ctx, store, "security.pgp_keyserver_retry", fmt.Sprintf("host=%s status=%d error=%s", host, status, publishErr)); eventErr != nil {
				return eventErr
			}
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := writePGPState(statePath, encoded); err != nil {
		return err
	}
	var published []string
	for _, item := range state.Servers {
		if item.Published != nil {
			published = append(published, item.Host+"="+item.Published.UTC().Format(time.RFC3339))
		}
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE pgp_key_metadata SET keyservers_published=? WHERE id=1 AND fingerprint=?`, marshalStringList(published), metadata.Fingerprint)
		return err
	})
}

func keyserverProgress(state *pgpKeyserverState, host string) *pgpKeyserverProgress {
	for index := range state.Servers {
		if strings.EqualFold(state.Servers[index].Host, host) {
			return &state.Servers[index]
		}
	}
	state.Servers = append(state.Servers, pgpKeyserverProgress{Host: host})
	return &state.Servers[len(state.Servers)-1]
}

func validPGPKeyserverHost(host string) bool {
	parsed, err := url.Parse("https://" + strings.TrimSpace(host))
	return err == nil && parsed.Host == host && parsed.Hostname() != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && !strings.ContainsAny(host, "\r\n\t")
}

func submitPGPKey(ctx context.Context, host string, public []byte) (int, error) {
	var endpoint, contentType string
	var body io.Reader
	if strings.EqualFold(host, "keys.openpgp.org") {
		endpoint = "https://keys.openpgp.org/vks/v1/upload"
		encoded, _ := json.Marshal(map[string]string{"keytext": string(public)})
		body = strings.NewReader(string(encoded))
		contentType = "application/json"
	} else {
		endpoint = "https://" + host + "/pks/add"
		body = strings.NewReader(url.Values{"keytext": {string(public)}}.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", contentType)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("refusing non-HTTPS PGP keyserver redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if strings.EqualFold(host, "keys.openpgp.org") && response.StatusCode == http.StatusOK {
		var result struct {
			Fingerprint string `json:"key_fpr"`
			Status      string `json:"status"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
			return response.StatusCode, fmt.Errorf("decode keyserver response: %w", err)
		}
		metadata, err := pgp.MetadataForPublic(public)
		if err != nil {
			return response.StatusCode, err
		}
		if !strings.EqualFold(result.Fingerprint, metadata.Fingerprint) || strings.TrimSpace(result.Status) == "" {
			return response.StatusCode, errors.New("keyserver response did not confirm the submitted key")
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	}
	return response.StatusCode, nil
}

func writePGPState(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".keyservers-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func authorizeSensitivePGPOperation(store *auth.Store, configuration *config.Config, operation string) error {
	if current, err := user.Current(); err == nil && current.Username == "root" {
		return nil
	}
	if err := authorizeTokenMaintenance(store); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s — re-enter operator token: ", operation)
	value, err := readMaintenanceSecret()
	if err != nil {
		return err
	}
	identity, err := store.AuthenticateBearer(context.Background(), value)
	if err != nil || !identity.Admin || subtle.ConstantTimeCompare([]byte(value), []byte(configuration.Server.Token)) != 1 {
		return errors.New("invalid operator token confirmation")
	}
	return nil
}

func readMaintenanceSecret() (string, error) {
	return promptOperatorToken(os.Stdin, os.Stderr)
}

func readPGPFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16<<20 {
		return nil, fmt.Errorf("refusing unsafe PGP file %s", path)
	}
	return os.ReadFile(path)
}

func writePGPExport(path string, data []byte, mode os.FileMode) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func persistPGPMetadata(ctx context.Context, store *database.Store, metadata pgp.Metadata, previous *pgp.Metadata, revoked bool) error {
	return store.Write(ctx, func(tx *sql.Tx) error {
		return writePGPMetadataTx(ctx, tx, metadata, previous, revoked, nil)
	})
}

func writePGPMetadataTx(ctx context.Context, tx *sql.Tx, metadata pgp.Metadata, previous *pgp.Metadata, revoked bool, published []string) error {
	var lastRotated any
	if previous != nil {
		lastRotated = time.Now().UTC().Format(time.RFC3339)
	}
	if published == nil {
		published = []string{}
	}
	encoded, err := json.Marshal(published)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pgp_key_metadata(id,fingerprint,created_at,expires_at,last_rotated_at,keyservers_published,revoked) VALUES(1,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint,created_at=excluded.created_at,expires_at=excluded.expires_at,last_rotated_at=excluded.last_rotated_at,keyservers_published=excluded.keyservers_published,revoked=excluded.revoked`, metadata.Fingerprint, metadata.CreatedAt.UTC().Format(time.RFC3339), metadata.ExpiresAt.UTC().Format(time.RFC3339), lastRotated, string(encoded), revoked)
	return err
}

func marshalStringList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func recordPGPEvent(ctx context.Context, store *database.Store, action, details string) error {
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','security',?,'local','pgp','key',?,1)`, action, details)
		return err
	})
}

func recoverPGPKeyState(ctx context.Context, store *database.Store, configDir string) error {
	var fingerprint string
	err := store.Read().QueryRowContext(ctx, `SELECT fingerprint FROM pgp_key_metadata WHERE id=1`).Scan(&fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read PGP key metadata: %w", err)
	}
	metadataMissing := errors.Is(err, sql.ErrNoRows)
	secret, err := contact.InstallationSecret(ctx, store)
	if err != nil {
		return err
	}
	directory := filepath.Join(configDir, "security")
	if metadataMissing || fingerprint == "" {
		staged, stageErr := os.Lstat(filepath.Join(directory, "pgp.pub.asc.new"))
		if errors.Is(stageErr, os.ErrNotExist) {
			public, publicErr := readPGPFile(filepath.Join(directory, "pgp.pub.asc"))
			if publicErr == nil {
				metadata, parseErr := pgp.MetadataForPublic(public)
				if parseErr != nil {
					return parseErr
				}
				if err := persistPGPMetadata(ctx, store, metadata, nil, false); err != nil {
					return err
				}
				fingerprint = metadata.Fingerprint
			} else if !errors.Is(publicErr, os.ErrNotExist) {
				return publicErr
			}
		} else if stageErr != nil {
			return stageErr
		} else {
			_ = staged
		}
	}
	recovered, err := pgp.RecoverStaged(directory, fingerprint, secret)
	if err != nil {
		return err
	}
	if recovered && fingerprint == "" {
		public, err := readPGPFile(filepath.Join(directory, "pgp.pub.asc"))
		if err == nil {
			metadata, err := pgp.MetadataForPublic(public)
			if err != nil {
				return err
			}
			return persistPGPMetadata(ctx, store, metadata, nil, false)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func recoverInstallationSecretPGPKey(ctx context.Context, store *database.Store, configDir string) error {
	stage := filepath.Join(configDir, "security", "pgp.priv.secret-new")
	if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	secret, err := contact.InstallationSecret(ctx, store)
	if err != nil {
		return err
	}
	staged, err := readPGPFile(stage)
	if err != nil {
		return err
	}
	if _, err := pgp.DecryptPrivate(staged, secret); err != nil {
		if removeErr := os.Remove(stage); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, removeErr)
		}
		return nil
	}
	active := filepath.Join(configDir, "security", "pgp.priv.asc.enc")
	if err := os.Rename(stage, active); err != nil {
		return fmt.Errorf("activate PGP private key after installation-secret rotation: %w", err)
	}
	return nil
}
