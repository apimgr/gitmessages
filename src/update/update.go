package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var releaseAPI = "https://api.github.com/repos/apimgr/gitmessages/releases?per_page=100"

type Release struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func Check(ctx context.Context, branch, current string, buildEpoch int64) (Release, bool, error) {
	return CheckDeferred(ctx, branch, current, buildEpoch, 0, time.Now())
}

// CheckDeferred applies the scheduled-update age gate to stable and beta releases.
// Devel releases use the build epoch and never use an age gate.
func CheckDeferred(ctx context.Context, branch, current string, buildEpoch int64, deferDays int, now time.Time) (Release, bool, error) {
	if deferDays < 0 {
		deferDays = 0
	}
	client := &http.Client{Timeout: 15 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return Release{}, false, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "gitmessages-update")
	response, err := client.Do(request)
	if err != nil {
		return Release{}, false, fmt.Errorf("query GitHub releases: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return Release{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return Release{}, false, fmt.Errorf("GitHub releases returned %s", response.Status)
	}
	var releases []Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases); err != nil {
		return Release{}, false, fmt.Errorf("decode GitHub releases: %w", err)
	}
	if branch == "devel" {
		sort.SliceStable(releases, func(i, j int) bool { return releases[i].PublishedAt.After(releases[j].PublishedAt) })
	}
	var selected *Release
	for index := range releases {
		candidate := &releases[index]
		if !eligible(candidate, branch) {
			continue
		}
		if branch != "devel" && deferDays > 0 && now.Sub(candidate.PublishedAt) < time.Duration(deferDays)*24*time.Hour {
			continue
		}
		if branch == "devel" {
			if candidate.TagName == "devel" && candidate.PublishedAt.Unix() <= buildEpoch {
				continue
			}
			if selected == nil || candidate.PublishedAt.After(selected.PublishedAt) {
				selected = candidate
			}
			continue
		}
		if selected == nil || selected.TagName == "devel" || compareTags(candidate.TagName, selected.TagName) > 0 {
			selected = candidate
		}
	}
	if selected == nil {
		return Release{}, false, nil
	}
	if normalizeTag(selected.TagName) == normalizeTag(current) {
		return *selected, false, nil
	}
	if compareTags(selected.TagName, current) <= 0 && selected.TagName != "devel" {
		return *selected, false, nil
	}
	return *selected, true, nil
}

func eligible(release *Release, branch string) bool {
	tag := strings.TrimPrefix(release.TagName, "v")
	stable := isStableTag(tag) && !release.Prerelease
	beta := strings.HasSuffix(tag, "-beta") && release.Prerelease
	devel := tag == "devel" && release.Prerelease
	switch branch {
	case "stable":
		return stable
	case "beta":
		return stable || beta
	case "devel":
		return stable || beta || devel
	default:
		return false
	}
}

func isStableTag(tag string) bool {
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func normalizeTag(tag string) string { return strings.TrimPrefix(tag, "v") }

func compareTags(left, right string) int {
	left = normalizeTag(left)
	right = normalizeTag(right)
	leftParts := strings.Split(strings.TrimSuffix(left, "-beta"), ".")
	rightParts := strings.Split(strings.TrimSuffix(right, "-beta"), ".")
	for index := 0; index < 3; index++ {
		var l, r int
		if index < len(leftParts) {
			l, _ = strconv.Atoi(leftParts[index])
		}
		if index < len(rightParts) {
			r, _ = strconv.Atoi(rightParts[index])
		}
		if l > r {
			return 1
		}
		if l < r {
			return -1
		}
	}
	if strings.HasSuffix(left, "-beta") && !strings.HasSuffix(right, "-beta") {
		return -1
	}
	if !strings.HasSuffix(left, "-beta") && strings.HasSuffix(right, "-beta") {
		return 1
	}
	return 0
}

func Install(ctx context.Context, release Release) error {
	assetName := fmt.Sprintf("gitmessages-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	var binaryURL, checksumURL string
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			binaryURL = asset.URL
		case "sha256.txt":
			checksumURL = asset.URL
		}
	}
	if binaryURL == "" || checksumURL == "" {
		return fmt.Errorf("release %s is missing the platform binary or sha256.txt", release.TagName)
	}
	checksumText, err := download(ctx, checksumURL, 1<<20)
	if err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	want, ok := checksumFor(string(checksumText), assetName)
	if !ok {
		return fmt.Errorf("release checksum file has no entry for %s", assetName)
	}
	binary, err := download(ctx, binaryURL, 1<<30)
	if err != nil {
		return fmt.Errorf("download release binary: %w", err)
	}
	sum := sha256.Sum256(binary)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fmt.Errorf("SHA-256 verification failed for %s", assetName)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(executable), ".gitmessages-update-*")
	if err != nil {
		return fmt.Errorf("create update staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	if _, err := temporary.Write(binary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write staged update: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync staged update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged update: %w", err)
	}
	if err := replaceAndRestart(executable, temporaryPath); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	return nil
}

func download(ctx context.Context, address string, maxSize int64) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-HTTPS redirect")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("download exceeds %d bytes", maxSize)
	}
	return data, nil
}

func checksumFor(contents, filename string) (string, bool) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename && len(fields[0]) == sha256.Size*2 {
			return fields[0], true
		}
	}
	return "", false
}
