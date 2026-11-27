package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	backuppkg "github.com/apimgr/gitmessages/src/backup"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/pgp"
)

func createApplicationBackup(ctx context.Context, store *database.Store, configuration *config.Config, configDir, dataDir, backupDir, destination string, includeSSL, includeData bool) (backuppkg.Manifest, error) {
	return createApplicationBackupAt(ctx, store, configuration, configDir, dataDir, backupDir, destination, includeSSL, includeData, time.Now().UTC())
}

func createApplicationBackupAt(ctx context.Context, store *database.Store, configuration *config.Config, configDir, dataDir, backupDir, destination string, includeSSL, includeData bool, snapshotAt time.Time) (backuppkg.Manifest, error) {
	if store == nil || configuration == nil {
		return backuppkg.Manifest{}, errors.New("database and configuration are required for backup")
	}
	if configuration.Server.Compliance.AnyEnabled() && strings.TrimSpace(configuration.Server.Backup.EncryptionPassword) == "" {
		return backuppkg.Manifest{}, errors.New("compliance mode requires server.backup.encryption_password before backups can run")
	}
	if destination == "" {
		extension := ".tar.gz"
		if configuration.Server.Backup.EncryptionPassword != "" {
			extension += ".enc"
		}
		destination = filepath.Join(backupDir, "gitmessages_backup_"+time.Now().UTC().Format("2006-01-02_150405")+extension)
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("secure backup directory: %w", err)
	}
	if configuration.Server.Backup.EncryptionPassword == "" {
		log.Printf("WARN backup.encryption_not_configured backups_are_unencrypted=true setting=server.backup.encryption_password")
	}
	tempParent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := paths.EnsurePrivateTempParent(tempParent); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("create project backup temp directory: %w", err)
	}
	workingDir, err := os.MkdirTemp(tempParent, paths.ProjectName+"-snapshot-")
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("create backup snapshot workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workingDir) }()
	if store.IsRemote() {
		probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		pingErr := store.PrimaryRead().PingContext(probeContext)
		cancel()
		if pingErr != nil {
			return backuppkg.Manifest{}, fmt.Errorf("remote database is unavailable for backup: %w", pingErr)
		}
	}
	driverName := strings.ToLower(strings.TrimSpace(configuration.Server.Database.Driver))
	if driverName == "" || driverName == "sqlite2" || driverName == "sqlite3" {
		driverName = "sqlite"
	}
	if store.IsRemote() && driverName != "libsql" && driverName != "turso" {
		driverName = "libsql"
	}
	snapshotPath := filepath.Join(workingDir, "server.db")
	if !store.IsRemote() {
		if err := store.Snapshot(ctx, snapshotPath); err != nil {
			return backuppkg.Manifest{}, fmt.Errorf("snapshot server database: %w", err)
		}
		if err := verifySQLiteBackup(snapshotPath); err != nil {
			return backuppkg.Manifest{}, fmt.Errorf("verify database snapshot: %w", err)
		}
	}
	inputs := make([]backuppkg.Input, 0)
	add := func(source, archivePrefix string, required bool) error {
		entries, err := backuppkg.InputsFromPath(source, archivePrefix)
		if errors.Is(err, os.ErrNotExist) && !required {
			return nil
		}
		if err != nil {
			return fmt.Errorf("collect backup source %s: %w", source, err)
		}
		inputs = append(inputs, entries...)
		return nil
	}
	if err := add(filepath.Join(configDir, "server.yml"), "", true); err != nil {
		return backuppkg.Manifest{}, err
	}
	secretRecords, err := readApplicationSecrets(ctx, store.PrimaryRead())
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("export application secrets: %w", err)
	}
	secretPath := filepath.Join(workingDir, "app_secrets.json")
	secretJSON, err := json.Marshal(secretRecords)
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("encode application secrets: %w", err)
	}
	if err := os.WriteFile(secretPath, secretJSON, 0o600); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("write application secrets export: %w", err)
	}
	secretInputs, err := backuppkg.InputsFromPath(secretPath, "")
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("collect application secrets export: %w", err)
	}
	inputs = append(inputs, secretInputs...)
	infoPath := filepath.Join(workingDir, "info")
	if err := os.WriteFile(infoPath, []byte("database_driver="+driverName+"\n"), 0o600); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("write backup driver info: %w", err)
	}
	infoInputs, err := backuppkg.InputsFromPath(infoPath, "")
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("collect backup driver info: %w", err)
	}
	inputs = append(inputs, infoInputs...)
	for _, item := range []struct{ path, prefix string }{
		{filepath.Join(configDir, "yaml.d"), "yaml.d"},
		{filepath.Join(configDir, "secrets.yml"), ""},
		{filepath.Join(configDir, "assets"), "assets"},
		{filepath.Join(configDir, "template"), "template"},
		{filepath.Join(configDir, "security"), "security"},
	} {
		if err := add(item.path, item.prefix, false); err != nil {
			return backuppkg.Manifest{}, err
		}
	}
	if includeSSL {
		if err := add(filepath.Join(configDir, "ssl"), "ssl", false); err != nil {
			return backuppkg.Manifest{}, err
		}
	}
	for _, filename := range []string{configuration.Server.Cache.TLSCA, configuration.Server.Cache.TLSCert, configuration.Server.Cache.TLSKey} {
		if includeSSL {
			break
		}
		if strings.TrimSpace(filename) == "" {
			continue
		}
		path, archivePath, err := connectionTLSBackupPath(configDir, filename)
		if err != nil {
			return backuppkg.Manifest{}, err
		}
		entries, err := backuppkg.InputsFromPath(path, "")
		if errors.Is(err, os.ErrNotExist) {
			return backuppkg.Manifest{}, fmt.Errorf("configured cache TLS file %q does not exist", archivePath)
		}
		if err != nil {
			return backuppkg.Manifest{}, fmt.Errorf("collect cache TLS file %q: %w", archivePath, err)
		}
		for index := range entries {
			entries[index].ArchivePath = archivePath
		}
		inputs = append(inputs, entries...)
	}
	if !store.IsRemote() {
		inputs = append(inputs, backuppkg.Input{Path: snapshotPath, ArchivePath: "server.db"})
	}
	if includeData {
		if err := add(dataDir, "data", false); err != nil {
			return backuppkg.Manifest{}, err
		}
	}
	manifest, err := backuppkg.Create(ctx, backuppkg.CreateOptions{
		Destination: destination, Inputs: inputs, AppVersion: Version, CreatedBy: "operator",
		SnapshotAt: snapshotAt, Password: configuration.Server.Backup.EncryptionPassword,
		Compliance: configuration.Server.Compliance.AnyEnabled(),
	})
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	return manifest, nil
}

type applicationSecretRecord struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

func readApplicationSecrets(ctx context.Context, db *sql.DB) ([]applicationSecretRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT name,value,version,updated_at FROM app_secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]applicationSecretRecord, 0)
	for rows.Next() {
		var record applicationSecretRecord
		if err := rows.Scan(&record.Name, &record.Value, &record.Version, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func connectionTLSBackupPath(configDir, filename string) (string, string, error) {
	sslRoot, err := filepath.Abs(filepath.Join(configDir, "ssl"))
	if err != nil {
		return "", "", errors.New("resolve configured cache TLS directory")
	}
	path := filename
	if !filepath.IsAbs(path) {
		path = filepath.Join(sslRoot, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", errors.New("resolve configured cache TLS file")
	}
	relative, err := filepath.Rel(sslRoot, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("configured cache TLS files must be inside config_dir/ssl")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", errors.New("configured cache TLS file is unavailable")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", "", errors.New("resolve configured cache TLS file")
	}
	resolvedRelative, err := filepath.Rel(sslRoot, resolved)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("configured cache TLS files must resolve inside config_dir/ssl")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errors.New("configured cache TLS path must name a regular file")
	}
	return resolved, filepath.ToSlash(filepath.Join("ssl", resolvedRelative)), nil
}

func verifyApplicationBackup(ctx context.Context, backupFile, backupDir, password string, limits config.RestoreConfig) (backuppkg.Manifest, error) {
	if backupFile == "" {
		selected, err := newestBackupFile(backupDir)
		if err != nil {
			return backuppkg.Manifest{}, err
		}
		backupFile = selected
	}
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := paths.EnsurePrivateTempParent(parent); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("create backup-test temp parent: %w", err)
	}
	workspace, err := os.MkdirTemp(parent, paths.ProjectName+"-backup-test-")
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("create backup-test workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	manifest, err := backuppkg.Restore(ctx, backupFile, password, filepath.Join(workspace, "extracted"), parseBackupRestoreLimits(limits))
	if err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("verify backup archive: %w", err)
	}
	extracted := filepath.Join(workspace, "extracted")
	if _, err := os.Stat(filepath.Join(extracted, "server.yml")); err != nil {
		return backuppkg.Manifest{}, errors.New("backup does not contain server.yml")
	}
	databasePath := filepath.Join(extracted, "server.db")
	driver, err := backupDatabaseDriver(extracted)
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	var installationSecret []byte
	if driver == "sqlite" {
		if err := verifySQLiteBackup(databasePath); err != nil {
			return backuppkg.Manifest{}, fmt.Errorf("verify backup database: %w", err)
		}
		store, err := sql.Open("sqlite", databasePath)
		if err != nil {
			return backuppkg.Manifest{}, err
		}
		var encoded string
		err = store.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='installation_secret'`).Scan(&encoded)
		_ = store.Close()
		if err != nil {
			return backuppkg.Manifest{}, fmt.Errorf("read backup installation secret: %w", err)
		}
		installationSecret, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(installationSecret) != 32 {
			return backuppkg.Manifest{}, errors.New("backup installation secret is invalid")
		}
	}
	secretRecords, err := readBackupSecrets(filepath.Join(extracted, "app_secrets.json"))
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	if driver != "sqlite" {
		for _, record := range secretRecords {
			if record.Name == "installation_secret" {
				installationSecret, err = base64.StdEncoding.DecodeString(record.Value)
				if err != nil || len(installationSecret) != 32 {
					return backuppkg.Manifest{}, errors.New("backup installation secret is invalid")
				}
				break
			}
		}
	}
	if err := verifyBackupPGPKeypairWithSecret(installationSecret, extracted); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("verify backup PGP keypair: %w", err)
	}
	return manifest, nil
}

func backupDatabaseDriver(extracted string) (string, error) {
	info, err := os.ReadFile(filepath.Join(extracted, "info"))
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Join(extracted, "server.db")); statErr == nil {
			return "sqlite", nil
		}
		return "", errors.New("backup is missing database driver info")
	}
	if err != nil {
		return "", fmt.Errorf("read backup driver info: %w", err)
	}
	value := strings.TrimSuffix(string(info), "\n")
	if strings.Contains(value, "\n") || !strings.HasPrefix(value, "database_driver=") {
		return "", errors.New("backup database driver info is invalid")
	}
	driver := strings.TrimPrefix(value, "database_driver=")
	if driver != "sqlite" && driver != "libsql" && driver != "turso" {
		return "", errors.New("backup database driver is unsupported")
	}
	_, statErr := os.Stat(filepath.Join(extracted, "server.db"))
	if driver == "sqlite" && statErr != nil || driver != "sqlite" && statErr == nil {
		return "", errors.New("backup database contents do not match its driver info")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	return driver, nil
}

func readBackupSecrets(path string) ([]applicationSecretRecord, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup app_secrets.json: %w", err)
	}
	var records []applicationSecretRecord
	if err := json.Unmarshal(content, &records); err != nil || records == nil {
		return nil, errors.New("backup app_secrets.json is invalid")
	}
	for _, record := range records {
		if strings.TrimSpace(record.Name) == "" || record.Version < 1 {
			return nil, errors.New("backup app_secrets.json contains an invalid record")
		}
	}
	return records, nil
}

func verifyBackupPGPKeypairWithSecret(secret []byte, extracted string) error {
	publicPath := filepath.Join(extracted, "security", "pgp.pub.asc")
	privatePath := filepath.Join(extracted, "security", "pgp.priv.asc.enc")
	public, publicErr := os.ReadFile(publicPath)
	private, privateErr := os.ReadFile(privatePath)
	if errors.Is(publicErr, os.ErrNotExist) && errors.Is(privateErr, os.ErrNotExist) {
		return nil
	}
	if publicErr != nil || privateErr != nil {
		return errors.Join(publicErr, privateErr, errors.New("backup must contain both PGP key files"))
	}
	if len(secret) != 32 {
		return errors.New("backup installation secret is invalid")
	}
	decrypted, err := pgp.DecryptPrivate(private, secret)
	if err != nil {
		return fmt.Errorf("decrypt backup PGP private key: %w", err)
	}
	privateMetadata, err := pgp.Import(decrypted, secret)
	if err != nil {
		return fmt.Errorf("validate backup PGP private key: %w", err)
	}
	publicMetadata, err := pgp.MetadataForPublic(public)
	if err != nil {
		return fmt.Errorf("validate backup PGP public key: %w", err)
	}
	if privateMetadata.Metadata.Fingerprint != publicMetadata.Fingerprint {
		return errors.New("backup PGP public and private fingerprints do not match")
	}
	return nil
}

func newestBackupFile(backupDir string) (string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", fmt.Errorf("read backup directory: %w", err)
	}
	var newestPath string
	var newestDate, newestModTime time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isBackupArchive(name) || !strings.HasPrefix(name, paths.ProjectName+"_") && !strings.HasPrefix(name, paths.ProjectName+"-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		path := filepath.Join(backupDir, name)
		backupDate, ok := fullBackupDate(name)
		if !ok && (strings.HasSuffix(name, "-daily.tar.gz") || strings.HasSuffix(name, "-daily.tar.gz.enc") || strings.HasSuffix(name, "-hourly.tar.gz") || strings.HasSuffix(name, "-hourly.tar.gz.enc")) {
			backupDate = info.ModTime().UTC().Truncate(24 * time.Hour)
			ok = true
		}
		if ok && (newestPath == "" || backupDate.After(newestDate) || backupDate.Equal(newestDate) && info.ModTime().After(newestModTime)) {
			newestPath, newestDate, newestModTime = path, backupDate, info.ModTime()
		}
	}
	if newestPath != "" {
		return newestPath, nil
	}
	return "", errors.New("no backup archive was found")
}

// newestFullBackupFile selects the newest full archive without falling back to
// an incremental archive, which cannot restore independently.
func newestFullBackupFile(backupDir string) (string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", err
	}
	var selected string
	var selectedDate, selectedModTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || !isBackupArchive(entry.Name()) || !strings.HasPrefix(entry.Name(), paths.ProjectName+"_backup_") {
			continue
		}
		date, ok := fullBackupDate(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if selected == "" || date.After(selectedDate) || date.Equal(selectedDate) && info.ModTime().After(selectedModTime) {
			selected = filepath.Join(backupDir, entry.Name())
			selectedDate, selectedModTime = date, info.ModTime()
		}
	}
	if selected == "" {
		return "", errors.New("no full backup archive was found")
	}
	return selected, nil
}

func fullBackupDate(name string) (time.Time, bool) {
	prefix := paths.ProjectName + "_backup_"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	dateEnd := len(prefix) + len("2006-01-02")
	if len(name) < dateEnd {
		return time.Time{}, false
	}
	date, err := time.Parse("2006-01-02", name[len(prefix):dateEnd])
	if err != nil || len(name) > dateEnd && name[dateEnd] != '_' && name[dateEnd] != '.' {
		return time.Time{}, false
	}
	return date.UTC(), true
}

func recordBackupEvent(ctx context.Context, store *database.Store, action, filename string, size int64, encrypted, success bool, details string) error {
	if store == nil {
		return errors.New("database is required to record backup activity")
	}
	encoded, err := json.Marshal(map[string]any{"filename": filepath.Base(filename), "size": size, "encrypted": encrypted, "details": details})
	if err != nil {
		return err
	}
	level := "info"
	if !success {
		level = "error"
	}
	successValue := 0
	if success {
		successValue = 1
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,target_type,target_id,details,success) VALUES(?, 'backup', ?, 'archive', ?, ?, ?)`, level, action, filepath.Base(filename), string(encoded), successValue); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO backups(action,filename,size,encrypted,success,details) VALUES(?,?,?,?,?,?)`, action, filepath.Base(filename), size, encrypted, successValue, details)
		return err
	})
}

func verifySQLiteBackup(path string) error {
	store, err := database.OpenSQLite(path, 1)
	if err != nil {
		return err
	}
	var result string
	queryErr := store.Read().QueryRow("PRAGMA integrity_check").Scan(&result)
	closeErr := store.Close()
	if queryErr != nil || closeErr != nil {
		return errors.Join(queryErr, closeErr)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check returned %q", result)
	}
	return nil
}

func parseBackupRestoreLimits(configuration config.RestoreConfig) backuppkg.RestoreLimits {
	defaults := backuppkg.DefaultRestoreLimits()
	if value, err := parseBackupSize(configuration.MaxArchiveSize); err == nil && value > 0 {
		defaults.MaxArchiveSize = value
	}
	if value, err := parseBackupSize(configuration.MaxExpandedSize); err == nil && value > 0 {
		defaults.MaxExpandedSize = value
	}
	if configuration.MaxFiles > 0 {
		defaults.MaxFiles = configuration.MaxFiles
	}
	if configuration.MaxPathLength > 0 {
		defaults.MaxPathLength = configuration.MaxPathLength
	}
	return defaults
}

func parseBackupSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0, errors.New("empty size")
	}
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		factor int64
	}{
		{suffix: "KB", factor: 1 << 10},
		{suffix: "MB", factor: 1 << 20},
		{suffix: "GB", factor: 1 << 30},
		{suffix: "TB", factor: 1 << 40},
		{suffix: "K", factor: 1 << 10},
		{suffix: "M", factor: 1 << 20},
		{suffix: "G", factor: 1 << 30},
		{suffix: "T", factor: 1 << 40},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier = unit.factor
			value = strings.TrimSuffix(value, unit.suffix)
			break
		}
	}
	value = strings.TrimSuffix(value, "B")
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount < 0 || amount > int64(^uint64(0)>>1)/multiplier {
		return 0, fmt.Errorf("invalid backup size %q", value)
	}
	return amount * multiplier, nil
}
