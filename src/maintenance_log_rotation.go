package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

type logRotationTarget struct {
	kind   string
	config config.LogKindConfig
}

func rotateLogFiles(directory string, configuration config.LoggingConfig, now time.Time) error {
	targets := []logRotationTarget{
		{kind: "access", config: configuration.Access},
		{kind: "server", config: configuration.Server},
		{kind: "error", config: configuration.Error},
		{kind: "app", config: configuration.App},
		{kind: "auth", config: configuration.Auth},
		{kind: "audit", config: configuration.Audit},
		{kind: "security", config: configuration.Security},
		{kind: "debug", config: configuration.Debug},
	}
	for _, target := range targets {
		if target.kind == "debug" && !target.config.Enabled {
			continue
		}
		filename := target.config.Filename
		if filename == "" {
			filename = target.kind + ".log"
		}
		if filepath.Base(filename) != filename || filename == "." || filename == ".." {
			return fmt.Errorf("invalid %s log filename", target.kind)
		}
		path := filepath.Join(directory, filename)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s log: %w", target.kind, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s log is not a regular file", target.kind)
		}
		rotation := target.config.Rotate
		if rotation == "" {
			rotation = configuration.Defaults.Rotate
		}
		if rotation == "" {
			rotation = "weekly,50MB"
		}
		if !logRotationDue(rotation, info, now) {
			continue
		}
		keep := target.config.Keep
		if keep == "" {
			keep = configuration.Defaults.Keep
		}
		if keep == "" {
			keep = "none"
		}
		if err := rotateOneLog(path, keep, target.config.Compress, now); err != nil {
			return fmt.Errorf("rotate %s log: %w", target.kind, err)
		}
	}
	return nil
}

func logRotationDue(policy string, info os.FileInfo, now time.Time) bool {
	for _, part := range strings.Split(policy, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch part {
		case "never", "":
			continue
		case "daily":
			if info.ModTime().In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02") {
				return true
			}
		case "weekly":
			year, week := info.ModTime().In(now.Location()).ISOWeek()
			nowYear, nowWeek := now.ISOWeek()
			if year != nowYear || week != nowWeek {
				return true
			}
		case "monthly":
			if info.ModTime().In(now.Location()).Format("2006-01") != now.Format("2006-01") {
				return true
			}
		case "yearly":
			if info.ModTime().In(now.Location()).Year() != now.Year() {
				return true
			}
		default:
			if threshold, err := parseBackupSize(strings.ToUpper(part)); err == nil && threshold > 0 && info.Size() >= threshold {
				return true
			}
		}
	}
	return false
}

func rotateOneLog(path, keep string, compress bool, now time.Time) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	archiveName := filepath.Base(path) + "." + now.UTC().Format("20060102T150405Z")
	if compress {
		archiveName += ".gz"
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gitmessages-log-rotation-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	var writer io.Writer = temporary
	var compressed *gzip.Writer
	if compress {
		compressed = gzip.NewWriter(temporary)
		writer = compressed
	}
	if _, err := io.Copy(writer, input); err != nil {
		_ = temporary.Close()
		return err
	}
	if compressed != nil {
		if err := compressed.Close(); err != nil {
			_ = temporary.Close()
			return err
		}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	archivePath := filepath.Join(filepath.Dir(path), archiveName)
	if keep == "none" || keep == "0" {
		if err := os.Truncate(path, 0); err != nil {
			return err
		}
		return pruneRotatedLogs(filepath.Dir(path), filepath.Base(path), "0", now)
	}
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return err
	}
	if err := os.Truncate(path, 0); err != nil {
		_ = os.Remove(archivePath)
		return err
	}
	return pruneRotatedLogs(filepath.Dir(path), filepath.Base(path), keep, now)
}

func pruneRotatedLogs(directory, filename, keep string, now time.Time) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type archivedLog struct {
		path string
		info os.FileInfo
	}
	archives := make([]archivedLog, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), filename+".") || strings.HasPrefix(entry.Name(), ".gitmessages-log-rotation-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		archives = append(archives, archivedLog{path: filepath.Join(directory, entry.Name()), info: info})
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].info.ModTime().After(archives[j].info.ModTime()) })
	if keep == "forever" {
		return nil
	}
	if count, err := strconv.Atoi(keep); err == nil && count >= 0 {
		if count >= len(archives) {
			return nil
		}
		for _, archive := range archives[count:] {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	cutoff, valid := logRetentionCutoff(keep, now)
	if !valid {
		return fmt.Errorf("invalid log retention policy %q", keep)
	}
	for _, archive := range archives {
		if archive.info.ModTime().Before(cutoff) {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func logRetentionCutoff(policy string, now time.Time) (time.Time, bool) {
	if strings.HasPrefix(policy, "weekly:") {
		amount, err := strconv.Atoi(strings.TrimPrefix(policy, "weekly:"))
		return now.AddDate(0, 0, -7*amount), err == nil && amount >= 0
	}
	if strings.HasPrefix(policy, "monthly:") {
		amount, err := strconv.Atoi(strings.TrimPrefix(policy, "monthly:"))
		return now.AddDate(0, -amount, 0), err == nil && amount >= 0
	}
	if strings.HasSuffix(policy, "d") {
		amount, err := strconv.Atoi(strings.TrimSuffix(policy, "d"))
		return now.AddDate(0, 0, -amount), err == nil && amount >= 0
	}
	if strings.HasSuffix(policy, "w") {
		amount, err := strconv.Atoi(strings.TrimSuffix(policy, "w"))
		return now.AddDate(0, 0, -7*amount), err == nil && amount >= 0
	}
	if strings.HasSuffix(policy, "m") {
		amount, err := strconv.Atoi(strings.TrimSuffix(policy, "m"))
		return now.AddDate(0, -amount, 0), err == nil && amount >= 0
	}
	return time.Time{}, false
}
