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
	snapshotPath := filepath.Join(workingDir, "server.db")
	if err := store.Snapshot(ctx, snapshotPath); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("snapshot server database: %w", err)
	}
	if err := verifySQLiteBackup(snapshotPath); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("verify database snapshot: %w", err)
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
	inputs = append(inputs, backuppkg.Input{Path: snapshotPath, ArchivePath: "server.db"})
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
		selected, err := newestFullBackupFile(backupDir)
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
	if err := verifySQLiteBackup(databasePath); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("verify backup database: %w", err)
	}
	if err := verifyBackupPGPKeypair(databasePath, extracted); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("verify backup PGP keypair: %w", err)
	}
	return manifest, nil
}

func verifyBackupPGPKeypair(databasePath, extracted string) error {
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
	store, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return fmt.Errorf("open backup secrets database: %w", err)
	}
	defer func() { _ = store.Close() }()
	var encoded string
	if err := store.QueryRow(`SELECT value FROM app_secrets WHERE name='installation_secret'`).Scan(&encoded); err != nil {
		return fmt.Errorf("read backup installation secret: %w", err)
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(secret) != 32 {
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
	var newestFullPath, newestIncrementalPath string
	var newestFullDate, newestFullModTime, newestIncrementalModTime time.Time
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
		if backupDate, ok := fullBackupDate(name); ok {
			if newestFullPath == "" || backupDate.After(newestFullDate) || backupDate.Equal(newestFullDate) && info.ModTime().After(newestFullModTime) {
				newestFullPath, newestFullDate, newestFullModTime = path, backupDate, info.ModTime()
			}
			continue
		}
		if strings.HasSuffix(name, "-daily.tar.gz") || strings.HasSuffix(name, "-daily.tar.gz.enc") || strings.HasSuffix(name, "-hourly.tar.gz") || strings.HasSuffix(name, "-hourly.tar.gz.enc") {
			if newestIncrementalPath == "" || info.ModTime().After(newestIncrementalModTime) {
				newestIncrementalPath, newestIncrementalModTime = path, info.ModTime()
			}
		}
	}
	if newestFullPath != "" {
		return newestFullPath, nil
	}
	if newestIncrementalPath != "" {
		return newestIncrementalPath, nil
	}
	return "", errors.New("no backup archive was found")
}

// newestFullBackupFile selects the newest full archive without falling back to
// an incremental archive, which cannot restore independently.
func newestFullBackupFile(backupDir string) (string, error) {
	selected, err := newestBackupFile(backupDir)
	if err != nil {
		return "", err
	}
	if _, ok := fullBackupDate(filepath.Base(selected)); !ok {
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
	if value, err := parseBackupSize(configuration.MaxFileSize); err == nil && value > 0 {
		defaults.MaxFileSize = value
	}
	if configuration.MaxFiles > 0 {
		defaults.MaxFiles = configuration.MaxFiles
	}
	if configuration.MaxRatio > 0 {
		defaults.MaxRatio = configuration.MaxRatio
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
