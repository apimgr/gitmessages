package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func TestRenderOperatorContentFormatsAndLanguages(t *testing.T) {
	request := httptest.NewRequest("GET", "/server/about", nil)
	request.Header.Set("X-GitMessages-Language", "fr")
	localized := renderOperatorContent(request, "about", &config.OperatorContent{
		Format: "md",
		Body:   map[string]any{"default": "Default paragraph", "fr": "Texte **français**"},
	})
	if !strings.Contains(string(localized), "Texte <strong>français</strong>") || strings.Contains(string(localized), "Default paragraph") {
		t.Fatalf("language-specific Markdown was not rendered: %s", localized)
	}

	plain := renderOperatorContent(request, "about", &config.OperatorContent{Format: "txt", Body: "<script>alert(1)</script>\n\nSecond paragraph"})
	if strings.Contains(string(plain), "<script>") || !strings.Contains(string(plain), "&lt;script&gt;") || strings.Count(string(plain), "<p>") != 2 {
		t.Fatalf("plain text was not escaped and split into paragraphs: %s", plain)
	}
}

func TestRenderMarkdownSanitizesDescriptionHTML(t *testing.T) {
	rendered := renderMarkdown("**Safe** <script>alert(1)</script>")
	if !strings.Contains(string(rendered), "<strong>Safe</strong>") || strings.Contains(string(rendered), "<script") || !strings.Contains(string(rendered), "alert(1)") {
		t.Fatalf("Markdown description was not rendered and sanitized: %s", rendered)
	}
}

func TestRenderOperatorContentSanitizesHTMLAndLinks(t *testing.T) {
	request := httptest.NewRequest("GET", "/server/about", nil)
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  branding:\n    content:\n      about:\n        format: html\n        body: '<h2>Extra</h2><p>Operator information.</p>'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defaultPath := filepath.Join(projectTestDir(t), "server.yml")
		if err := os.WriteFile(defaultPath, []byte("server: {}\n"), 0o600); err != nil {
			t.Errorf("write default test config: %v", err)
			return
		}
		if _, err := config.Load(defaultPath); err != nil {
			t.Errorf("restore default test config: %v", err)
		}
	})
	content := renderOperatorContent(request, "about", &config.OperatorContent{
		Format: "html",
		Body:   `<h2>Extra</h2><script>alert(1)</script><p onclick="alert(1)">Safe <a href="https://example.test/" target="_blank">link</a> <a href="/server/contact?type=privacy">report</a></p><form><input value="x"></form>`,
	})
	got := string(content)
	for _, forbidden := range []string{"<script", "onclick=", "<form", "<input", `href="/server/contact`} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sanitized HTML retained %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, `rel="noopener noreferrer nofollow ugc"`) || !strings.Contains(got, "Safe") || !strings.Contains(got, "report") {
		t.Errorf("safe links or visible text were lost: %s", got)
	}
	card := operatorCard(request, "about", "operator-about")
	if !strings.Contains(string(card), `<h2 id="operator-about">`) || !strings.Contains(string(card), "<h3>Extra</h3>") {
		t.Errorf("operator card did not preserve the page heading hierarchy: %s", card)
	}
}

func TestTorOperatorContentRemovesClearnetLinks(t *testing.T) {
	current := config.Get()
	previous := current.Server.Tor.OnionAddress
	current.Server.Tor.OnionAddress = strings.Repeat("a", 56) + ".onion"
	t.Cleanup(func() { current.Server.Tor.OnionAddress = previous })
	request := httptest.NewRequest("GET", "http://"+current.Server.Tor.OnionAddress+"/server/privacy", nil)
	content := renderOperatorContent(request, "privacy", &config.OperatorContent{
		Format: "md",
		Body:   "[external](https://example.com/privacy) and [onion](http://" + current.Server.Tor.OnionAddress + "/privacy) and [email](mailto:privacy@example.com)",
	})
	got := string(content)
	if strings.Contains(got, `href="https://example.com/privacy"`) || strings.Contains(got, `href="mailto:privacy@example.com"`) {
		t.Fatalf("Tor privacy page retained a clearnet link: %s", got)
	}
	if !strings.Contains(got, "<span>external</span>") || !strings.Contains(got, "email") || !strings.Contains(got, `href="http://`+current.Server.Tor.OnionAddress+`/privacy"`) {
		t.Fatalf("Tor privacy page did not preserve plain labels and the onion link: %s", got)
	}
}

func TestRenderOperatorContentRejectsInvalidEntries(t *testing.T) {
	request := httptest.NewRequest("GET", "/server/about", nil)
	if got := renderOperatorContent(request, "about", &config.OperatorContent{Format: "unknown", Body: "content"}); got != "" {
		t.Errorf("unknown format produced content: %s", got)
	}
	if got := renderOperatorContent(request, "about", &config.OperatorContent{Format: "md", Body: strings.Repeat("x", maxOperatorBodyBytes+1)}); got != "" {
		t.Errorf("oversized body produced content")
	}
	if got := renderOperatorContent(request, "footer", &config.OperatorContent{Format: "md", Body: "content"}); got != "" {
		t.Errorf("non-HTML footer produced content: %s", got)
	}
}

func TestBrandingImageDataURILimitsAndTypes(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	valid := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	imageData, err := decodeBrandingDataURI(valid)
	if err != nil || imageData.typeName != "image/png" || string(imageData.body) != string(png) {
		t.Fatalf("valid PNG data URI decoded as %#v, %v", imageData, err)
	}
	for _, value := range []string{
		"data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("GIF89a")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, maxBrandingDataBytes)),
	} {
		if _, err := decodeBrandingDataURI(value); err == nil {
			t.Errorf("invalid data URI was accepted: prefix=%q", value[:min(len(value), 48)])
		}
	}
}

func TestBrandingImageLoadsOnlyNamedPrivateAssets(t *testing.T) {
	configDir := projectTestDir(t)
	assetsDir := filepath.Join(configDir, "assets")
	if err := os.Mkdir(assetsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n")
	if err := os.WriteFile(filepath.Join(assetsDir, "brand.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "server.yml")
	if err := os.WriteFile(configPath, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load image test configuration: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})
	imageData, err := loadBrandingImage(context.Background(), "brand.png")
	if err != nil || imageData.typeName != "image/png" || string(imageData.body) != string(png) {
		t.Fatalf("local image resolved as %#v, %v", imageData, err)
	}
	for _, value := range []string{"../brand.png", "sub/brand.png", "missing.png", "brand.exe"} {
		if _, err := loadBrandingImage(context.Background(), value); err == nil {
			t.Errorf("invalid local image name %q was accepted", value)
		}
	}
}

func TestBrandingImageRejectsPrivateRemoteAddresses(t *testing.T) {
	parsed, err := url.Parse("https://127.0.0.1/logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBrandingRemoteURL(context.Background(), parsed); err == nil {
		t.Fatal("private remote image address was accepted")
	}
}

func TestBrandingImageScalingPreservesOrCropsAsSpecified(t *testing.T) {
	configPath := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(configPath, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("load scaling test configuration: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("restore default config: %v", err)
		}
	})
	source := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x * 50), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		width, height int
		cover         bool
	}{{20, 0, false}, {10, 10, true}, {32, 32, false}} {
		result, err := cachedScaledBrandingPNG(encoded.Bytes(), test.width, test.height, test.cover)
		if err != nil {
			t.Fatalf("scale %dx%d cover=%t: %v", test.width, test.height, test.cover, err)
		}
		bounds, err := png.DecodeConfig(bytes.NewReader(result))
		if err != nil {
			t.Fatalf("decode scaled PNG: %v", err)
		}
		wantWidth, wantHeight := test.width, test.height
		if wantHeight == 0 {
			wantHeight = 10
		}
		if bounds.Width != wantWidth || bounds.Height != wantHeight {
			t.Errorf("scaled image size = %dx%d, want %dx%d", bounds.Width, bounds.Height, wantWidth, wantHeight)
		}
	}
	if _, err := cachedScaledBrandingPNG(encoded.Bytes(), 1201, 1, false); err == nil {
		t.Fatal("oversized output dimension was accepted")
	}
}
