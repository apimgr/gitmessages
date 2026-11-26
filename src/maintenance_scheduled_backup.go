package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	backuppkg "github.com/apimgr/gitmessages/src/backup"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/shirou/gopsutil/v3/disk"
)

type scheduledFull struct {
	path       string
	manifest   backuppkg.Manifest
	forceDaily bool
}

func runScheduledBackup(ctx context.Context, store *database.Store, configuration *config.Config, directories paths.Directories, hourly bool) error {
	if store == nil || configuration == nil {
		return errors.New("database and configuration are required for scheduled backup")
	}
	if err := os.MkdirAll(directories.Backup, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := applyBackupRetention(ctx, store, directories.Backup, configuration); err != nil {
		return fmt.Errorf("prune backups before scheduled run: %w", err)
	}
	base, err := newestFullBackup(ctx, directories.Backup, configuration.Server.Backup.EncryptionPassword, time.Time{})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := checkBackupDisk(directories.Backup, configuration.Server.Backup.DiskThreshold); err != nil {
		if strings.Contains(err.Error(), "backup.skipped_disk_full") {
			_ = recordBackupEvent(ctx, store, "backup.skipped_disk_full", paths.ProjectName, 0, configuration.Server.Backup.EncryptionPassword != "", false, err.Error())
		}
		return err
	}
	t0 := time.Now().UTC()
	workspace, err := scheduledBackupWorkspace()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	stagedFull := filepath.Join(workspace, "snapshot.tar.gz")
	manifest, err := createApplicationBackupAt(ctx, store, configuration, directories.Config, directories.Data, workspace, stagedFull, false, false, t0)
	if err != nil {
		return fmt.Errorf("create scheduled backup snapshot: %w", err)
	}
	if base.path != "" {
		name := paths.ProjectName + "-daily.tar.gz"
		if hourly {
			name = paths.ProjectName + "-hourly.tar.gz"
		}
		if configuration.Server.Backup.EncryptionPassword != "" {
			name += ".enc"
		}
		incremental, err := createIncrementalFromSnapshot(ctx, stagedFull, manifest, base, filepath.Join(directories.Backup, name), configuration)
		if err != nil {
			return fmt.Errorf("create scheduled incremental backup: %w", err)
		}
		incrementPath := filepath.Join(directories.Backup, name)
		previousName := strings.TrimSuffix(name, ".enc")
		if !strings.HasSuffix(name, ".enc") {
			previousName += ".enc"
		}
		if err := os.Remove(filepath.Join(directories.Backup, previousName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove superseded incremental backup: %w", err)
		}
		if info, statErr := os.Stat(incrementPath); statErr == nil {
			action := "backup.created"
			if !hourly {
				action = "backup.daily_updated"
			}
			if err := recordBackupEvent(ctx, store, action, incrementPath, info.Size(), incremental.Encrypted, true, fmt.Sprintf("%d changed files", len(incremental.Files))); err != nil {
				return fmt.Errorf("record incremental backup: %w", err)
			}
		}
	}
	if hourly {
		return applyBackupRetention(ctx, store, directories.Backup, configuration)
	}
	fullName := paths.ProjectName + "_backup_" + t0.Format("2006-01-02") + ".tar.gz"
	if configuration.Server.Backup.EncryptionPassword != "" {
		fullName += ".enc"
	}
	fullPath := filepath.Join(directories.Backup, fullName)
	if _, err := os.Stat(fullPath); err == nil {
		fullName = paths.ProjectName + "_backup_" + t0.Format("2006-01-02_150405") + ".tar.gz"
		if configuration.Server.Backup.EncryptionPassword != "" {
			fullName += ".enc"
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect scheduled full backup destination: %w", err)
	}
	fullPath = filepath.Join(directories.Backup, fullName)
	if err := os.Rename(stagedFull, fullPath); err != nil {
		return fmt.Errorf("publish scheduled full backup: %w", err)
	}
	if info, err := os.Stat(fullPath); err != nil {
		return err
	} else if err := recordBackupEvent(ctx, store, "backup.created", fullPath, info.Size(), manifest.Encrypted, true, "verified"); err != nil {
		return fmt.Errorf("record full backup: %w", err)
	}
	if err := applyBackupRetention(ctx, store, directories.Backup, configuration); err != nil {
		return err
	}
	return nil
}

func scheduledBackupWorkspace() (string, error) {
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create backup temp parent: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", fmt.Errorf("secure backup temp parent: %w", err)
	}
	workspace, err := os.MkdirTemp(parent, paths.ProjectName+"-scheduled-backup-")
	if err != nil {
		return "", fmt.Errorf("create scheduled backup workspace: %w", err)
	}
	return workspace, nil
}

func newestFullBackup(ctx context.Context, directory, password string, before time.Time) (scheduledFull, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return scheduledFull{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), paths.ProjectName+"_backup_") || !strings.HasSuffix(entry.Name(), ".tar.gz") && !strings.HasSuffix(entry.Name(), ".tar.gz.enc") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if encryptedArchiveName(entry.Name()) {
			header, headerErr := backuppkg.ReadEncryptionHeader(path)
			if headerErr != nil {
				slog.Warn("backup.base_corrupt", "backup", entry.Name(), "reason", "invalid encryption header")
				return scheduledFull{}, os.ErrNotExist
			}
			if !header.MatchesPassword(password) {
				slog.Info("backup.password_changed", "base_backup", entry.Name())
				return scheduledFull{}, os.ErrNotExist
			}
		} else if password != "" {
			slog.Info("backup.password_changed", "base_backup", entry.Name())
			return scheduledFull{}, os.ErrNotExist
		}
		manifest, verifyErr := backuppkg.Verify(ctx, path, password, backuppkg.DefaultRestoreLimits())
		if verifyErr != nil || manifest.Type != "full" {
			slog.Warn("backup.base_corrupt", "backup", entry.Name(), "reason", "verification failed")
			return scheduledFull{}, os.ErrNotExist
		}
		if !before.IsZero() && !manifest.SnapshotAt.Before(before) {
			continue
		}
		return scheduledFull{path: path, manifest: manifest}, nil
	}
	return scheduledFull{}, os.ErrNotExist
}

func encryptedArchiveName(name string) bool { return strings.HasSuffix(name, ".tar.gz.enc") }

func isBackupArchive(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || encryptedArchiveName(name)
}

func archiveBaseBackup(ctx context.Context, path, password string) (string, bool) {
	if encryptedArchiveName(path) {
		header, err := backuppkg.ReadEncryptionHeader(path)
		if err != nil {
			return "", false
		}
		return header.BaseBackup, true
	}
	manifest, err := backuppkg.Verify(ctx, path, password, backuppkg.DefaultRestoreLimits())
	if err != nil || manifest.Type != "incremental" {
		return "", false
	}
	return manifest.BaseBackup, true
}

func createIncrementalFromSnapshot(ctx context.Context, snapshotPath string, snapshot backuppkg.Manifest, base scheduledFull, destination string, configuration *config.Config) (backuppkg.Manifest, error) {
	workspace, err := scheduledBackupWorkspace()
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	baseDir := filepath.Join(workspace, "base")
	currentDir := filepath.Join(workspace, "current")
	if _, err := backuppkg.Restore(ctx, base.path, configuration.Server.Backup.EncryptionPassword, baseDir, parseBackupRestoreLimits(configuration.Server.Restore)); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("extract incremental base: %w", err)
	}
	if _, err := backuppkg.Restore(ctx, snapshotPath, configuration.Server.Backup.EncryptionPassword, currentDir, parseBackupRestoreLimits(configuration.Server.Restore)); err != nil {
		return backuppkg.Manifest{}, fmt.Errorf("extract current snapshot: %w", err)
	}
	baseFiles := make(map[string]backuppkg.FileEntry, len(base.manifest.Files))
	for _, file := range base.manifest.Files {
		baseFiles[file.Path] = file
	}
	currentFiles := make(map[string]backuppkg.FileEntry, len(snapshot.Files))
	inputs := make([]backuppkg.Input, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		currentFiles[file.Path] = file
		previous, exists := baseFiles[file.Path]
		if exists && previous.Size == file.Size && previous.ModTime == file.ModTime && strings.EqualFold(previous.SHA256, file.SHA256) {
			continue
		}
		inputs = append(inputs, backuppkg.Input{Path: filepath.Join(currentDir, filepath.FromSlash(file.Path)), ArchivePath: file.Path})
	}
	deleted := make([]string, 0)
	for path := range baseFiles {
		if _, exists := currentFiles[path]; !exists {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(deleted)
	baseHash, err := hashBackupFile(base.path)
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	manifest, err := backuppkg.Create(ctx, backuppkg.CreateOptions{
		Destination: destination, Inputs: inputs, AppVersion: Version, CreatedBy: "scheduler",
		SnapshotAt: snapshot.SnapshotAt, Type: "incremental", BaseBackup: filepath.Base(base.path), BaseSHA256: baseHash,
		Deleted: deleted, Password: configuration.Server.Backup.EncryptionPassword,
		Compliance: configuration.Server.Compliance.AnyEnabled(),
	})
	if err != nil {
		return backuppkg.Manifest{}, err
	}
	if _, err := backuppkg.Verify(ctx, destination, configuration.Server.Backup.EncryptionPassword, parseBackupRestoreLimits(configuration.Server.Restore)); err != nil {
		_ = os.Remove(destination)
		return backuppkg.Manifest{}, fmt.Errorf("verify incremental backup: %w", err)
	}
	return manifest, nil
}

func hashBackupFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checkBackupDisk(directory string, threshold int) error {
	if threshold <= 0 || threshold > 100 {
		threshold = 90
	}
	usage, err := disk.Usage(directory)
	if err != nil {
		return fmt.Errorf("measure backup disk usage: %w", err)
	}
	mostRecent := int64(0)
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var latest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !isBackupArchive(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest, mostRecent = info.ModTime(), info.Size()
		}
	}
	if usage.UsedPercent > float64(threshold) || mostRecent > 0 && usage.Free < uint64(mostRecent)*2 {
		return fmt.Errorf("backup.skipped_disk_full: used_percent=%.1f free_bytes=%d latest_backup_bytes=%d threshold_percent=%d", usage.UsedPercent, usage.Free, mostRecent, threshold)
	}
	return nil
}

func applyBackupRetention(ctx context.Context, store *database.Store, directory string, configuration *config.Config) error {
	policy := configuration.Server.Backup.Retention
	if task, ok := configuration.Server.Scheduler.Tasks["backup_daily"]; ok && task.Retention != (config.BackupRetentionConfig{}) {
		policy = task.Retention
	}
	if policy.MaxBackups < 1 {
		policy.MaxBackups = 1
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	fulls := make([]scheduledFull, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), paths.ProjectName+"_backup_") || !isBackupArchive(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if snapshotAt, ok := backupFilenameTime(entry.Name()); ok {
			fulls = append(fulls, scheduledFull{path: path, manifest: backuppkg.Manifest{Type: "full", SnapshotAt: snapshotAt}})
		} else if info, statErr := entry.Info(); statErr == nil {
			fulls = append(fulls, scheduledFull{path: path, manifest: backuppkg.Manifest{Type: "full", SnapshotAt: info.ModTime().UTC()}, forceDaily: true})
		}
	}
	keep := backupRetentionSet(fulls, policy)
	for _, name := range []string{paths.ProjectName + "-daily.tar.gz", paths.ProjectName + "-daily.tar.gz.enc", paths.ProjectName + "-hourly.tar.gz", paths.ProjectName + "-hourly.tar.gz.enc"} {
		incrementPath := filepath.Join(directory, name)
		if base, ok := archiveBaseBackup(ctx, incrementPath, configuration.Server.Backup.EncryptionPassword); ok && base != "" {
			keep[filepath.Join(directory, base)] = struct{}{}
		}
	}
	deleted := make([]string, 0)
	for _, full := range fulls {
		if _, keepIt := keep[full.path]; keepIt {
			continue
		}
		if err := os.Remove(full.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove expired backup %s: %w", filepath.Base(full.path), err)
		}
		deleted = append(deleted, filepath.Base(full.path))
	}
	sizedDeleted, err := enforceBackupSizeLimit(ctx, directory, policy.MaxTotalSize, configuration)
	if err != nil {
		return err
	}
	deleted = append(deleted, sizedDeleted...)
	if len(deleted) > 0 {
		if err := recordBackupEvent(ctx, store, "backup.retention_cleanup", directory, 0, false, true, strings.Join(deleted, ",")); err != nil {
			return fmt.Errorf("record backup retention cleanup: %w", err)
		}
	}
	return nil
}

func backupFilenameTime(name string) (time.Time, bool) {
	name = strings.TrimPrefix(name, paths.ProjectName+"_backup_")
	name = strings.TrimSuffix(name, ".tar.gz.enc")
	name = strings.TrimSuffix(name, ".tar.gz")
	for _, layout := range []string{"2006-01-02_150405", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, name, time.UTC); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func backupRetentionSet(fulls []scheduledFull, policy config.BackupRetentionConfig) map[string]struct{} {
	ordered := append([]scheduledFull(nil), fulls...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].manifest.SnapshotAt.After(ordered[j].manifest.SnapshotAt) })
	keep := make(map[string]struct{})
	daily, weekly, monthly, yearly := 0, 0, 0, 0
	for _, full := range ordered {
		date := full.manifest.SnapshotAt
		keepIt := false
		if full.forceDaily {
			keepIt = daily < policy.MaxBackups
			daily++
		} else {
			switch {
			case date.Month() == time.January && date.Day() == 1:
				keepIt = yearly < policy.KeepYearly
				yearly++
			case date.Day() == 1:
				keepIt = monthly < policy.KeepMonthly
				monthly++
			case date.Weekday() == time.Sunday:
				keepIt = weekly < policy.KeepWeekly
				weekly++
			default:
				keepIt = daily < policy.MaxBackups
				daily++
			}
		}
		if keepIt {
			keep[full.path] = struct{}{}
		}
	}
	return keep
}

func enforceBackupSizeLimit(ctx context.Context, directory, configured string, configuration *config.Config) ([]string, error) {
	value := strings.TrimSpace(strings.ToUpper(configured))
	if value == "" || value == "0" || value == "FALSE" || value == "NO" || value == "NONE" || value == "DISABLE" || value == "DISABLED" || value == "OFF" {
		return nil, nil
	}
	usage, err := disk.Usage(directory)
	if err != nil {
		return nil, fmt.Errorf("measure backup volume for retention: %w", err)
	}
	limit := int64(0)
	if strings.HasSuffix(value, "%") {
		percent, parseErr := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
		if parseErr != nil || percent <= 0 || percent > 100 {
			return nil, fmt.Errorf("invalid backup retention size limit %q", configured)
		}
		limit = int64(float64(usage.Total) * percent / 100)
	} else {
		limit, err = parseBackupSize(value)
		if err != nil || limit <= 0 {
			return nil, fmt.Errorf("invalid backup retention size limit %q", configured)
		}
	}
	type archiveFile struct {
		path    string
		modTime time.Time
		size    int64
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	archives := make([]archiveFile, 0)
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), paths.ProjectName+"_") && !strings.HasPrefix(entry.Name(), paths.ProjectName+"-") || !isBackupArchive(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(directory, entry.Name())
		archives = append(archives, archiveFile{path: path, modTime: info.ModTime(), size: info.Size()})
		total += info.Size()
	}
	if total <= limit {
		return nil, nil
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].modTime.Before(archives[j].modTime) })
	protected := make(map[string]struct{})
	protectedBases := make(map[string]struct{})
	for _, archive := range archives {
		if base, ok := archiveBaseBackup(ctx, archive.path, configuration.Server.Backup.EncryptionPassword); ok && base != "" {
			protected[archive.path] = struct{}{}
			basePath := filepath.Join(directory, base)
			protected[basePath] = struct{}{}
			protectedBases[basePath] = struct{}{}
		}
	}
	deleted := make([]string, 0)
	for _, archive := range archives {
		if total <= limit {
			break
		}
		if _, keepBase := protected[archive.path]; keepBase {
			continue
		}
		if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove backup over size limit %s: %w", filepath.Base(archive.path), err)
		}
		deleted = append(deleted, filepath.Base(archive.path))
		total -= archive.size
		for _, incremental := range archives {
			if incremental.path == archive.path {
				continue
			}
			base, ok := archiveBaseBackup(ctx, incremental.path, configuration.Server.Backup.EncryptionPassword)
			if ok && base != "" && filepath.Join(directory, base) == archive.path {
				if err := os.Remove(incremental.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf("remove incremental whose base exceeded size limit %s: %w", filepath.Base(incremental.path), err)
				}
				deleted = append(deleted, filepath.Base(incremental.path))
				for _, candidate := range archives {
					if candidate.path == incremental.path {
						total -= candidate.size
						break
					}
				}
			}
		}
	}
	if total > limit {
		for _, archive := range archives {
			if total <= limit {
				break
			}
			if _, wasBase := protectedBases[archive.path]; !wasBase {
				continue
			}
			stillReferenced := false
			for _, candidate := range archives {
				if _, statErr := os.Stat(candidate.path); statErr != nil {
					continue
				}
				base, ok := archiveBaseBackup(ctx, candidate.path, configuration.Server.Backup.EncryptionPassword)
				if ok && base != "" && filepath.Join(directory, base) == archive.path {
					stillReferenced = true
					break
				}
			}
			if stillReferenced {
				continue
			}
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove unreferenced backup over size limit %s: %w", filepath.Base(archive.path), err)
			}
			deleted = append(deleted, filepath.Base(archive.path))
			total -= archive.size
		}
	}
	if total > limit {
		slog.Warn("backup.cap_exceeded_protected_base", "backup_dir", directory, "total_bytes", total, "limit_bytes", limit)
	}
	return deleted, nil
}
