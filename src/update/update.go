package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/paths"
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

type stagedRelease struct {
	TagName   string    `json:"tag_name"`
	Published time.Time `json:"published_at"`
	AssetName string    `json:"asset_name"`
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

func stripUpdateArguments(arguments []string) []string {
	if len(arguments) == 0 {
		return nil
	}
	result := []string{arguments[0]}
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--update" {
			if index+1 < len(arguments) && (arguments[index+1] == "yes" || arguments[index+1] == "check" || arguments[index+1] == "branch") {
				index++
				if arguments[index] == "branch" && index+1 < len(arguments) {
					index++
				}
			}
			continue
		}
		if strings.HasPrefix(argument, "--update=") {
			continue
		}
		if argument == "--maintenance" && index+1 < len(arguments) && arguments[index+1] == "update" {
			index++
			continue
		}
		result = append(result, argument)
	}
	return result
}

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
	assetName := platformAssetName()
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

// InstallFromURL installs a checksum-pinned binary distributed by the configured server.
func InstallFromURL(ctx context.Context, binaryURL, expectedSHA256, currentVersion, nextVersion string) error {
	return installFromURL(ctx, binaryURL, expectedSHA256, currentVersion, nextVersion, "")
}

// InstallFromURLWithBearerToken installs a checksum-pinned binary using a bearer token for a private same-origin endpoint.
func InstallFromURLWithBearerToken(ctx context.Context, binaryURL, expectedSHA256, currentVersion, nextVersion, bearerToken string) error {
	return installFromURL(ctx, binaryURL, expectedSHA256, currentVersion, nextVersion, bearerToken)
}

func installFromURL(ctx context.Context, binaryURL, expectedSHA256, currentVersion, nextVersion, bearerToken string) error {
	if !httpsURL(binaryURL) {
		return errors.New("CLI update downloads require HTTPS")
	}
	if len(expectedSHA256) != sha256.Size*2 {
		return errors.New("CLI update has an invalid SHA-256 checksum")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return errors.New("CLI update has an invalid SHA-256 checksum")
	}
	binary, err := downloadWithBearerToken(ctx, binaryURL, 1<<30, bearerToken)
	if err != nil {
		return fmt.Errorf("download CLI update: %w", err)
	}
	tempParent := filepath.Join(os.TempDir(), paths.OrgName)
	if err := os.MkdirAll(tempParent, 0o755); err != nil {
		return fmt.Errorf("prepare CLI update temp directory: %w", err)
	}
	tempDir, err := os.MkdirTemp(tempParent, paths.ProjectName+"-")
	if err != nil {
		return fmt.Errorf("create CLI update temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	updatePath := filepath.Join(tempDir, "cli.update.tmp")
	if err := os.WriteFile(updatePath, binary, 0o600); err != nil {
		return fmt.Errorf("write CLI update temp file: %w", err)
	}
	verifiedBinary, err := os.ReadFile(updatePath)
	if err != nil {
		return fmt.Errorf("read CLI update temp file: %w", err)
	}
	actual := sha256.Sum256(verifiedBinary)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expectedSHA256) {
		return errors.New("CLI update SHA-256 verification failed")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate CLI binary: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve CLI binary: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("inspect CLI binary: %w", err)
	}
	parent := filepath.Dir(executable)
	temporary, err := os.CreateTemp(parent, ".gitmessages-cli-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("you do not have permission to update %s; ask your admin or move the binary to a writable path", executable)
		}
		return fmt.Errorf("create staged CLI update: %w", err)
	}
	stagePath := temporary.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure staged CLI update: %w", err)
	}
	if _, err := temporary.Write(verifiedBinary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write staged CLI update: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync staged CLI update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged CLI update: %w", err)
	}
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("clean CLI update temp directory: %w", err)
	}
	_ = os.Setenv("GITMESSAGES_CLI_UPDATE_FROM", currentVersion)
	_ = os.Setenv("GITMESSAGES_CLI_UPDATE_TO", nextVersion)
	if err := replaceAndRestart(executable, stagePath); err != nil {
		return fmt.Errorf("replace and restart CLI: %w", err)
	}
	return nil
}

// Stage downloads and verifies a release into a service-writable update directory.
func Stage(ctx context.Context, release Release, directory, projectName string) error {
	assetName := platformAssetName()
	binaryURL, checksumURL := releaseAssetURLs(release, assetName)
	if binaryURL == "" || checksumURL == "" {
		return fmt.Errorf("release %s is missing the platform binary or sha256.txt", release.TagName)
	}
	if !httpsURL(binaryURL) || !httpsURL(checksumURL) {
		return fmt.Errorf("release assets must use HTTPS")
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
	if !matchesSHA256(binary, want) {
		return fmt.Errorf("SHA-256 verification failed for %s", assetName)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create update staging directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure update staging directory: %w", err)
	}
	metadata, err := json.Marshal(stagedRelease{TagName: release.TagName, Published: release.PublishedAt, AssetName: assetName})
	if err != nil {
		return fmt.Errorf("encode staged release metadata: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, projectName+".new"), binary, 0o755); err != nil {
		return fmt.Errorf("stage update binary: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, projectName+".sha256"), []byte(strings.ToLower(want)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write informational update checksum: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, projectName+".json"), metadata, 0o600); err != nil {
		return fmt.Errorf("write staged release metadata: %w", err)
	}
	return nil
}

// ApplyStaged verifies a staged binary against a fresh official release checksum and applies it.
func ApplyStaged(ctx context.Context, directory, projectName, executable, currentVersion string, buildEpoch int64, startedElevated bool) (bool, error) {
	if runtime.GOOS == "windows" || !startedElevated {
		return false, nil
	}
	stageDir := filepath.Join(directory, "update")
	metadataPath := filepath.Join(stageDir, projectName+".json")
	metadataBytes, err := os.ReadFile(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, stageErr := os.Stat(stageDir); errors.Is(stageErr, os.ErrNotExist) {
			return false, nil
		}
		return false, failAndClearStaged(stageDir, errors.New("staged update metadata is missing"))
	}
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("read staged update metadata: %w", err))
	}
	var metadata stagedRelease
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil || metadata.TagName == "" || metadata.AssetName != platformAssetName() {
		return false, failAndClearStaged(stageDir, errors.New("staged update metadata is invalid"))
	}
	release, err := officialRelease(ctx, metadata.TagName)
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("re-fetch official release checksum: %w", err))
	}
	assetName := platformAssetName()
	binaryURL, checksumURL := releaseAssetURLs(release, assetName)
	if binaryURL == "" || checksumURL == "" {
		return false, failAndClearStaged(stageDir, fmt.Errorf("official release %s is missing the platform binary or checksum", metadata.TagName))
	}
	if !httpsURL(binaryURL) || !httpsURL(checksumURL) {
		return false, failAndClearStaged(stageDir, errors.New("official release assets must use HTTPS"))
	}
	checksumText, err := download(ctx, checksumURL, 1<<20)
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("re-fetch official release checksum: %w", err))
	}
	want, ok := checksumFor(string(checksumText), assetName)
	if !ok {
		return false, failAndClearStaged(stageDir, fmt.Errorf("official checksum has no entry for %s", assetName))
	}
	if !releaseIsNewer(metadata.TagName, release.PublishedAt, currentVersion, buildEpoch) {
		return false, failAndClearStaged(stageDir, fmt.Errorf("staged update %s is not newer than running version %s", metadata.TagName, currentVersion))
	}
	stagedPath := filepath.Join(stageDir, projectName+".new")
	binary, err := readRegularFile(stagedPath, 1<<30)
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("read staged update binary: %w", err))
	}
	if !matchesSHA256(binary, want) {
		return false, failAndClearStaged(stageDir, errors.New("staged update checksum does not match the official release"))
	}
	if _, err := os.Stat(executable); err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("inspect current executable: %w", err))
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("resolve current executable: %w", err))
	}
	info, err := os.Stat(executable)
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("inspect resolved executable: %w", err))
	}
	temporary, err := os.CreateTemp(filepath.Dir(executable), ".gitmessages-update-*")
	if err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("create privileged update file: %w", err))
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return false, failAndClearStaged(stageDir, fmt.Errorf("set privileged update permissions: %w", err))
	}
	if _, err := temporary.Write(binary); err != nil {
		_ = temporary.Close()
		return false, failAndClearStaged(stageDir, fmt.Errorf("copy verified update: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, failAndClearStaged(stageDir, fmt.Errorf("sync verified update: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return false, failAndClearStaged(stageDir, fmt.Errorf("close verified update: %w", err))
	}
	if err := os.RemoveAll(stageDir); err != nil {
		return false, fmt.Errorf("remove staged update files: %w", err)
	}
	if err := replaceAndRestart(executable, temporaryPath); err != nil {
		return false, fmt.Errorf("replace and restart executable: %w", err)
	}
	return true, nil
}

func officialRelease(ctx context.Context, tag string) (Release, error) {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("refusing non-HTTPS redirect")
		}
		return nil
	}}
	if !httpsURL(releaseAPI) {
		return Release{}, errors.New("official release API must use HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "gitmessages-update-verify")
	response, err := client.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub releases returned %s", response.Status)
	}
	var releases []Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases); err != nil {
		return Release{}, fmt.Errorf("decode official releases: %w", err)
	}
	for _, release := range releases {
		if release.TagName == tag {
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("official release %q was not found", tag)
}

func releaseAssetURLs(release Release, assetName string) (string, string) {
	var binaryURL, checksumURL string
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			binaryURL = asset.URL
		case "sha256.txt":
			checksumURL = asset.URL
		}
	}
	return binaryURL, checksumURL
}

func platformAssetName() string {
	assetName := fmt.Sprintf("gitmessages-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	return assetName
}

func releaseIsNewer(tag string, published time.Time, current string, buildEpoch int64) bool {
	if tag == "devel" {
		return published.Unix() > buildEpoch
	}
	return compareTags(tag, current) > 0
}

func matchesSHA256(data []byte, digest string) bool {
	sum := sha256.Sum256(data)
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(digest))
}

func httpsURL(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func readRegularFile(path string, maxSize int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSize {
		return nil, errors.New("staged file is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, errors.New("staged file exceeds its size limit")
	}
	return data, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".stage-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
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

func failAndClearStaged(directory string, err error) error {
	if removeErr := os.RemoveAll(directory); removeErr != nil {
		return errors.Join(err, fmt.Errorf("remove failed staged update: %w", removeErr))
	}
	return err
}

func download(ctx context.Context, address string, maxSize int64) ([]byte, error) {
	return downloadWithBearerToken(ctx, address, maxSize, "")
}

func downloadWithBearerToken(ctx context.Context, address string, maxSize int64, bearerToken string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-HTTPS redirect")
		}
		if bearerToken != "" && (len(via) == 0 || !sameUpdateOrigin(via[0].URL, request.URL)) {
			return fmt.Errorf("refusing cross-origin redirect for authenticated CLI update")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
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

func sameUpdateOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && left.User == nil && right.User == nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
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
