package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
)

const (
	sitemapNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"
	atomNamespace    = "http://www.w3.org/2005/Atom"
)

type sitemapDocument struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Location  string `xml:"loc"`
	Lastmod   string `xml:"lastmod,omitempty"`
	Frequency string `xml:"changefreq"`
	Priority  string `xml:"priority"`
}

type atomDocument struct {
	XMLName xml.Name    `xml:"feed"`
	XMLNS   string      `xml:"xmlns,attr"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Link    atomLink    `xml:"link"`
	Entries []atomEntry `xml:"entry,omitempty"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Summary string   `xml:"summary,omitempty"`
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	LastBuild   string    `xml:"lastBuildDate"`
	Items       []rssItem `xml:"item,omitempty"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description,omitempty"`
}

type publicSitemapPage struct {
	path      string
	frequency string
	priority  string
}

var publicSitemapPages = []publicSitemapPage{
	{path: "/", frequency: "daily", priority: "1.0"},
	{path: "/types", frequency: "weekly", priority: "0.8"},
	{path: "/templates", frequency: "weekly", priority: "0.8"},
	{path: "/builder", frequency: "weekly", priority: "0.8"},
	{path: "/server/about", frequency: "weekly", priority: "0.8"},
	{path: "/server/help", frequency: "weekly", priority: "0.8"},
	{path: "/server/help/mcp", frequency: "weekly", priority: "0.8"},
	{path: "/server/privacy", frequency: "weekly", priority: "0.8"},
	{path: "/server/terms", frequency: "weekly", priority: "0.8"},
	{path: "/server/security", frequency: "weekly", priority: "0.8"},
	{path: "/server/security/policy", frequency: "weekly", priority: "0.8"},
	{path: "/server/security/thanks", frequency: "weekly", priority: "0.8"},
	{path: "/server/dpo", frequency: "weekly", priority: "0.8"},
	{path: "/server/docs/swagger", frequency: "weekly", priority: "0.7"},
	{path: "/server/docs/graphql", frequency: "weekly", priority: "0.7"},
}

var (
	seoMetaNamePattern   = regexp.MustCompile(`^[a-z0-9:_.-]+$`)
	seoCustomMetaPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)
	seoVerifyPatterns    = map[string]*regexp.Regexp{
		"google":    regexp.MustCompile(`^[A-Za-z0-9_-]{1,43}$`),
		"bing":      regexp.MustCompile(`^[A-F0-9]{1,32}$`),
		"yandex":    regexp.MustCompile(`^[a-f0-9]{1,32}$`),
		"baidu":     regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`),
		"pinterest": regexp.MustCompile(`^[a-f0-9]{1,32}$`),
		"facebook":  regexp.MustCompile(`^[a-z0-9]{1,64}$`),
	}
)

func seoDescription(r *http.Request) string {
	configuration := config.Get()
	for _, value := range []string{configuration.Server.Branding.Description, configuration.Server.Branding.Tagline, i18n.T(languageCode(r), "about.body")} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return paths.ProjectName
}

func seoBrandingTitle() string {
	if title := strings.TrimSpace(config.Get().Server.Branding.Title); title != "" {
		return title
	}
	return paths.ProjectName
}

func seoPageTitle(title string) string {
	configuration := config.Get()
	title = strings.TrimSpace(title)
	if title == "" {
		title = seoBrandingTitle()
	}
	tagline := strings.TrimSpace(configuration.Server.Branding.Tagline)
	if tagline == "" {
		tagline = seoBrandingTitle()
	}
	if title == tagline {
		return title
	}
	separator := configuration.Server.SEO.TitleSeparator
	if len(separator) > 8 {
		separator = " - "
	}
	if separator == "" {
		separator = " - "
	}
	return title + separator + tagline
}

func seoCanonicalURL(r *http.Request) string {
	path := r.URL.Path
	canonical := strings.TrimSpace(config.Get().Server.SEO.Canonical)
	if canonical == "" {
		return BuildURL(r, path)
	}
	base, err := url.Parse(canonical)
	if err != nil || !strings.EqualFold(base.Scheme, "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return BuildURL(r, path)
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	return base.String()
}

func seoHeadMarkup(r *http.Request, title string) string {
	configuration := config.Get()
	seo := configuration.Server.SEO
	brand := strings.TrimSpace(configuration.Server.Branding.Title)
	if brand == "" {
		brand = paths.ProjectName
	}
	description := seoDescription(r)
	pageTitle := seoPageTitle(title)
	canonical := seoCanonicalURL(r)
	currentURL := BuildURL(r, r.URL.Path)
	var markup strings.Builder
	if len(seo.Keywords) > 0 {
		fmt.Fprintf(&markup, "    <meta name=\"keywords\" content=\"%s\">\n", html.EscapeString(strings.Join(seo.Keywords, ", ")))
	}
	if value := strings.TrimSpace(seo.Author); value != "" {
		fmt.Fprintf(&markup, "    <meta name=\"author\" content=\"%s\">\n", html.EscapeString(value))
	}
	fmt.Fprintf(&markup, "    <link rel=\"canonical\" href=\"%s\">\n", html.EscapeString(canonical))
	metaKeys := make([]string, 0, len(seo.MetaTags))
	for key := range seo.MetaTags {
		metaKeys = append(metaKeys, key)
	}
	sort.Strings(metaKeys)
	emittedMetaTags := 0
	for _, key := range metaKeys {
		if emittedMetaTags >= 20 {
			break
		}
		value := strings.TrimSpace(seo.MetaTags[key])
		if !seoMetaNamePattern.MatchString(key) || value == "" || strings.Contains(" description keywords author robots viewport ", " "+key+" ") {
			continue
		}
		fmt.Fprintf(&markup, "    <meta name=\"%s\" content=\"%s\">\n", html.EscapeString(key), html.EscapeString(value))
		emittedMetaTags++
	}
	if seo.OpenGraph.Enabled {
		ogType := strings.TrimSpace(seo.OpenGraph.Type)
		if ogType == "" {
			ogType = "website"
		}
		siteName := strings.TrimSpace(seo.OpenGraph.SiteName)
		if siteName == "" {
			siteName = brand
		}
		fmt.Fprintf(&markup, "    <meta property=\"og:title\" content=\"%s\">\n    <meta property=\"og:description\" content=\"%s\">\n    <meta property=\"og:type\" content=\"%s\">\n    <meta property=\"og:url\" content=\"%s\">\n    <meta property=\"og:site_name\" content=\"%s\">\n", html.EscapeString(pageTitle), html.EscapeString(description), html.EscapeString(ogType), html.EscapeString(canonical), html.EscapeString(siteName))
		if image := brandingPublicImageURL(r, "og-image", seo.OGImage); image != "" {
			fmt.Fprintf(&markup, "    <meta property=\"og:image\" content=\"%s\">\n", html.EscapeString(image))
		}
	}
	if seo.Twitter.Enabled {
		card := strings.TrimSpace(seo.Twitter.Card)
		if card != "summary" && card != "summary_large_image" {
			card = "summary_large_image"
		}
		fmt.Fprintf(&markup, "    <meta name=\"twitter:card\" content=\"%s\">\n    <meta name=\"twitter:title\" content=\"%s\">\n    <meta name=\"twitter:description\" content=\"%s\">\n", card, html.EscapeString(pageTitle), html.EscapeString(description))
		if image := brandingPublicImageURL(r, "og-image", seo.OGImage); image != "" {
			fmt.Fprintf(&markup, "    <meta name=\"twitter:image\" content=\"%s\">\n", html.EscapeString(image))
		}
		for _, handle := range []struct{ name, value string }{{"twitter:site", seo.TwitterHandle}, {"twitter:creator", seo.Twitter.Creator}} {
			if value := strings.TrimSpace(handle.value); value != "" {
				fmt.Fprintf(&markup, "    <meta name=\"%s\" content=\"%s\">\n", handle.name, html.EscapeString(value))
			}
		}
	}
	writeVerificationMeta(&markup, seo.Verification)
	structured, err := json.Marshal(map[string]string{"@context": "https://schema.org", "@type": "WebSite", "name": brand, "description": description, "url": currentURL})
	if err == nil {
		fmt.Fprintf(&markup, "    <script type=\"application/ld+json\">%s</script>\n", structured)
	}
	return markup.String()
}

func writeVerificationMeta(markup *strings.Builder, verification config.SEOVerificationConfig) {
	providers := []struct{ key, name, content string }{
		{"google", "google-site-verification", verification.Google},
		{"bing", "msvalidate.01", verification.Bing},
		{"yandex", "yandex-verification", verification.Yandex},
		{"baidu", "baidu-site-verification", verification.Baidu},
		{"pinterest", "p:domain_verify", verification.Pinterest},
		{"facebook", "fb:domain_verification", verification.Facebook},
	}
	for _, provider := range providers {
		value := strings.TrimSpace(provider.content)
		if value != "" && seoVerifyPatterns[provider.key].MatchString(value) {
			if provider.key == "facebook" {
				fmt.Fprintf(markup, "    <meta property=\"%s\" content=\"%s\">\n", provider.name, html.EscapeString(value))
			} else {
				fmt.Fprintf(markup, "    <meta name=\"%s\" content=\"%s\">\n", provider.name, html.EscapeString(value))
			}
		}
	}
	for _, custom := range verification.Custom {
		name, property, content := strings.TrimSpace(custom.Name), strings.TrimSpace(custom.Property), strings.TrimSpace(custom.Content)
		if content == "" || len(content) > 256 || (name == "") == (property == "") || name != "" && !seoCustomMetaPattern.MatchString(name) || property != "" && !seoCustomMetaPattern.MatchString(property) {
			continue
		}
		if property != "" {
			fmt.Fprintf(markup, "    <meta property=\"%s\" content=\"%s\">\n", html.EscapeString(property), html.EscapeString(content))
		} else {
			fmt.Fprintf(markup, "    <meta name=\"%s\" content=\"%s\">\n", html.EscapeString(name), html.EscapeString(content))
		}
	}
}

func seoRobotsDirective(path string) string {
	if config.Get().Server.SEO.NoIndex {
		return "noindex,nofollow"
	}
	switch path {
	case "/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/help/mcp",
		"/server/privacy", "/server/terms", "/server/security", "/server/security/policy",
		"/server/security/thanks", "/server/dpo", "/server/docs/swagger", "/server/docs/graphql":
		return "index,follow"
	default:
		return "noindex,nofollow"
	}
}

func robotsTxt(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	configuration := config.Get()
	allow, deny := normalizedRobotsRules(configuration.Web.Robots)
	var body strings.Builder
	body.WriteString("User-agent: *\n")
	for _, rule := range allow {
		fmt.Fprintf(&body, "Allow: %s\n", rule)
	}
	for _, rule := range deny {
		fmt.Fprintf(&body, "Disallow: %s\n", rule)
	}
	fmt.Fprintf(&body, "Sitemap: %s\n", BuildURL(r, "/sitemap.xml"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body.String()))
	}
}

func sitemapXML(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	configuration := config.Get()
	allow, deny := normalizedRobotsRules(configuration.Web.Robots)
	pages := make([]sitemapURL, 0, len(publicSitemapPages))
	lastmod := sitemapLastModified()
	maxURLs := configuration.Server.SEO.Sitemap.MaxURLs
	if maxURLs <= 0 || maxURLs > 50000 {
		maxURLs = 50000
	}
	for _, page := range publicSitemapPages {
		if !robotsAllowsPath(page.path, allow, deny) {
			continue
		}
		entry := sitemapURL{Location: BuildURL(r, page.path), Frequency: page.frequency, Priority: page.priority}
		if lastmod != "" {
			entry.Lastmod = lastmod
		}
		pages = append(pages, entry)
		if len(pages) >= maxURLs {
			break
		}
	}
	body, err := marshalXML(sitemapDocument{XMLNS: sitemapNamespace, URLs: pages})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeCachedXML(w, r, "application/xml; charset=utf-8", body, false, time.Now().UTC())
}

func atomFeedXML(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	updated := feedSnapshotTime()
	feedURL := BuildURL(r, "/feed.xml")
	title := feedTitle(config.Get())
	document := atomDocument{
		XMLNS: atomNamespace, ID: BuildURL(r, "/"), Title: cleanXMLText(title),
		Updated: updated.Format(time.RFC3339), Link: atomLink{Rel: "self", Href: feedURL},
		Entries: []atomEntry{},
	}
	body, err := marshalXML(document)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeCachedXML(w, r, "application/atom+xml; charset=utf-8", body, true, updated)
}

func rssFeedXML(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	updated := feedSnapshotTime()
	document := rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title: cleanXMLText(feedTitle(config.Get())), Link: BuildURL(r, "/"),
			Description: cleanXMLText(feedTitle(config.Get())), LastBuild: updated.Format(time.RFC1123Z),
			Items: []rssItem{},
		},
	}
	body, err := marshalXML(document)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeCachedXML(w, r, "application/rss+xml; charset=utf-8", body, true, updated)
}

func allowReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, tr(r, "api.method_not_allowed"), http.StatusMethodNotAllowed)
	return false
}

func normalizedRobotsRules(configuration config.WebRobotsConfig) ([]string, []string) {
	allow := normalizeRobotsRules(configuration.Allow)
	deny := normalizeRobotsRules(configuration.Deny)
	if len(allow) == 0 {
		allow = []string{"/"}
	}
	return allow, deny
}

func normalizeRobotsRules(rules []string) []string {
	result := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if !strings.HasPrefix(rule, "/") || strings.ContainsAny(rule, "\r\n\x00") {
			continue
		}
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		result = append(result, rule)
	}
	return result
}

func robotsAllowsPath(path string, allow, deny []string) bool {
	for _, rule := range deny {
		if strings.HasPrefix(path, rule) {
			return false
		}
	}
	for _, rule := range allow {
		if strings.HasPrefix(path, rule) {
			return true
		}
	}
	return false
}

func feedTitle(configuration *config.Config) string {
	if title := strings.TrimSpace(configuration.Server.SEO.Feed.Title); title != "" {
		return title
	}
	if title := strings.TrimSpace(configuration.Server.Branding.Title); title != "" {
		return title
	}
	return paths.ProjectName
}

func sitemapLastModified() string {
	if serverBuildDate == "" {
		return ""
	}
	date, err := time.Parse(time.RFC3339, serverBuildDate)
	if err != nil {
		return ""
	}
	return date.UTC().Format("2006-01-02")
}

func feedSnapshotTime() time.Time {
	return time.Now().UTC().Truncate(5 * time.Minute)
}

func marshalXML(value any) ([]byte, error) {
	body, err := xml.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

func writeCachedXML(w http.ResponseWriter, r *http.Request, contentType string, body []byte, cache bool, snapshot time.Time) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	lastModified := snapshot.Format(http.TimeFormat)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", lastModified)
	if cache {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, candidate := range strings.Split(match, ",") {
			if strings.TrimSpace(candidate) == etag || strings.TrimSpace(candidate) == "*" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	} else if modifiedSince := r.Header.Get("If-Modified-Since"); modifiedSince != "" {
		if since, err := time.Parse(http.TimeFormat, modifiedSince); err == nil && !snapshot.After(since) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func cleanXMLText(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for _, character := range value {
		if !xmlCharacterAllowed(character) {
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

func xmlCharacterAllowed(character rune) bool {
	return character == 0x9 || character == 0xa || character == 0xd ||
		character >= 0x20 && character <= 0xd7ff || character >= 0xe000 && character <= 0xfffd ||
		character >= 0x10000 && character <= 0x10ffff && utf8.ValidRune(character)
}
