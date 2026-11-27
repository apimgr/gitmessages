package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	backuppkg "github.com/apimgr/gitmessages/src/backup"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
	"golang.org/x/term"
)

// restoreApplicationBackup verifies and validates an archive before replacing live state.
func restoreApplicationBackup(ctx context.Context, archivePath, configDir, dataDir, databaseDir, pidPath, address, port string, limits config.RestoreConfig, password string, targetStore ...*database.Store) error {
	if ctx == nil {
		return errors.New("restore context is required")
	}
	if err := ensureRestoreServerStopped(pidPath, address, port); err != nil {
		return err
	}
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := paths.EnsurePrivateTempParent(parent); err != nil {
		return fmt.Errorf("create restore temp parent: %w", err)
	}
	workspace, err := os.MkdirTemp(parent, paths.ProjectName+"-restore-")
	if err != nil {
		return fmt.Errorf("create restore workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	extracted := filepath.Join(workspace, "extracted")
	manifest, err := backuppkg.Restore(ctx, archivePath, password, extracted, parseBackupRestoreLimits(limits))
	if err != nil {
		return fmt.Errorf("verify and extract backup: %w", err)
	}
	if manifest.AppVersion != "" && manifest.AppVersion != Version {
		log.Printf("WARN restore.version_mismatch backup_version=%q app_version=%q schema_updates=applied_on_next_database_open", manifest.AppVersion, Version)
	}
	if manifest.Type != "full" && manifest.Type != "incremental" {
		return errors.New("unsupported backup type")
	}
	driver, err := backupDatabaseDriver(extracted)
	if err != nil {
		return err
	}
	databasePath := filepath.Join(extracted, "server.db")
	if driver == "sqlite" {
		if err := verifyRestoreSQLite(databasePath); err != nil {
			return fmt.Errorf("validate restored database: %w", err)
		}
	} else if len(targetStore) == 0 || targetStore[0] == nil || !targetStore[0].IsRemote() {
		return errors.New("a remote database backup requires a configured remote database")
	}
	secretRecords, err := readBackupSecrets(filepath.Join(extracted, "app_secrets.json"))
	if err != nil {
		return err
	}
	configSource := filepath.Join(workspace, "new-config")
	if err := os.Mkdir(configSource, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(extracted)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "server.db" || entry.Name() == "app_secrets.json" || entry.Name() == "info" || entry.Name() == "data" || entry.Name() == "manifest.json" {
			continue
		}
		if err := copyRestoreEntry(filepath.Join(extracted, entry.Name()), filepath.Join(configSource, entry.Name())); err != nil {
			return fmt.Errorf("stage restored configuration: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Join(configSource, "server.yml")); err != nil {
		return errors.New("backup is missing server.yml")
	}
	targets := []restoreTarget{{destination: configDir, source: configSource}}
	dataSource := filepath.Join(extracted, "data")
	hasData := false
	if info, err := os.Stat(dataSource); err == nil && info.IsDir() {
		hasData = true
		targets = append(targets, restoreTarget{destination: dataDir, source: dataSource})
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if driver == "sqlite" && !isWithinDir(dataDir, databaseDir) {
		dbSource := filepath.Join(workspace, "new-database")
		if err := os.Mkdir(dbSource, 0o700); err != nil {
			return err
		}
		if err := copyRestoreEntry(filepath.Join(extracted, "server.db"), filepath.Join(dbSource, "server.db")); err != nil {
			return fmt.Errorf("stage restored database: %w", err)
		}
		targets = append(targets, restoreTarget{destination: databaseDir, source: dbSource})
	} else if driver == "sqlite" && dataDir != "" {
		relative, err := filepath.Rel(dataDir, filepath.Join(databaseDir, "server.db"))
		if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("database path is not safely contained in data directory")
		}
		if hasData {
			dbTarget := filepath.Join(dataSource, relative)
			if err := os.MkdirAll(filepath.Dir(dbTarget), 0o700); err != nil {
				return err
			}
			if err := copyRestoreEntry(filepath.Join(extracted, "server.db"), dbTarget); err != nil {
				return fmt.Errorf("stage restored database: %w", err)
			}
		} else {
			dbSource := filepath.Join(workspace, "new-database")
			if err := os.Mkdir(dbSource, 0o700); err != nil {
				return err
			}
			if err := copyRestoreEntry(filepath.Join(extracted, "server.db"), filepath.Join(dbSource, "server.db")); err != nil {
				return fmt.Errorf("stage restored database: %w", err)
			}
			targets = append(targets, restoreTarget{destination: databaseDir, source: dbSource})
		}
	}
	if len(targetStore) > 0 && targetStore[0] != nil && targetStore[0].IsRemote() {
		if driver == "sqlite" {
			empty, err := targetStore[0].RestoreDataEmpty(ctx)
			if err != nil {
				return err
			}
			if empty {
				if err := targetStore[0].SeedFromSQLite(ctx, databasePath); err != nil {
					return fmt.Errorf("seed empty remote database from SQLite backup: %w", err)
				}
			}
		}
		if err := applyApplicationSecretsIfAbsent(ctx, targetStore[0], secretRecords); err != nil {
			return err
		}
	}
	return replaceRestoreTargets(targets, workspace)
}

func applyApplicationSecretsIfAbsent(ctx context.Context, store *database.Store, records []applicationSecretRecord) error {
	if store == nil || !store.IsRemote() {
		return errors.New("remote database is required to apply archived application secrets")
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_secrets`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		for _, record := range records {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,?,?)`, record.Name, record.Value, record.Version, record.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// authorizeApplicationRestore handles operator authorization separately from archive passwords.
func authorizeApplicationRestore(serverToken, suppliedToken string, databaseEmpty, root, serviceUser bool, confirm io.Reader, confirmWriter io.Writer) error {
	if databaseEmpty {
		return nil
	}
	if root {
		if confirm == nil || confirmWriter == nil {
			return errors.New("root restore requires interactive confirmation")
		}
		if _, err := fmt.Fprint(confirmWriter, i18n.T(os.Getenv("GITMESSAGES_LANG"), "cli.restore_confirm_prompt")); err != nil {
			return err
		}
		var response string
		if _, err := fmt.Fscanln(confirm, &response); err != nil || response != "RESTORE" {
			return errors.New("restore confirmation failed")
		}
		return nil
	}
	if !serviceUser || serverToken == "" || suppliedToken == "" || subtle.ConstantTimeCompare([]byte(serverToken), []byte(suppliedToken)) != 1 {
		return errors.New("restore requires administrator authorization")
	}
	return nil
}

// confirmDefaultRestore requires an explicit operator acknowledgement when
// restore chooses an archive automatically.
func confirmDefaultRestore(confirm io.Reader, confirmWriter io.Writer) error {
	if confirm == nil || confirmWriter == nil {
		return errors.New("default restore requires interactive confirmation")
	}
	if _, err := fmt.Fprint(confirmWriter, i18n.T(os.Getenv("GITMESSAGES_LANG"), "cli.restore_confirm_prompt")); err != nil {
		return err
	}
	var response string
	if _, err := fmt.Fscanln(confirm, &response); err != nil || response != "RESTORE" {
		return errors.New("restore confirmation failed")
	}
	return nil
}

// promptBackupPassword reads a backup password without echoing it to the terminal.
func promptBackupPassword(input *os.File, output io.Writer) (string, error) {
	if input == nil || output == nil || !term.IsTerminal(int(input.Fd())) {
		return "", errors.New("encrypted backup restore requires an interactive terminal password prompt")
	}
	if _, err := io.WriteString(output, i18n.T(os.Getenv("GITMESSAGES_LANG"), "cli.backup_password_prompt")); err != nil {
		return "", err
	}
	value, err := term.ReadPassword(int(input.Fd()))
	_, _ = io.WriteString(output, "\n")
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func promptOperatorToken(input *os.File, output io.Writer) (string, error) {
	if input == nil || output == nil || !term.IsTerminal(int(input.Fd())) {
		return "", errors.New("operator token authorization requires an interactive terminal")
	}
	if _, err := io.WriteString(output, i18n.T(os.Getenv("GITMESSAGES_LANG"), "cli.operator_token_prompt")); err != nil {
		return "", err
	}
	value, err := term.ReadPassword(int(input.Fd()))
	_, _ = io.WriteString(output, "\n")
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func applicationDatabaseEmpty(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	store, err := database.OpenSQLite(path, 1)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	var count int64
	err = store.Read().QueryRow(`SELECT
		(SELECT COUNT(*) FROM app_secrets) +
		(SELECT COUNT(*) FROM rate_limits) +
		(SELECT COUNT(*) FROM audit_log) +
		(SELECT COUNT(*) FROM owners) +
		(SELECT COUNT(*) FROM api_tokens) +
		(SELECT COUNT(*) FROM captcha_challenges) +
		(SELECT COUNT(*) FROM security_reports) +
		(SELECT COUNT(*) FROM scheduler_tasks) +
		(SELECT COUNT(*) FROM security_thanks)`).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func ensureRestoreServerStopped(pidPath, address, port string) error {
	if paths.IsRunningInContainer() {
		if port == "" || address == "" {
			return errors.New("cannot verify server is stopped because the configured port is unavailable")
		}
		host := strings.Trim(address, "[]")
		if host == "" || host == "::" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		for _, candidate := range strings.Split(port, ",") {
			endpoint := net.JoinHostPort(host, strings.TrimSpace(candidate))
			connection, err := net.DialTimeout("tcp", endpoint, 250*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				return errors.New("restore requires the server to be stopped")
			}
		}
		return nil
	}
	if pidPath == "" {
		return errors.New("cannot verify server is stopped because the PID path is unavailable")
	}
	running, pid, err := paths.CheckPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("check server PID: %w", err)
	}
	if running {
		return fmt.Errorf("restore requires the server to be stopped (pid %d)", pid)
	}
	return nil
}

func verifyRestoreSQLite(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("database archive entry is not a regular file")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check returned %q", result)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return errors.New("SQLite foreign key integrity check failed")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

type restoreTarget struct {
	destination string
	source      string
}

func replaceRestoreTargets(targets []restoreTarget, workspace string) error {
	staged := make([]restoreTarget, 0, len(targets))
	backups := make([]string, 0, len(targets))
	installed := 0
	rollback := func() {
		for i := installed - 1; i >= 0; i-- {
			_ = os.RemoveAll(targets[i].destination)
			if backups[i] != "" {
				_ = os.Rename(backups[i], targets[i].destination)
			}
		}
	}
	for index, target := range targets {
		if target.destination == "" || target.source == "" {
			return errors.New("restore target path is empty")
		}
		parent := filepath.Dir(target.destination)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("prepare restore target parent: %w", err)
		}
		stage := filepath.Join(parent, ".gitmessages-restore-stage-"+filepath.Base(workspace)+fmt.Sprintf("-%d", index))
		if _, err := os.Lstat(stage); err == nil {
			return errors.New("restore staging path already exists")
		}
		if err := copyRestoreEntry(target.source, stage); err != nil {
			for _, item := range staged {
				_ = os.RemoveAll(item.source)
			}
			return fmt.Errorf("stage restore target: %w", err)
		}
		staged = append(staged, restoreTarget{destination: target.destination, source: stage})
	}
	for _, target := range staged {
		backup := filepath.Join(filepath.Dir(target.destination), ".gitmessages-restore-rollback-"+filepath.Base(workspace)+fmt.Sprintf("-%d", installed))
		if _, err := os.Lstat(target.destination); err == nil {
			if err := os.Rename(target.destination, backup); err != nil {
				rollback()
				return fmt.Errorf("move current restore target aside: %w", err)
			}
			backups = append(backups, backup)
		} else if errors.Is(err, os.ErrNotExist) {
			backups = append(backups, "")
		} else {
			rollback()
			return err
		}
		if err := os.Rename(target.source, target.destination); err != nil {
			if backups[installed] != "" {
				_ = os.Rename(backups[installed], target.destination)
			}
			rollback()
			return fmt.Errorf("install restored target: %w", err)
		}
		installed++
	}
	for _, backup := range backups {
		if backup != "" {
			_ = os.RemoveAll(backup)
		}
	}
	return nil
}

func copyRestoreEntry(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symbolic links are not allowed in restore data")
	}
	if info.IsDir() {
		if err := os.Mkdir(destination, info.Mode().Perm()&0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyRestoreEntry(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("non-regular restore entry")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0o600)
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	closeErr := output.Close()
	return errors.Join(copyErr, inputCloseErr, closeErr)
}

func isWithinDir(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(parentAbs, childAbs)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
