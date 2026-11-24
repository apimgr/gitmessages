package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxBrandingFileBytes   = 2 * 1024 * 1024
	maxBrandingDataBytes   = 256 * 1024
	maxBrandingRemoteBytes = 10 * 1024 * 1024
)

var (
	brandingImageMu       sync.Mutex
	brandingImageWarnings sync.Map
)

type brandingImageData struct {
	body     []byte
	typeName string
}

func brandingImage(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/.well-known/branding/")
	var value string
	switch kind {
	case "logo":
		value = config.Get().Server.Branding.Logo
	case "favicon":
		value = config.Get().Server.Branding.Favicon
	case "og-image":
		value = config.Get().Server.SEO.OGImage
	default:
		http.NotFound(w, r)
		return
	}
	imageData, err := loadBrandingImage(r.Context(), value)
	if err != nil {
		key := "server.branding." + kind
		if kind == "og-image" {
			key = "server.seo.og_image"
		}
		if _, loaded := brandingImageWarnings.LoadOrStore(key, struct{}{}); !loaded {
			slog.Warn("branding image unavailable; using embedded default", "key", key)
		}
		imageData = brandingImageData{body: []byte(defaultIconSVG), typeName: "image/svg+xml"}
	}
	if kind == "logo" {
		if width, parseErr := strconv.Atoi(r.URL.Query().Get("width")); parseErr == nil && (width == 50 || width == 200) {
			if scaled, scaleErr := cachedScaledBrandingPNG(imageData.body, width, 0, false); scaleErr == nil {
				imageData = brandingImageData{body: scaled, typeName: "image/png"}
			}
		}
	}
	if kind == "favicon" {
		if size, parseErr := strconv.Atoi(r.URL.Query().Get("size")); parseErr == nil && validBrandingFaviconSize(size) {
			if scaled, scaleErr := cachedScaledBrandingPNG(imageData.body, size, size, false); scaleErr == nil {
				imageData = brandingImageData{body: scaled, typeName: "image/png"}
			}
		}
	}
	if kind == "og-image" && r.URL.Query().Get("size") == "1200x630" {
		if scaled, scaleErr := cachedScaledBrandingPNG(imageData.body, 1200, 630, true); scaleErr == nil {
			imageData = brandingImageData{body: scaled, typeName: "image/png"}
		}
	}
	w.Header().Set("Content-Type", imageData.typeName)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !setBrandingImageCacheHeaders(w, r, imageData.body) || r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(imageData.body)
}

func setBrandingImageCacheHeaders(w http.ResponseWriter, r *http.Request, body []byte) bool {
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return false
	}
	return true
}

func brandingHeaderMarkup(r *http.Request) string {
	branding := config.Get().Server.Branding
	if strings.TrimSpace(branding.Logo) == "" {
		return `<strong>` + html.EscapeString(seoBrandingTitle()) + `</strong>`
	}
	logoURL := requestBasePath(r) + "/.well-known/branding/logo"
	return `<img src="` + html.EscapeString(logoURL+"?width=200") + `" srcset="` + html.EscapeString(logoURL+"?width=50 50w, "+logoURL+"?width=200 200w") + `" sizes="(max-width: 767px) 50px, 200px" alt="` + html.EscapeString(seoBrandingTitle()) + `">`
}

func brandingPublicImageURL(r *http.Request, kind, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	imageURL := BuildURL(r, "/.well-known/branding/"+kind)
	if kind == "og-image" {
		imageURL += "?size=1200x630"
	}
	return imageURL
}

func validBrandingFaviconSize(size int) bool {
	switch size {
	case 16, 32, 48, 180, 192, 512:
		return true
	default:
		return false
	}
}

func cachedScaledBrandingPNG(source []byte, width, height int, cover bool) ([]byte, error) {
	if width < 1 || height < 0 || width > 1200 || height > 630 {
		return nil, fmt.Errorf("invalid image dimensions")
	}
	imageConfig, format, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil || format == "" || imageConfig.Width < 1 || imageConfig.Height < 1 || int64(imageConfig.Width)*int64(imageConfig.Height) > 40_000_000 {
		return nil, fmt.Errorf("image cannot be rasterized")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(source))
	if err != nil || decodedFormat != format {
		return nil, fmt.Errorf("image cannot be rasterized")
	}
	imageBounds := decoded.Bounds()
	sourceWidth, sourceHeight := imageBounds.Dx(), imageBounds.Dy()
	if sourceWidth != imageConfig.Width || sourceHeight != imageConfig.Height {
		return nil, fmt.Errorf("image dimensions exceed limit")
	}
	if height == 0 {
		height = max(1, int(int64(sourceHeight)*int64(width)/int64(sourceWidth)))
	}
	if height > 630 {
		return nil, fmt.Errorf("scaled image dimensions exceed limit")
	}
	digestInput := make([]byte, 0, len(source)+32)
	digestInput = append(digestInput, source...)
	digestInput = append(digestInput, []byte(fmt.Sprintf("\x00%d:%d:%t", width, height, cover))...)
	digest := sha256.Sum256(digestInput)
	brandingImageMu.Lock()
	defer brandingImageMu.Unlock()
	cacheDir, err := brandingImageCacheDirectory()
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(digest[:])+".png")
	if info, statErr := os.Lstat(cachePath); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() > 8 && info.Size() <= maxBrandingRemoteBytes {
		if cached, readErr := os.ReadFile(cachePath); readErr == nil && len(cached) >= 8 && string(cached[:8]) == "\x89PNG\r\n\x1a\n" {
			return cached, nil
		}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	sourceRect := imageBounds
	if cover {
		sourceAspect := float64(sourceWidth) / float64(sourceHeight)
		targetAspect := float64(width) / float64(height)
		if sourceAspect > targetAspect {
			cropWidth := int(float64(sourceHeight) * targetAspect)
			sourceRect.Min.X += (sourceWidth - cropWidth) / 2
			sourceRect.Max.X = sourceRect.Min.X + cropWidth
		} else if sourceAspect < targetAspect {
			cropHeight := int(float64(sourceWidth) / targetAspect)
			sourceRect.Min.Y += (sourceHeight - cropHeight) / 2
			sourceRect.Max.Y = sourceRect.Min.Y + cropHeight
		}
		xdraw.ApproxBiLinear.Scale(canvas, canvas.Bounds(), decoded, sourceRect, draw.Src, nil)
	} else {
		scale := min(float64(width)/float64(sourceWidth), float64(height)/float64(sourceHeight))
		destWidth := max(1, int(float64(sourceWidth)*scale))
		destHeight := max(1, int(float64(sourceHeight)*scale))
		destination := image.Rect((width-destWidth)/2, (height-destHeight)/2, (width+destWidth)/2, (height+destHeight)/2)
		xdraw.ApproxBiLinear.Scale(canvas, destination, decoded, sourceRect, draw.Over, nil)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, err
	}
	if err := writeBrandingCache(cachePath, cacheDir, encoded.Bytes()); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func brandingImageCacheDirectory() (string, error) {
	configDir := config.ConfigDirectory()
	if configDir == "" {
		return "", fmt.Errorf("configuration directory is unavailable")
	}
	cacheRoot := filepath.Join(configDir, "cache")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if info, err := os.Lstat(cacheRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("image cache root is invalid")
	}
	cacheDir := filepath.Join(cacheRoot, "branding-images")
	if err := os.Mkdir(cacheDir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if info, err := os.Lstat(cacheDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("image cache directory is invalid")
	}
	return cacheDir, nil
}

func writeBrandingCache(destination, directory string, data []byte) error {
	temporary, err := os.CreateTemp(directory, ".branding-scaled-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, destination)
}

func loadBrandingImage(ctx context.Context, value string) (brandingImageData, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return brandingImageData{}, fmt.Errorf("empty image source")
	}
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		return decodeBrandingDataURI(value)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return brandingImageData{}, err
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return fetchCachedBrandingImage(ctx, value)
	}
	if parsed.Scheme != "" || parsed.Host != "" || strings.ContainsAny(value, `/\\`) || value == "." || value == ".." {
		return brandingImageData{}, fmt.Errorf("invalid asset filename")
	}
	configDir := config.ConfigDirectory()
	if configDir == "" {
		return brandingImageData{}, fmt.Errorf("configuration directory is unavailable")
	}
	assetExt := strings.ToLower(filepath.Ext(value))
	switch assetExt {
	case ".svg", ".png", ".ico", ".webp", ".jpg":
	default:
		return brandingImageData{}, fmt.Errorf("unsupported asset filename extension")
	}
	assetsDir := filepath.Join(configDir, "assets")
	assetsInfo, err := os.Lstat(assetsDir)
	if err != nil || !assetsInfo.IsDir() || assetsInfo.Mode()&os.ModeSymlink != 0 {
		return brandingImageData{}, fmt.Errorf("asset directory is unavailable")
	}
	assetPath := filepath.Join(assetsDir, value)
	info, err := os.Lstat(assetPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBrandingFileBytes {
		return brandingImageData{}, fmt.Errorf("asset file is unavailable or invalid")
	}
	body, err := os.ReadFile(assetPath)
	if err != nil {
		return brandingImageData{}, err
	}
	typeName, err := brandingImageType(filepath.Ext(value), body)
	if err != nil {
		return brandingImageData{}, err
	}
	return brandingImageData{body: body, typeName: typeName}, nil
}

func decodeBrandingDataURI(value string) (brandingImageData, error) {
	header, payload, ok := strings.Cut(value, ",")
	if !ok || len(value) > maxBrandingDataBytes {
		return brandingImageData{}, fmt.Errorf("invalid or oversized image data URI")
	}
	media := strings.ToLower(strings.TrimPrefix(strings.SplitN(header, ";", 2)[0], "data:"))
	if !allowedBrandingImageType(media) {
		return brandingImageData{}, fmt.Errorf("unsupported data URI image type")
	}
	var body []byte
	var err error
	if strings.HasSuffix(strings.ToLower(header), ";base64") {
		body, err = base64.StdEncoding.DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		body = []byte(decoded)
	}
	if err != nil || len(body) > maxBrandingDataBytes {
		return brandingImageData{}, fmt.Errorf("invalid image data URI payload")
	}
	if _, err := brandingImageType("."+brandingImageExtension(media), body); err != nil {
		return brandingImageData{}, err
	}
	return brandingImageData{body: body, typeName: media}, nil
}

func fetchCachedBrandingImage(ctx context.Context, rawURL string) (brandingImageData, error) {
	brandingImageMu.Lock()
	defer brandingImageMu.Unlock()
	configDir := config.ConfigDirectory()
	if configDir == "" {
		return brandingImageData{}, fmt.Errorf("configuration directory is unavailable")
	}
	digest := sha256.Sum256([]byte(rawURL))
	assetsDir := filepath.Join(configDir, "assets")
	if info, err := os.Lstat(assetsDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return brandingImageData{}, fmt.Errorf("asset directory is unavailable")
	}
	cacheDir := filepath.Join(configDir, "cache", "branding-images")
	cacheRoot := filepath.Dir(cacheDir)
	if err := os.Mkdir(cacheRoot, 0o700); err != nil && !os.IsExist(err) {
		return brandingImageData{}, err
	}
	if info, err := os.Lstat(cacheRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return brandingImageData{}, fmt.Errorf("image cache root is invalid")
	}
	if err := os.Mkdir(cacheDir, 0o700); err != nil && !os.IsExist(err) {
		return brandingImageData{}, err
	}
	if info, err := os.Lstat(cacheDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return brandingImageData{}, fmt.Errorf("image cache directory is invalid")
	}
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(digest[:]))
	if info, err := os.Lstat(cachePath); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() <= maxBrandingRemoteBytes && time.Since(info.ModTime()) < 24*time.Hour {
		body, readErr := os.ReadFile(cachePath)
		if readErr == nil {
			if typeName, typeErr := brandingImageType("", body); typeErr == nil {
				return brandingImageData{body: body, typeName: typeName}, nil
			}
		}
	}
	body, typeName, err := fetchRemoteBrandingImage(ctx, rawURL)
	if err != nil {
		if info, statErr := os.Lstat(cachePath); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			cached, readErr := os.ReadFile(cachePath)
			if readErr == nil {
				if oldType, typeErr := brandingImageType("", cached); typeErr == nil {
					return brandingImageData{body: cached, typeName: oldType}, nil
				}
			}
		}
		return brandingImageData{}, err
	}
	tmp, err := os.CreateTemp(cacheDir, ".branding-*")
	if err != nil {
		return brandingImageData{}, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		return brandingImageData{}, errors.Join(err, tmp.Close())
	}
	if _, err := tmp.Write(body); err != nil {
		return brandingImageData{}, errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return brandingImageData{}, errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return brandingImageData{}, err
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		return brandingImageData{}, err
	}
	return brandingImageData{body: body, typeName: typeName}, nil
}

func fetchRemoteBrandingImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validateBrandingRemoteURL(ctx, req.URL)
		},
		Transport: &http.Transport{DialContext: safeBrandingDialContext()},
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	if err := validateBrandingRemoteURL(ctx, parsed); err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "gitmessages/1.0")
	request.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp, image/x-icon")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image server returned %s", response.Status)
	}
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if !allowedRemoteBrandingImageType(media) {
		return nil, "", fmt.Errorf("unsupported image content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBrandingRemoteBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxBrandingRemoteBytes {
		return nil, "", fmt.Errorf("image response is invalid or too large")
	}
	if _, err := brandingImageType("."+brandingImageExtension(media), body); err != nil {
		return nil, "", err
	}
	return body, media, nil
}

func validateBrandingRemoteURL(ctx context.Context, parsed *url.URL) error {
	if parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("invalid remote image URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("internal image host is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("image host did not resolve")
	}
	for _, address := range addresses {
		ip := address.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("image host resolves to a non-public address")
		}
	}
	return nil
}

func safeBrandingDialContext() func(context.Context, string, string) (net.Conn, error) {
	return func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(dialCtx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			ip := candidate.IP
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				continue
			}
			connection, dialErr := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return connection, nil
			}
		}
		return nil, fmt.Errorf("no public image address could be reached")
	}
}

func allowedBrandingImageType(typeName string) bool {
	switch typeName {
	case "image/png", "image/jpeg", "image/webp", "image/x-icon", "image/svg+xml":
		return true
	default:
		return false
	}
}

func allowedRemoteBrandingImageType(typeName string) bool {
	switch typeName {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon":
		return true
	default:
		return false
	}
}

func brandingImageExtension(typeName string) string {
	switch typeName {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/x-icon":
		return "ico"
	case "image/svg+xml":
		return "svg"
	default:
		return ""
	}
}

func brandingImageType(extension string, body []byte) (string, error) {
	if strings.EqualFold(extension, ".svg") || extension == "" && strings.Contains(strings.ToLower(string(body)), "<svg") {
		text := strings.ToLower(string(body))
		if len(body) > 0 && len(body) <= maxBrandingRemoteBytes && strings.Contains(text, "<svg") && !strings.Contains(text, "<script") && !strings.Contains(text, "<foreignobject") {
			return "image/svg+xml", nil
		}
		return "", fmt.Errorf("invalid SVG image")
	}
	if len(body) >= 8 && string(body[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png", nil
	}
	if len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff {
		return "image/jpeg", nil
	}
	if len(body) >= 6 && (string(body[:6]) == "GIF87a" || string(body[:6]) == "GIF89a") {
		return "image/gif", nil
	}
	if len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP" {
		return "image/webp", nil
	}
	if len(body) >= 4 && body[0] == 0 && body[1] == 0 && body[2] == 1 && body[3] == 0 {
		return "image/x-icon", nil
	}
	return "", fmt.Errorf("unsupported or invalid image data")
}

const defaultIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#282a36"/><path fill="#bd93f9" d="M104 128h304v48H104zm0 104h240v48H104zm0 104h192v48H104z"/></svg>`
