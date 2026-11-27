package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	htmlnode "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const maxOperatorBodyBytes = 256 * 1024

var (
	operatorWarnings sync.Map
	paragraphBreaks  = regexp.MustCompile(`\n\s*\n+`)
)

// operatorHTML resolves, renders and sanitizes one operator-provided branding body.
func operatorHTML(r *http.Request, name string) template.HTML {
	entry := operatorEntry(config.Get().Server.Branding.Content, name)
	return renderOperatorContent(r, name, entry)
}

func renderMarkdown(source string) template.HTML {
	var output bytes.Buffer
	if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert([]byte(source), &output); err != nil {
		return template.HTML(html.EscapeString(source))
	}
	return template.HTML(operatorPolicy("help").Sanitize(output.String()))
}

func renderOperatorContent(r *http.Request, name string, entry *config.OperatorContent) template.HTML {
	if entry == nil || entry.Body == nil {
		return ""
	}
	body, ok := resolveOperatorBody(entry.Body, r.Header.Get("X-GitMessages-Language"))
	key := "server.branding.content." + name
	if !ok {
		warnOperatorContent(key)
		return ""
	}
	if len([]byte(body)) > maxOperatorBodyBytes {
		warnOperatorContent(key)
		return ""
	}
	if strings.TrimSpace(body) == "" {
		return ""
	}
	format := strings.TrimSpace(entry.Format)
	if format == "" {
		format = "md"
	}
	if (name == "header" || name == "footer") && format != "html" {
		warnOperatorContent(key)
		return ""
	}
	body = substituteOperatorVariables(body, format == "html")
	var rendered string
	switch format {
	case "md":
		var output bytes.Buffer
		if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert([]byte(body), &output); err != nil {
			warnOperatorContent(key)
			return ""
		}
		rendered = output.String()
	case "html":
		rendered = body
	case "txt":
		if name == "header" || name == "footer" {
			warnOperatorContent(key)
			return ""
		}
		paragraphs := paragraphBreaks.Split(strings.TrimSpace(body), -1)
		for _, paragraph := range paragraphs {
			if strings.TrimSpace(paragraph) != "" {
				rendered += `<p>` + html.EscapeString(strings.TrimSpace(paragraph)) + `</p>`
			}
		}
	default:
		warnOperatorContent(key)
		return ""
	}
	policy := operatorPolicy(name)
	sanitized := policy.Sanitize(rendered)
	sanitized = normalizeOperatorLinks(sanitized, r.URL.Path, requestUsesTorAddress(r))
	if strings.TrimSpace(sanitized) == "" && strings.TrimSpace(body) != "" {
		warnOperatorContent(key)
		return ""
	}
	return template.HTML(sanitized)
}

func operatorSource(r *http.Request, name string) (string, string, bool) {
	entry := operatorEntry(config.Get().Server.Branding.Content, name)
	if entry == nil || entry.Body == nil {
		return "", "", false
	}
	body, ok := resolveOperatorBody(entry.Body, r.Header.Get("X-GitMessages-Language"))
	key := "server.branding.content." + name
	if !ok || len([]byte(body)) > maxOperatorBodyBytes {
		warnOperatorContent(key)
		return "", "", false
	}
	format := strings.TrimSpace(entry.Format)
	if format == "" {
		format = "md"
	}
	switch format {
	case "md":
		format = "markdown"
	case "html":
	case "txt":
		format = "text"
	default:
		warnOperatorContent(key)
		return "", "", false
	}
	if strings.TrimSpace(body) == "" {
		return "", "", false
	}
	return substituteOperatorVariables(body, false), format, true
}

func operatorEntry(content config.BrandingContentConfig, name string) *config.OperatorContent {
	switch name {
	case "header":
		return &content.Header
	case "about":
		return &content.About
	case "privacy":
		return &content.Privacy
	case "terms":
		return &content.Terms
	case "footer":
		return &content.Footer
	default:
		return nil
	}
}

func resolveOperatorBody(value any, language string) (string, bool) {
	if text, ok := value.(string); ok {
		return text, true
	}
	values := make(map[string]any)
	switch typed := value.(type) {
	case map[string]any:
		values = typed
	case map[any]any:
		for key, item := range typed {
			if textKey, ok := key.(string); ok {
				values[textKey] = item
			}
		}
	default:
		return "", false
	}
	for _, key := range []string{language, "default"} {
		if text, ok := values[key].(string); ok {
			return text, true
		}
	}
	return "", false
}

func operatorPolicy(name string) *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "span", "div")
	policy.AllowElements("strong", "b", "em", "i", "u", "s", "small")
	policy.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	policy.AllowElements("ul", "ol", "li")
	policy.AllowAttrs("href", "title", "target", "rel").OnElements("a")
	policy.RequireNoReferrerOnLinks(true)
	policy.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	policy.AllowURLSchemes("http", "https", "data")
	policy.AllowRelativeURLs(true)
	policy.AllowAttrs("class", "id").Globally()
	if name != "header" && name != "footer" {
		policy.AllowElements("table", "thead", "tbody", "tr", "th", "td", "pre", "code", "blockquote", "hr", "dl", "dt", "dd", "sup", "sub")
	}
	return policy
}

func normalizeOperatorLinks(fragment, pagePath string, torRequest bool) string {
	root, err := htmlnode.ParseFragment(strings.NewReader(fragment), &htmlnode.Node{Type: htmlnode.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return fragment
	}
	for _, node := range root {
		normalizeOperatorNode(node, pagePath, torRequest)
	}
	var output bytes.Buffer
	for _, node := range root {
		if err := htmlnode.Render(&output, node); err != nil {
			return fragment
		}
	}
	return output.String()
}

func operatorCard(r *http.Request, name, id string) template.HTML {
	content := operatorHTML(r, name)
	if content == "" {
		return ""
	}
	content = normalizeOperatorHeadings(string(content))
	return template.HTML(`<section class="section-card" aria-labelledby="` + id + `"><h2 id="` + id + `">` + html.EscapeString(tr(r, "branding.additional_information")) + `</h2>` + string(content) + `</section>`)
}

func normalizeOperatorHeadings(fragment string) template.HTML {
	context := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "div", DataAtom: atom.Div}
	root, err := htmlnode.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return template.HTML(fragment)
	}
	var output bytes.Buffer
	for _, node := range root {
		convertOperatorHeadings(node)
		if err := htmlnode.Render(&output, node); err != nil {
			return template.HTML(fragment)
		}
	}
	return template.HTML(output.String())
}

func convertOperatorHeadings(node *htmlnode.Node) {
	if node.Type == htmlnode.ElementNode && (node.Data == "h1" || node.Data == "h2") {
		node.Data = "h3"
		node.DataAtom = atom.H3
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		convertOperatorHeadings(child)
	}
}

func normalizeOperatorNode(node *htmlnode.Node, pagePath string, torRequest bool) {
	if node.Type == htmlnode.ElementNode && node.Data == "a" {
		var href string
		for index := 0; index < len(node.Attr); index++ {
			if node.Attr[index].Key == "href" {
				href = node.Attr[index].Val
				parsed, err := url.Parse(href)
				if err != nil {
					continue
				}
				base := &url.URL{Scheme: "https", Host: "branding.invalid", Path: pagePath}
				resolved := base.ResolveReference(parsed)
				decodedPath, decodeErr := url.PathUnescape(resolved.EscapedPath())
				if decodeErr == nil && strings.TrimSuffix(decodedPath, "/") == "/server/contact" {
					node.Attr = append(node.Attr[:index], node.Attr[index+1:]...)
					index--
				}
			}
		}
		parsed, err := url.Parse(href)
		if torRequest && err == nil && isClearnetLink(parsed) {
			node.Data = "span"
			node.DataAtom = atom.Span
			node.Attr = nil
		} else if err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "") {
			rel := "noopener noreferrer nofollow ugc"
			found := false
			for index := range node.Attr {
				if node.Attr[index].Key == "rel" {
					node.Attr[index].Val = rel
					found = true
				}
			}
			if !found {
				node.Attr = append(node.Attr, htmlnode.Attribute{Key: "rel", Val: rel})
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		normalizeOperatorNode(child, pagePath, torRequest)
	}
}

func requestUsesTorAddress(r *http.Request) bool {
	if r == nil {
		return false
	}
	configured := config.Get().Server.Tor.OnionAddress
	if configured == "" {
		return false
	}
	_, host, _ := GetURLVars(r)
	return strings.EqualFold(host, configured)
}

func isClearnetLink(target *url.URL) bool {
	if target == nil {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return target.Scheme == "mailto"
	}
	if target.Host == "" {
		return false
	}
	configured := config.Get().Server.Tor.OnionAddress
	return configured == "" || !strings.EqualFold(target.Hostname(), configured)
}

func substituteOperatorVariables(value string, escapeValues bool) string {
	variables := map[string]string{
		"{current_year}":    fmt.Sprint(time.Now().Year()),
		"{project_name}":    paths.ProjectName,
		"{project_org}":     paths.OrgName,
		"{project_version}": serverVersion,
		"{build_datetime}":  operatorBuildDate(),
		"{onion_address}":   operatorOverlayAddress("tor"),
		"{i2p_address}":     operatorOverlayAddress("i2p"),
	}
	for placeholder, replacement := range variables {
		if escapeValues {
			replacement = html.EscapeString(replacement)
		}
		value = strings.ReplaceAll(value, placeholder, replacement)
	}
	return value
}

func operatorBuildDate() string {
	if serverBuildDate == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02T15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, serverBuildDate); err == nil {
			return parsed.Format("January 02, 2006 at 15:04:05 MST")
		}
	}
	return ""
}

func operatorOverlayAddress(name string) string {
	statuses := overlayStatuses()
	if statuses == nil {
		return ""
	}
	encoded, err := json.Marshal(statuses[name])
	if err != nil {
		return ""
	}
	var status struct {
		Status   string `json:"status"`
		Hostname string `json:"hostname"`
		Address  string `json:"address"`
	}
	if json.Unmarshal(encoded, &status) != nil || status.Status != "healthy" {
		return ""
	}
	if status.Hostname != "" {
		return status.Hostname
	}
	return status.Address
}

func warnOperatorContent(key string) {
	if _, loaded := operatorWarnings.LoadOrStore(key, struct{}{}); !loaded {
		fmt.Fprintf(os.Stderr, "warning: ignoring unusable %s content\n", key)
	}
}
