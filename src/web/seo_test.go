package web

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func loadSEOTestConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(projectTestDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := config.Load(path)
	if err != nil {
		t.Fatalf("load SEO test configuration: %v", err)
	}
	t.Cleanup(func() {
		resetPath := filepath.Join(projectTestDir(t), "server.yml")
		if err := os.WriteFile(resetPath, []byte("server: {}\n"), 0o600); err != nil {
			t.Errorf("write reset configuration: %v", err)
			return
		}
		if _, err := config.Load(resetPath); err != nil {
			t.Errorf("reset SEO test configuration: %v", err)
		}
	})
	return configuration
}

func TestSEOEndpointsExposeOnlyPublicPages(t *testing.T) {
	handler := Handler("test-version")
	request := webTestRequest(http.MethodGet, "/sitemap.xml", nil)
	request.Host = "messages.example.test"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("sitemap status = %d: %s", response.Code, response.Body.String())
	}
	var sitemap struct {
		URLs []struct {
			Location string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(response.Body.Bytes(), &sitemap); err != nil {
		t.Fatalf("decode sitemap: %v", err)
	}
	seen := make(map[string]bool, len(sitemap.URLs))
	for _, entry := range sitemap.URLs {
		seen[entry.Location] = true
		if strings.Contains(entry.Location, "/api/") || strings.Contains(entry.Location, "/feed.xml") || strings.Contains(entry.Location, "/rss.xml") || strings.Contains(entry.Location, "/server/contact") {
			t.Errorf("sitemap contains a forbidden URL: %s", entry.Location)
		}
	}
	if !seen[BuildURL(request, "/")] || !seen[BuildURL(request, "/server/docs/swagger")] {
		t.Fatalf("sitemap omitted a required public page: %#v", seen)
	}
}

func TestSEOFeedsAreValidEmptyAndCacheable(t *testing.T) {
	handler := Handler("test-version")
	for _, test := range []struct {
		path        string
		contentType string
	}{{"/feed.xml", "application/atom+xml; charset=utf-8"}, {"/rss.xml", "application/rss+xml; charset=utf-8"}} {
		request := webTestRequest(http.MethodGet, test.path, nil)
		request.Host = "messages.example.test"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != test.contentType {
			t.Fatalf("GET %s returned %d and content type %q", test.path, response.Code, response.Header().Get("Content-Type"))
		}
		decoder := xml.NewDecoder(strings.NewReader(response.Body.String()))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Errorf("GET %s returned invalid XML: %v", test.path, err)
				break
			}
		}
		if !strings.Contains(response.Body.String(), "messages.example.test") || !strings.Contains(response.Header().Get("Cache-Control"), "max-age=300") {
			t.Errorf("GET %s omitted request origin or cache policy", test.path)
		}
		if strings.Contains(response.Body.String(), "<entry>") || strings.Contains(response.Body.String(), "<item>") {
			t.Errorf("GET %s fabricated public catalog item history", test.path)
		}
		if response.Header().Get("ETag") == "" || response.Header().Get("Last-Modified") == "" {
			t.Errorf("GET %s omitted cache validators", test.path)
		}
		conditional := webTestRequest(http.MethodGet, test.path, nil)
		conditional.Host = "messages.example.test"
		conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		handler.ServeHTTP(cached, conditional)
		if cached.Code != http.StatusNotModified {
			t.Errorf("conditional GET %s returned %d, want 304", test.path, cached.Code)
		}
	}
}

func TestSEOEndpointsAllowOnlyGetAndHead(t *testing.T) {
	handler := Handler("test-version")
	for _, path := range []string{"/robots.txt", "/sitemap.xml", "/feed.xml", "/rss.xml"} {
		head := httptest.NewRecorder()
		handler.ServeHTTP(head, webTestRequest(http.MethodHead, path, nil))
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("HEAD %s returned %d with %d body bytes", path, head.Code, head.Body.Len())
		}
		post := httptest.NewRecorder()
		handler.ServeHTTP(post, webTestRequest(http.MethodPost, path, nil))
		if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("POST %s returned %d with Allow %q", path, post.Code, post.Header().Get("Allow"))
		}
	}
}

func TestRobotsDirectiveIndexesOnlyPublicPages(t *testing.T) {
	handler := Handler("test-version")
	for _, test := range []struct {
		path string
		want string
	}{{"/", "index,follow"}, {"/server/about", "index,follow"}, {"/server/healthz", "noindex,nofollow"}, {"/server/contact", "noindex,nofollow"}, {APIBasePath() + "/types", "noindex,nofollow"}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, webTestRequest(http.MethodGet, test.path, nil))
		if response.Code < http.StatusBadRequest && strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") && !strings.Contains(response.Body.String(), `<meta name="robots" content="`+test.want+`">`) {
			t.Errorf("GET %s has no robots meta directive %q", test.path, test.want)
		}
		if got := response.Header().Get("X-Robots-Tag"); (test.want == "noindex,nofollow") != (got == "noindex, nofollow") {
			t.Errorf("GET %s X-Robots-Tag = %q for directive %q", test.path, got, test.want)
		}
	}
}

func TestSEOHeadEscapesAndValidatesConfiguredMetadata(t *testing.T) {
	current := loadSEOTestConfig(t)
	current.Server.Branding = config.DefaultConfig().Server.Branding
	current.Server.Branding.Title = `Messages <&>`
	current.Server.Branding.Tagline = `A curated reference`
	current.Server.Branding.Description = `Curated <commit> references & examples.`
	current.Server.SEO = config.DefaultConfig().Server.SEO
	current.Server.SEO.Author = `Team "Git"`
	current.Server.SEO.Keywords = []string{"commit", "conventions"}
	current.Server.SEO.Canonical = "https://canonical.example/base"
	current.Server.SEO.MetaTags = map[string]string{"custom.name": `value "quoted"`, "description": "must be ignored", "bad name": "must be ignored"}
	current.Server.SEO.Verification.Google = "valid_code-123"
	current.Server.SEO.Verification.Facebook = "UPPERCASE-is-invalid"
	current.Server.SEO.Verification.Custom = []config.SEOCustomMetaConfig{
		{Name: "norton-safeweb-site-verification", Content: "valid"},
		{Name: "bad name", Content: "invalid"},
		{Property: "fb:app_id", Content: "12345"},
		{Name: "valid", Property: "also-valid", Content: "ambiguous"},
	}
	request := webTestRequest(http.MethodGet, "/server/about", nil)
	request.Host = "request.example.test"
	markup := seoHeadMarkup(request, "About")
	for _, expected := range []string{
		`<link rel="canonical" href="https://canonical.example/base/server/about">`,
		`<meta property="og:title" content="About - A curated reference">`,
		`content="Messages &lt;&amp;&gt;"`,
		`content="Curated &lt;commit&gt; references &amp; examples."`,
		`"url":"http://request.example.test/server/about"`,
		`name="google-site-verification" content="valid_code-123"`,
		`name="norton-safeweb-site-verification" content="valid"`,
		`property="fb:app_id" content="12345"`,
	} {
		if !strings.Contains(markup, expected) {
			t.Errorf("SEO markup omitted %q: %s", expected, markup)
		}
	}
	if !strings.Contains(seoPageTitle("About"), "About - A curated reference") {
		t.Errorf("page title does not use configured tagline: %q", seoPageTitle("About"))
	}
	for _, forbidden := range []string{"must be ignored", "UPPERCASE-is-invalid", `name="bad name"`, `name="valid" property="also-valid"`} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("SEO markup included invalid configured data %q", forbidden)
		}
	}
}

func TestNoIndexConfigurationMarksEveryRoute(t *testing.T) {
	current := loadSEOTestConfig(t)
	current.Server.SEO.NoIndex = true
	handler := Handler("test-version")
	for _, path := range []string{"/", "/api/healthz", "/feed.xml"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, webTestRequest(http.MethodGet, path, nil))
		if response.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Errorf("GET %s X-Robots-Tag = %q", path, response.Header().Get("X-Robots-Tag"))
		}
		if path == "/" && !strings.Contains(response.Body.String(), `<meta name="robots" content="noindex,nofollow">`) {
			t.Error("private instance homepage omitted noindex metadata")
		}
	}
}
