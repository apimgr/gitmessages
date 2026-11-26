package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
)

var cliPlatformName = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

type cliBinaryVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

func cliBinaryDirectory() string {
	directories := paths.GetDirectories()
	configured := strings.TrimSpace(config.Get().CLI.BinaryDownload.Dir)
	if configured == "" {
		configured = filepath.Join(directories.Data, "cli")
	}
	configured = strings.ReplaceAll(configured, "{data_dir}", directories.Data)
	if !filepath.IsAbs(configured) {
		configured = filepath.Join(directories.Data, configured)
	}
	return filepath.Clean(configured)
}

func cliBinaryVersions() map[string]cliBinaryVersion {
	result := make(map[string]cliBinaryVersion)
	directory := cliBinaryDirectory()
	versionData, err := readCLIManifest(filepath.Join(directory, "version.txt"), 256)
	if err != nil {
		return result
	}
	version := strings.TrimSpace(string(versionData))
	if version == "" {
		return result
	}
	checksums, err := readCLIManifest(filepath.Join(directory, "sha256.txt"), 1<<20)
	if err != nil {
		return result
	}
	for _, entry := range mustReadDirectory(directory) {
		name := entry.Name()
		if !strings.HasPrefix(name, paths.ProjectName+"-cli-") {
			continue
		}
		platform := strings.TrimPrefix(name, paths.ProjectName+"-cli-")
		platform = strings.TrimSuffix(platform, ".exe")
		if !cliPlatformName.MatchString(platform) {
			continue
		}
		filePath := filepath.Join(directory, name)
		fileInfo, err := os.Lstat(filePath)
		if err != nil || !fileInfo.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(filePath)
		if err != nil {
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<30 {
			_ = file.Close()
			continue
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		_ = file.Close()
		if copyErr != nil {
			continue
		}
		actual := hex.EncodeToString(hash.Sum(nil))
		if expected := cliChecksum(string(checksums), name); expected != actual {
			slog.Warn("CLI binary checksum does not match release metadata", "file", name)
			continue
		}
		result[platform] = cliBinaryVersion{Version: version, SHA256: actual}
	}
	return result
}

func readCLIManifest(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(path)
}

func mustReadDirectory(path string) []os.DirEntry {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	return entries
}

func cliChecksum(contents, filename string) string {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename && len(fields[0]) == sha256.Size*2 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

func serveCLIBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	settings := config.Get().CLI.BinaryDownload
	if settings.RequireAuth && !hasValidBearer(r.Context(), r, currentAuthStore()) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/cli/binaries/")
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) || !strings.HasPrefix(name, paths.ProjectName+"-cli-") {
		http.NotFound(w, r)
		return
	}
	platform := strings.TrimSuffix(strings.TrimPrefix(name, paths.ProjectName+"-cli-"), ".exe")
	if !cliPlatformName.MatchString(platform) {
		http.NotFound(w, r)
		return
	}
	directory := cliBinaryDirectory()
	checksums, err := readCLIManifest(filepath.Join(directory, "sha256.txt"), 1<<20)
	if err != nil || cliChecksum(string(checksums), name) == "" {
		http.NotFound(w, r)
		return
	}
	filePath := filepath.Join(directory, name)
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<30 {
		http.NotFound(w, r)
		return
	}
	listedInfo := info
	file, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(listedInfo, info) {
		http.NotFound(w, r)
		return
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || hex.EncodeToString(hash.Sum(nil)) != cliChecksum(string(checksums), name) {
		slog.Warn("CLI binary checksum does not match release metadata", "file", name)
		http.NotFound(w, r)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if store := currentAuthStore(); store != nil {
		details, _ := json.Marshal(map[string]string{"platform": platform, "version": strings.TrimSpace(readText(filepath.Join(directory, "version.txt"))), "ip": requestClientIP(r)})
		_ = store.Database().Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','cli','cli.update_started',?,'cli',?,?,1)`, requestClientIP(r), platform, string(details))
			return err
		})
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func recordRejectedCLIToken(r *http.Request) {
	if r == nil || !strings.HasPrefix(r.UserAgent(), paths.ProjectName+"-cli/") {
		return
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return
	}
	store := currentAuthStore()
	if store == nil {
		return
	}
	prefix, rejected := store.RejectedBearerPrefix(r.Context(), strings.TrimSpace(value[7:]))
	if !rejected {
		return
	}
	details, _ := json.Marshal(map[string]string{"token_prefix": prefix})
	_ = store.Database().Write(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','cli','cli.token_rejected',?,'token',?,?,0)`, requestClientIP(r), prefix, string(details))
		return err
	})
}

func readText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
