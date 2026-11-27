package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apimgr/gitmessages/src/paths"
	"golang.org/x/crypto/argon2"
)

const (
	manifestName  = "manifest.json"
	envelopeID    = "BKUP1"
	manifestLimit = 1 << 20
	backupArgonM  = uint32(64 * 1024)
	backupArgonT  = uint32(3)
	backupArgonP  = uint32(2)
)

// EncryptionHeader is the cleartext, authenticated metadata on an encrypted backup.
type EncryptionHeader struct {
	Salt       [16]byte
	MemoryKiB  uint32
	Iterations uint32
	Parallel   uint32
	Nonce      [12]byte
	KeyID      string
	BaseBackup string
	Encoded    []byte
}

// Manifest describes a complete backup archive and its verified files.
type Manifest struct {
	Version          string      `json:"version"`
	CreatedAt        time.Time   `json:"created_at"`
	CreatedBy        string      `json:"created_by,omitempty"`
	AppVersion       string      `json:"app_version,omitempty"`
	Contents         []string    `json:"contents"`
	Encrypted        bool        `json:"encrypted"`
	EncryptionMethod string      `json:"encryption_method,omitempty"`
	SHA256           string      `json:"sha256"`
	Type             string      `json:"type"`
	SnapshotAt       time.Time   `json:"snapshot_at"`
	Files            []FileEntry `json:"files"`
	BaseBackup       string      `json:"base_backup,omitempty"`
	BaseSHA256       string      `json:"base_sha256,omitempty"`
	Deleted          []string    `json:"deleted,omitempty"`
}

// FileEntry is a checksum record for one regular file stored in an archive.
type FileEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	ModTime int64  `json:"mtime"`
}

// Input identifies a source file and its normalized archive path.
type Input struct {
	Path        string
	ArchivePath string
}

// CreateOptions configures an atomic full or incremental archive creation.
type CreateOptions struct {
	Destination string
	Inputs      []Input
	AppVersion  string
	CreatedBy   string
	SnapshotAt  time.Time
	Type        string
	BaseBackup  string
	BaseSHA256  string
	Deleted     []string
	Password    string
	Compliance  bool
}

// RestoreLimits bounds archive input and extraction work.
type RestoreLimits struct {
	MaxArchiveSize  int64
	MaxExpandedSize int64
	MaxFiles        int
	MaxPathLength   int
}

// ErrRestoreLimit identifies an operator-configured restore safety limit.
var ErrRestoreLimit = errors.New("ERR_RESTORE_LIMIT")

// DefaultRestoreLimits returns the limits specified by AI.md PART 22.
func DefaultRestoreLimits() RestoreLimits {
	return RestoreLimits{MaxArchiveSize: 20 << 30, MaxExpandedSize: 50 << 30, MaxFiles: 1000000, MaxPathLength: 4096}
}

// InputsFromPath returns regular files below source with archivePrefix applied.
func InputsFromPath(source, archivePrefix string) ([]Input, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("unsupported backup source type: %s", source)
	}
	if !info.IsDir() {
		name := filepath.ToSlash(filepath.Join(archivePrefix, filepath.Base(source)))
		return []Input{{Path: source, ArchivePath: name}}, nil
	}
	inputs := make([]Input, 0)
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported in backups: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("unsupported backup source type: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(archivePrefix, relative))
		inputs = append(inputs, Input{Path: path, ArchivePath: name})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ArchivePath < inputs[j].ArchivePath })
	return inputs, nil
}

// Create builds and verifies a tar.gz archive, then atomically publishes it.
func Create(ctx context.Context, options CreateOptions) (Manifest, error) {
	if options.Destination == "" {
		return Manifest{}, errors.New("backup destination is required")
	}
	if options.Compliance && options.Password == "" {
		return Manifest{}, errors.New("compliance mode requires server.backup.encryption_password")
	}
	if options.Password != "" && len(options.Password) < 12 {
		return Manifest{}, errors.New("backup encryption password must contain at least 12 characters")
	}
	if options.Type == "" {
		options.Type = "full"
	}
	if options.Type != "full" && options.Type != "incremental" {
		return Manifest{}, fmt.Errorf("unsupported backup type %q", options.Type)
	}
	if options.Type == "incremental" && (options.BaseBackup == "" || !validSHA256(options.BaseSHA256)) {
		return Manifest{}, errors.New("incremental backup requires a base filename and SHA-256")
	}
	if options.SnapshotAt.IsZero() {
		options.SnapshotAt = time.Now().UTC()
	} else {
		options.SnapshotAt = options.SnapshotAt.UTC()
	}
	manifest := Manifest{
		Version: "1.0.0", CreatedAt: time.Now().UTC(), CreatedBy: options.CreatedBy,
		AppVersion: options.AppVersion, Type: options.Type, SnapshotAt: options.SnapshotAt,
		BaseBackup: options.BaseBackup, BaseSHA256: strings.ToLower(options.BaseSHA256),
		Deleted: cleanDeleted(options.Deleted), Encrypted: options.Password != "",
	}
	if manifest.Encrypted {
		manifest.EncryptionMethod = "AES-256-GCM"
	}
	archive, err := createArchive(ctx, options.Inputs, &manifest)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Encrypted {
		archive, err = encrypt(archive, options.Password, manifest.BaseBackup)
		if err != nil {
			return Manifest{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(options.Destination), 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create backup directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(options.Destination), ".gitmessages-backup-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create backup temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return Manifest{}, fmt.Errorf("secure backup temporary file: %w", err)
	}
	if _, err := temporary.Write(archive); err != nil {
		_ = temporary.Close()
		return Manifest{}, fmt.Errorf("write backup: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Manifest{}, fmt.Errorf("sync backup: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close backup: %w", err)
	}
	if _, err := Verify(ctx, temporaryPath, options.Password, DefaultRestoreLimits()); err != nil {
		return Manifest{}, fmt.Errorf("verify created backup: %w", err)
	}
	if err := os.Rename(temporaryPath, options.Destination); err != nil {
		return Manifest{}, fmt.Errorf("publish backup: %w", err)
	}
	return manifest, nil
}

// Verify decrypts if needed and validates archive structure, manifest, checksums, and limits.
func Verify(ctx context.Context, archivePath, password string, limits RestoreLimits) (Manifest, error) {
	return verify(ctx, archivePath, password, normalizedLimits(limits), true)
}

func verify(ctx context.Context, archivePath, password string, limits RestoreLimits, allowIncremental bool) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	limits = normalizedLimits(limits)
	archive, err := readArchiveFile(archivePath, limits.MaxArchiveSize)
	if err != nil {
		return Manifest{}, err
	}
	var header *EncryptionHeader
	if bytes.HasPrefix(archive, []byte(envelopeID)) {
		parsed, parseErr := ParseEncryptionHeader(archive)
		if parseErr != nil {
			return Manifest{}, parseErr
		}
		header = &parsed
	}
	archive, encrypted, err := decryptIfNeeded(archive, password)
	if err != nil {
		return Manifest{}, err
	}
	parent, err := tempParent()
	if err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(parent, paths.ProjectName+"-verify-")
	if err != nil {
		return Manifest{}, fmt.Errorf("create backup verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	manifest, expanded, err := extract(ctx, archive, stage, limits)
	if err != nil {
		return Manifest{}, err
	}
	if expanded > limits.MaxExpandedSize {
		return Manifest{}, fmt.Errorf("%w: backup expanded size exceeds configured limit", ErrRestoreLimit)
	}
	if manifest.Encrypted != encrypted {
		return Manifest{}, errors.New("backup encryption metadata does not match its format")
	}
	if encrypted && manifest.BaseBackup != header.BaseBackup {
		return Manifest{}, errors.New("backup header base does not match manifest")
	}
	if manifest.Type == "incremental" {
		if !allowIncremental {
			return Manifest{}, errors.New("incremental backups cannot be used as a base")
		}
		if filepath.Base(manifest.BaseBackup) != manifest.BaseBackup || manifest.BaseBackup == "." || manifest.BaseBackup == "" {
			return Manifest{}, errors.New("invalid incremental base filename")
		}
		basePath := filepath.Join(filepath.Dir(archivePath), manifest.BaseBackup)
		currentPath, currentErr := filepath.Abs(archivePath)
		baseAbsPath, baseAbsErr := filepath.Abs(basePath)
		if currentErr != nil || baseAbsErr != nil || currentPath == baseAbsPath {
			return Manifest{}, errors.New("invalid incremental base filename")
		}
		baseHash, err := fileSHA256(basePath, limits.MaxArchiveSize)
		if err != nil {
			if errors.Is(err, ErrRestoreLimit) {
				return Manifest{}, err
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_MISSING: %w", err)
		}
		if !strings.EqualFold(baseHash, manifest.BaseSHA256) {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: incremental base checksum mismatch")
		}
		baseManifest, err := verify(ctx, basePath, password, limits, false)
		if err != nil || baseManifest.Type != "full" {
			if errors.Is(err, ErrRestoreLimit) {
				return Manifest{}, err
			}
			if err == nil {
				err = errors.New("incremental base is not a full backup")
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: %w", err)
		}
		if baseManifest.Encrypted != manifest.Encrypted {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: base and incremental encryption settings differ")
		}
		baseExpanded := manifestExpandedSize(baseManifest)
		if baseExpanded > limits.MaxExpandedSize-expanded {
			return Manifest{}, fmt.Errorf("%w: combined backup expanded size exceeds configured limit", ErrRestoreLimit)
		}
		if len(baseManifest.Files)+len(manifest.Files)+2 > limits.MaxFiles {
			return Manifest{}, fmt.Errorf("%w: combined backup contains more files than allowed", ErrRestoreLimit)
		}
	}
	return manifest, nil
}

// Restore verifies an archive or full-plus-incremental pair into a new directory.
// The destination must not exist; callers perform authorization and live-data swaps.
func Restore(ctx context.Context, archivePath, password, destination string, limits RestoreLimits) (Manifest, error) {
	if destination == "" {
		return Manifest{}, errors.New("restore destination is required")
	}
	if _, err := os.Lstat(destination); err == nil {
		return Manifest{}, errors.New("restore destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("inspect restore destination: %w", err)
	}
	manifest, err := Verify(ctx, archivePath, password, limits)
	if err != nil {
		return Manifest{}, err
	}
	limits = normalizedLimits(limits)
	parent, err := tempParent()
	if err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(parent, paths.ProjectName+"-restore-")
	if err != nil {
		return Manifest{}, fmt.Errorf("create restore staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if manifest.Type == "incremental" {
		basePath := filepath.Join(filepath.Dir(archivePath), manifest.BaseBackup)
		baseData, err := readArchiveFile(basePath, limits.MaxArchiveSize)
		if err != nil {
			if errors.Is(err, ErrRestoreLimit) {
				return Manifest{}, err
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_MISSING: %w", err)
		}
		baseData, _, err = decryptIfNeeded(baseData, password)
		if err != nil {
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: %w", err)
		}
		baseStage := filepath.Join(stage, "base")
		if err := os.Mkdir(baseStage, 0o700); err != nil {
			return Manifest{}, err
		}
		baseManifest, baseSize, err := extract(ctx, baseData, baseStage, limits)
		if err != nil || baseManifest.Type != "full" {
			if errors.Is(err, ErrRestoreLimit) {
				return Manifest{}, err
			}
			if err == nil {
				err = errors.New("incremental base is not a full backup")
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: %w", err)
		}
		if baseSize > limits.MaxExpandedSize {
			return Manifest{}, fmt.Errorf("%w: combined backup expanded size exceeds configured limit", ErrRestoreLimit)
		}
		incrementData, err := readArchiveFile(archivePath, limits.MaxArchiveSize)
		if err != nil {
			return Manifest{}, err
		}
		incrementData, _, err = decryptIfNeeded(incrementData, password)
		if err != nil {
			return Manifest{}, err
		}
		incrementStage := filepath.Join(stage, "increment")
		if err := os.Mkdir(incrementStage, 0o700); err != nil {
			return Manifest{}, err
		}
		_, incrementSize, err := extract(ctx, incrementData, incrementStage, limits)
		if err != nil {
			return Manifest{}, err
		}
		if incrementSize > limits.MaxExpandedSize-baseSize {
			return Manifest{}, fmt.Errorf("%w: combined backup expanded size exceeds configured limit", ErrRestoreLimit)
		}
		if err := overlayTree(baseStage, incrementStage); err != nil {
			return Manifest{}, err
		}
		for _, name := range manifest.Deleted {
			target, err := safeTarget(baseStage, name)
			if err != nil {
				return Manifest{}, err
			}
			if err := os.RemoveAll(target); err != nil {
				return Manifest{}, fmt.Errorf("apply incremental deletion: %w", err)
			}
		}
		if err := os.RemoveAll(incrementStage); err != nil {
			return Manifest{}, err
		}
		if err := os.Remove(filepath.Join(baseStage, manifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, err
		}
		if err := os.Rename(baseStage, destination); err != nil {
			return Manifest{}, fmt.Errorf("publish restored backup: %w", err)
		}
		return manifest, nil
	}
	archive, err := readArchiveFile(archivePath, limits.MaxArchiveSize)
	if err != nil {
		return Manifest{}, err
	}
	archive, _, err = decryptIfNeeded(archive, password)
	if err != nil {
		return Manifest{}, err
	}
	if _, _, err := extract(ctx, archive, stage, limits); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(stage, destination); err != nil {
		return Manifest{}, fmt.Errorf("publish restored backup: %w", err)
	}
	return manifest, nil
}

func createArchive(ctx context.Context, inputs []Input, manifest *Manifest) ([]byte, error) {
	files := append([]Input(nil), inputs...)
	sort.Slice(files, func(i, j int) bool { return files[i].ArchivePath < files[j].ArchivePath })
	archive := new(bytes.Buffer)
	compressed := gzip.NewWriter(archive)
	tarWriter := tar.NewWriter(compressed)
	seen := make(map[string]struct{}, len(files))
	entries := make([]FileEntry, 0, len(files))
	for _, input := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.ToSlash(input.ArchivePath)
		if _, err := safeTarget("/backup-root", name); err != nil || name == manifestName {
			return nil, fmt.Errorf("invalid archive path %q", input.ArchivePath)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate archive path %q", name)
		}
		seen[name] = struct{}{}
		info, err := os.Lstat(input.Path)
		if err != nil {
			return nil, fmt.Errorf("inspect backup source %s: %w", input.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("backup source is not a regular file: %s", input.Path)
		}
		file, err := os.Open(input.Path)
		if err != nil {
			return nil, fmt.Errorf("open backup source %s: %w", input.Path, err)
		}
		hash := sha256.New()
		data := new(bytes.Buffer)
		_, copyErr := io.Copy(io.MultiWriter(data, hash), file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(data.Len()) != info.Size() {
			return nil, fmt.Errorf("backup source changed while reading: %s", input.Path)
		}
		size := int64(data.Len())
		header := &tar.Header{Name: name, Mode: 0o600, Size: size, Typeflag: tar.TypeReg, ModTime: info.ModTime().UTC(), Format: tar.FormatPAX}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tarWriter, data); err != nil {
			return nil, err
		}
		entries = append(entries, FileEntry{Path: name, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), ModTime: info.ModTime().Unix()})
	}
	manifest.Files = entries
	manifest.Contents = make([]string, len(entries))
	for i, entry := range entries {
		manifest.Contents[i] = entry.Path
	}
	manifest.SHA256 = manifestChecksum(manifest.Files)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode backup manifest: %w", err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(manifestBytes)), Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt, Format: tar.FormatPAX}); err != nil {
		return nil, err
	}
	if _, err := tarWriter.Write(manifestBytes); err != nil {
		return nil, err
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func extract(ctx context.Context, archive []byte, destination string, limits RestoreLimits) (Manifest, int64, error) {
	compressed := bytes.NewReader(archive)
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return Manifest{}, 0, fmt.Errorf("invalid backup compression: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	actual := make(map[string]FileEntry)
	seenPaths := make(map[string]struct{})
	var manifest Manifest
	var expanded int64
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, expanded, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, expanded, fmt.Errorf("read backup entry: %w", err)
		}
		count++
		if count > limits.MaxFiles {
			return Manifest{}, expanded, fmt.Errorf("%w: backup contains more files than allowed", ErrRestoreLimit)
		}
		if len(header.Name) > limits.MaxPathLength {
			return Manifest{}, expanded, fmt.Errorf("%w: backup path exceeds configured length", ErrRestoreLimit)
		}
		// Accept the zero-valued regular-file marker used by legacy tar archives.
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return Manifest{}, expanded, fmt.Errorf("unsupported backup entry type for %q", header.Name)
		}
		target, err := safeTarget(destination, header.Name)
		if err != nil {
			return Manifest{}, expanded, err
		}
		if _, exists := seenPaths[header.Name]; exists {
			return Manifest{}, expanded, fmt.Errorf("duplicate backup path %q", header.Name)
		}
		seenPaths[header.Name] = struct{}{}
		if header.Size < 0 || header.Size > limits.MaxExpandedSize-expanded {
			return Manifest{}, expanded, fmt.Errorf("%w: backup expanded size exceeds configured limit", ErrRestoreLimit)
		}
		expanded += header.Size
		if header.Name == manifestName {
			if header.Size > manifestLimit {
				return Manifest{}, expanded, errors.New("backup manifest exceeds size limit")
			}
			data, err := io.ReadAll(io.LimitReader(tarReader, manifestLimit+1))
			if err != nil {
				return Manifest{}, expanded, err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return Manifest{}, expanded, fmt.Errorf("invalid backup manifest: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Manifest{}, expanded, err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return Manifest{}, expanded, fmt.Errorf("create restored file: %w", err)
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(file, hash), tarReader, header.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return Manifest{}, expanded, errors.Join(copyErr, closeErr, io.ErrUnexpectedEOF)
		}
		actual[header.Name] = FileEntry{Path: header.Name, Size: written, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if manifest.Version == "" || (manifest.Type != "full" && manifest.Type != "incremental") {
		return Manifest{}, expanded, errors.New("backup manifest is missing or invalid")
	}
	if manifest.Encrypted && manifest.EncryptionMethod != "AES-256-GCM" {
		return Manifest{}, expanded, errors.New("unsupported backup encryption method")
	}
	if !manifest.Encrypted && manifest.EncryptionMethod != "" {
		return Manifest{}, expanded, errors.New("unencrypted backup has unexpected encryption metadata")
	}
	if !validSHA256(manifest.SHA256) || manifestChecksum(manifest.Files) != manifest.SHA256 {
		return Manifest{}, expanded, errors.New("backup manifest checksum mismatch")
	}
	if err := verifyEntries(manifest, actual); err != nil {
		return Manifest{}, expanded, err
	}
	return manifest, expanded, nil
}

func verifyEntries(manifest Manifest, actual map[string]FileEntry) error {
	if len(actual) != len(manifest.Files) {
		return errors.New("backup file list does not match manifest")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, entry := range manifest.Files {
		if _, ok := seen[entry.Path]; ok {
			return fmt.Errorf("duplicate manifest path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		got, ok := actual[entry.Path]
		if !ok {
			return fmt.Errorf("backup content is missing for %q", entry.Path)
		}
		if got.Size != entry.Size {
			return fmt.Errorf("backup content size mismatch for %q: got %d, want %d", entry.Path, got.Size, entry.Size)
		}
		if !strings.EqualFold(got.SHA256, entry.SHA256) {
			return fmt.Errorf("backup content checksum mismatch for %q", entry.Path)
		}
	}
	if len(manifest.Contents) != len(manifest.Files) {
		return errors.New("backup contents list does not match manifest files")
	}
	for index, entry := range manifest.Files {
		if manifest.Contents[index] != entry.Path {
			return errors.New("backup contents list does not match manifest files")
		}
	}
	if manifest.Type == "incremental" {
		if manifest.BaseBackup == "" || filepath.Base(manifest.BaseBackup) != manifest.BaseBackup || !validSHA256(manifest.BaseSHA256) {
			return errors.New("incremental backup is missing valid base metadata")
		}
	} else if manifest.BaseBackup != "" || manifest.BaseSHA256 != "" || len(manifest.Deleted) != 0 {
		return errors.New("full backup contains incremental metadata")
	}
	deleted := make(map[string]struct{}, len(manifest.Deleted))
	for _, name := range manifest.Deleted {
		if _, err := safeTarget("/backup-root", name); err != nil || name == manifestName {
			return fmt.Errorf("invalid deleted path %q in manifest", name)
		}
		if _, ok := deleted[name]; ok {
			return fmt.Errorf("duplicate deleted path %q in manifest", name)
		}
		deleted[name] = struct{}{}
	}
	return nil
}

func manifestExpandedSize(manifest Manifest) int64 {
	var total int64
	for _, entry := range manifest.Files {
		total += entry.Size
	}
	data, _ := json.Marshal(manifest)
	return total + int64(len(data))
}

func manifestChecksum(files []FileEntry) string {
	entries := append([]FileEntry(nil), files...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	hash := sha256.New()
	for _, entry := range entries {
		_, _ = fmt.Fprintf(hash, "%s  %s\n", strings.ToLower(entry.SHA256), entry.Path)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func encrypt(plain []byte, password, baseBackup string) ([]byte, error) {
	if len(baseBackup) > 255 || !utf8.ValidString(baseBackup) || baseBackup != "" && (filepath.Base(baseBackup) != baseBackup || strings.ContainsAny(baseBackup, `/\\`)) {
		return nil, errors.New("invalid encrypted backup base filename")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate backup salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, backupArgonT, backupArgonM, uint8(backupArgonP), 32)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate backup nonce: %w", err)
	}
	keyID := backupKeyID(key)
	header := make([]byte, 0, 55+len(baseBackup))
	header = append(header, envelopeID...)
	header = append(header, salt...)
	var parameters [12]byte
	binary.BigEndian.PutUint32(parameters[0:4], backupArgonM)
	binary.BigEndian.PutUint32(parameters[4:8], backupArgonT)
	binary.BigEndian.PutUint32(parameters[8:12], backupArgonP)
	header = append(header, parameters[:]...)
	header = append(header, nonce...)
	header = append(header, keyID...)
	var baseLength [2]byte
	binary.BigEndian.PutUint16(baseLength[:], uint16(len(baseBackup)))
	header = append(header, baseLength[:]...)
	header = append(header, baseBackup...)
	result := append([]byte(nil), header...)
	result = aead.Seal(result, nonce, plain, header)
	return result, nil
}

func decryptIfNeeded(data []byte, password string) ([]byte, bool, error) {
	if !bytes.HasPrefix(data, []byte(envelopeID)) {
		return data, false, nil
	}
	header, err := ParseEncryptionHeader(data)
	if err != nil {
		return nil, true, err
	}
	if password == "" {
		return nil, true, errors.New("encrypted backup requires a password")
	}
	if len(data) < len(header.Encoded)+16 {
		return nil, true, errors.New("invalid encrypted backup format")
	}
	key := argon2.IDKey([]byte(password), header.Salt[:], header.Iterations, header.MemoryKiB, uint8(header.Parallel), 32)
	defer clear(key)
	if !hmac.Equal([]byte(backupKeyID(key)), []byte(header.KeyID)) {
		return nil, true, errors.New("invalid backup password or corrupted encrypted backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, true, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, true, err
	}
	plain, err := aead.Open(nil, header.Nonce[:], data[len(header.Encoded):], header.Encoded)
	if err != nil {
		return nil, true, errors.New("invalid backup password or corrupted encrypted backup")
	}
	return plain, true, nil
}

// ParseEncryptionHeader parses authenticated metadata without requiring the password.
func ParseEncryptionHeader(data []byte) (EncryptionHeader, error) {
	const fixedLength = 55
	if len(data) < fixedLength || !bytes.HasPrefix(data, []byte(envelopeID)) {
		return EncryptionHeader{}, errors.New("invalid encrypted backup format")
	}
	var header EncryptionHeader
	copy(header.Salt[:], data[5:21])
	header.MemoryKiB = binary.BigEndian.Uint32(data[21:25])
	header.Iterations = binary.BigEndian.Uint32(data[25:29])
	header.Parallel = binary.BigEndian.Uint32(data[29:33])
	copy(header.Nonce[:], data[33:45])
	header.KeyID = string(data[45:53])
	baseLength := int(binary.BigEndian.Uint16(data[53:55]))
	if header.MemoryKiB < 8*1024 || header.MemoryKiB > 1024*1024 || header.Iterations == 0 || header.Iterations > 10 || header.Parallel == 0 || header.Parallel > 16 || header.MemoryKiB < 8*header.Parallel {
		return EncryptionHeader{}, errors.New("invalid encrypted backup Argon2 parameters")
	}
	if baseLength > 255 || len(data) < fixedLength+baseLength {
		return EncryptionHeader{}, errors.New("invalid encrypted backup format")
	}
	if _, err := hex.DecodeString(header.KeyID); err != nil || len(header.KeyID) != 8 {
		return EncryptionHeader{}, errors.New("invalid encrypted backup key identifier")
	}
	header.BaseBackup = string(data[fixedLength : fixedLength+baseLength])
	if !utf8.ValidString(header.BaseBackup) || header.BaseBackup != "" && (filepath.Base(header.BaseBackup) != header.BaseBackup || header.BaseBackup == "." || header.BaseBackup == ".." || strings.ContainsAny(header.BaseBackup, `/\\`)) {
		return EncryptionHeader{}, errors.New("invalid encrypted backup base filename")
	}
	header.Encoded = append([]byte(nil), data[:fixedLength+baseLength]...)
	return header, nil
}

// ReadEncryptionHeader reads cleartext encrypted-archive metadata without a password.
func ReadEncryptionHeader(path string) (EncryptionHeader, error) {
	file, err := os.Open(path)
	if err != nil {
		return EncryptionHeader{}, err
	}
	defer func() { _ = file.Close() }()
	fixed := make([]byte, 55)
	if _, err := io.ReadFull(file, fixed); err != nil {
		return EncryptionHeader{}, errors.New("invalid encrypted backup format")
	}
	baseLength := int(binary.BigEndian.Uint16(fixed[53:55]))
	if baseLength > 255 {
		return EncryptionHeader{}, errors.New("invalid encrypted backup format")
	}
	data := append([]byte(nil), fixed...)
	if baseLength > 0 {
		base := make([]byte, baseLength)
		if _, err := io.ReadFull(file, base); err != nil {
			return EncryptionHeader{}, errors.New("invalid encrypted backup format")
		}
		data = append(data, base...)
	}
	return ParseEncryptionHeader(data)
}

// MatchesPassword reports whether password derives the key identifier in header.
func (header EncryptionHeader) MatchesPassword(password string) bool {
	key := argon2.IDKey([]byte(password), header.Salt[:], header.Iterations, header.MemoryKiB, uint8(header.Parallel), 32)
	defer clear(key)
	return hmac.Equal([]byte(backupKeyID(key)), []byte(header.KeyID))
}

func backupKeyID(key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("key-id"))
	return hex.EncodeToString(mac.Sum(nil))[:8]
}

func readArchiveFile(path string, maxSize int64) ([]byte, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect backup archive: %w", err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Size() <= 0 {
		return nil, errors.New("backup archive must be a regular non-empty file within the configured size limit")
	}
	if linkInfo.Size() > maxSize {
		return nil, fmt.Errorf("%w: backup archive exceeds configured size limit", ErrRestoreLimit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened backup archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, errors.New("backup archive must be a regular non-empty file within the configured size limit")
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("%w: backup archive exceeds configured size limit", ErrRestoreLimit)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read backup archive: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("%w: backup archive exceeds configured size limit", ErrRestoreLimit)
	}
	return data, nil
}

func fileSHA256(path string, maxSize int64) (string, error) {
	data, err := readArchiveFile(path, maxSize)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func safeTarget(root, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\\') || strings.ContainsRune(name, 0) || strings.Contains(name, ":") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

func cleanDeleted(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, name := range paths {
		if _, err := safeTarget("/backup-root", name); err == nil && name != manifestName {
			result = append(result, filepath.ToSlash(filepath.Clean(name)))
		}
	}
	sort.Strings(result)
	return result
}

func overlayTree(destination, source string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("invalid entry in staged incremental")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, input.Close(), output.Close())
	})
}

func tempParent() (string, error) {
	parent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create project temp parent: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", fmt.Errorf("secure project temp parent: %w", err)
	}
	return parent, nil
}

func normalizedLimits(limits RestoreLimits) RestoreLimits {
	defaults := DefaultRestoreLimits()
	if limits.MaxArchiveSize <= 0 {
		limits.MaxArchiveSize = defaults.MaxArchiveSize
	}
	if limits.MaxExpandedSize <= 0 {
		limits.MaxExpandedSize = defaults.MaxExpandedSize
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxPathLength <= 0 {
		limits.MaxPathLength = defaults.MaxPathLength
	}
	return limits
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
