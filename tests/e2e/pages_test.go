//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

func TestMain(m *testing.M) {
	if os.Getenv("E2E_COMPILE_ONLY") != "1" {
		if err := waitForServer(e2eServerURL() + "/api/v1/server/healthz"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func e2eServerURL() string {
	host := strings.TrimSpace(os.Getenv("E2E_SERVER_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := strings.TrimSpace(os.Getenv("E2E_SERVER_PORT"))
	return "http://" + net.JoinHostPort(host, port)
}

func waitForServer(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
				lastErr = fmt.Errorf("server health endpoint returned %s", response.Status)
			} else {
				lastErr = requestErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready: %v: %w", lastErr, ctx.Err())
		case <-ticker.C:
		}
	}
}

func newBrowserContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	var allocator context.Context
	var cancelAllocator context.CancelFunc
	if endpoint := strings.TrimSpace(os.Getenv("CHROME_CDP_ENDPOINT")); endpoint != "" {
		if err := waitForChrome(ctx, endpoint); err != nil {
			t.Fatalf("wait for Chromium sidecar: %v", err)
		}
		allocator, cancelAllocator = chromedp.NewRemoteAllocator(ctx, endpoint)
	} else {
		options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.Headless)
		allocator, cancelAllocator = chromedp.NewExecAllocator(ctx, options...)
	}
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	var consoleErrors []string
	var consoleMu sync.Mutex
	chromedp.ListenTarget(browser, func(event any) {
		var message string
		switch event := event.(type) {
		case *cdpruntime.EventExceptionThrown:
			message = event.ExceptionDetails.Text
			if event.ExceptionDetails.Exception != nil && event.ExceptionDetails.Exception.Description != "" {
				message += ": " + event.ExceptionDetails.Exception.Description
			}
		case *cdpruntime.EventConsoleAPICalled:
			if string(event.Type) == "error" && len(event.Args) > 0 {
				message = event.Args[0].Description
			}
		}
		if message != "" {
			consoleMu.Lock()
			consoleErrors = append(consoleErrors, message)
			consoleMu.Unlock()
		}
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		directory := strings.TrimSpace(os.Getenv("E2E_ARTIFACT_DIR"))
		if directory == "" {
			return
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Errorf("create browser artifact directory: %v", err)
			return
		}
		name := artifactFilename.ReplaceAllString(t.Name(), "_")
		var screenshot []byte
		if err := chromedp.Run(browser, chromedp.CaptureScreenshot(&screenshot)); err == nil && len(screenshot) > 0 {
			if err := os.WriteFile(directory+"/"+name+".png", screenshot, 0o600); err != nil {
				t.Errorf("save failure screenshot: %v", err)
			}
		}
		var pageHTML string
		if err := chromedp.Run(browser, chromedp.OuterHTML("html", &pageHTML)); err == nil && pageHTML != "" {
			pageHTML = csrfHTMLValue.ReplaceAllString(pageHTML, `${1}[redacted]`)
			pageHTML = bearerTokenValue.ReplaceAllString(pageHTML, "[redacted]")
			if err := os.WriteFile(directory+"/"+name+".html", []byte(pageHTML), 0o600); err != nil {
				t.Errorf("save failure page HTML: %v", err)
			}
		}
	})
	t.Cleanup(func() {
		consoleMu.Lock()
		defer consoleMu.Unlock()
		if len(consoleErrors) > 0 {
			t.Errorf("browser console reported JavaScript errors: %s", strings.Join(consoleErrors, "; "))
		}
	})
	return browser
}

var (
	artifactFilename = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	csrfHTMLValue    = regexp.MustCompile(`(?i)(name="csrf_token"\s+value=")[^"]*`)
	bearerTokenValue = regexp.MustCompile(`\b(?:adm|own|mtr)_[A-Za-z0-9_-]{16,}`)
)

type browserStep struct {
	name   string
	action chromedp.Action
}

func runBrowserSteps(t *testing.T, browser context.Context, steps ...browserStep) {
	t.Helper()
	for _, step := range steps {
		if err := chromedp.Run(browser, step.action); err != nil {
			t.Fatalf("browser step %s: %v", step.name, err)
		}
	}
}

func waitForChrome(ctx context.Context, endpoint string) error {
	if !strings.HasPrefix(endpoint, "ws://") {
		return fmt.Errorf("unsupported Chromium endpoint %q", endpoint)
	}
	probeURL := "http://" + strings.TrimSuffix(strings.TrimPrefix(endpoint, "ws://"), "/") + "/json/version"
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
				lastErr = fmt.Errorf("Chromium devtools endpoint returned %s", response.Status)
			} else {
				lastErr = requestErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", lastErr, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestSSRPagesContainRenderedContent(t *testing.T) {
	baseURL := e2eServerURL()
	for _, route := range []string{
		"/", "/types", "/templates", "/builder",
		"/server/about", "/server/help", "/server/help/mcp", "/server/healthz",
		"/server/privacy", "/server/terms", "/server/security", "/server/security/policy",
		"/server/security/thanks", "/server/contact", "/server/dpo", "/server/preferences",
		"/server/preferences/sync", "/server/preferences/tokens",
		"/server/docs/swagger", "/server/docs/graphql",
	} {
		t.Run(strings.Trim(route, "/"), func(t *testing.T) {
			response, err := http.Get(baseURL + route)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d", route, response.StatusCode)
			}
			var page strings.Builder
			if _, err := io.Copy(&page, response.Body); err != nil {
				t.Fatal(err)
			}
			body := page.String()
			if !strings.Contains(body, "<title>") || !strings.Contains(body, "<main") || !strings.Contains(body, `lang="en"`) || !strings.Contains(body, `name="viewport"`) {
				t.Fatalf("GET %s did not return a complete server-rendered page", route)
			}
			if route == "/server/docs/swagger" && (!strings.Contains(body, `id="swagger-ui"`) || !strings.Contains(body, `src="/static/js/docs-swagger.js?`)) {
				t.Fatal("Swagger page omitted its local interactive explorer")
			}
			if route == "/server/docs/graphql" && (!strings.Contains(body, `id="graphiql"`) || !strings.Contains(body, `src="/static/js/docs-graphql.js?`) || !strings.Contains(body, `name="query"`)) {
				t.Fatal("GraphiQL page omitted its interactive explorer or no-JavaScript form")
			}
			if route == "/server/help" && strings.Count(body, `href="/server/help/mcp"`) != 1 {
				t.Fatal("help page must link to MCP help exactly once through its More help hub")
			}
		})
	}
}

func TestSwaggerExplorerListsPublicRead(t *testing.T) {
	browser := newBrowserContext(t)
	var listed bool
	runBrowserSteps(t, browser,
		browserStep{name: "open REST explorer", action: chromedp.Navigate(e2eServerURL() + "/server/docs/swagger")},
		browserStep{name: "wait for API operations", action: chromedp.WaitVisible(`.opblock-summary-path`, chromedp.ByQuery)},
		browserStep{name: "verify type catalog operation", action: chromedp.Evaluate(`[...document.querySelectorAll('.opblock-summary-path')].some(item=>item.textContent.trim()==='/api/v1/types')`, &listed)},
	)
	if !listed {
		t.Fatal("Swagger UI did not list the public commit-type endpoint")
	}
}

func TestInternalLinkCrawlHasNoDeadLinksOrServerErrors(t *testing.T) {
	baseURL, err := url.Parse(e2eServerURL())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	queue := []string{baseURL.String() + "/"}
	visited := make(map[string]struct{})
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, ok := visited[current]; ok {
			continue
		}
		visited[current] = struct{}{}
		response, err := client.Get(current)
		if err != nil {
			t.Fatalf("GET %s: %v", current, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", current, readErr)
		}
		if response.StatusCode >= http.StatusBadRequest {
			t.Fatalf("internal link %s returned %d", current, response.StatusCode)
		}
		contentType := response.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/css") {
			for _, match := range cssAssetReference.FindAllSubmatch(body, -1) {
				reference, parseErr := url.Parse(strings.TrimSpace(string(match[1])))
				if parseErr != nil {
					continue
				}
				resolved := baseURL.ResolveReference(reference)
				if resolved.Scheme == baseURL.Scheme && resolved.Host == baseURL.Host {
					resolved.Fragment = ""
					queue = append(queue, resolved.String())
				}
			}
			continue
		}
		if !strings.Contains(contentType, "text/html") {
			continue
		}
		document, err := html.Parse(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("parse %s: %v", current, err)
		}
		queue = append(queue, internalReferences(document, baseURL)...)
	}
	if len(visited) < 10 {
		t.Fatalf("internal crawl visited only %d URLs", len(visited))
	}
}

var cssAssetReference = regexp.MustCompile(`url\(\s*["']?([^"')\s]+)`)

func internalReferences(root *html.Node, base *url.URL) []string {
	var references []string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			method := "get"
			if node.Data == "form" {
				for _, attribute := range node.Attr {
					if attribute.Key == "method" {
						method = strings.ToLower(attribute.Val)
					}
				}
			}
			for _, attribute := range node.Attr {
				if attribute.Key != "href" && attribute.Key != "src" && !(node.Data == "form" && attribute.Key == "action" && method == "get") {
					continue
				}
				reference, err := url.Parse(strings.TrimSpace(attribute.Val))
				if err != nil {
					continue
				}
				resolved := base.ResolveReference(reference)
				if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
					continue
				}
				resolved.Fragment = ""
				references = append(references, resolved.String())
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return references
}

func TestSSRReferenceCatalogAndBuilderContent(t *testing.T) {
	baseURL := e2eServerURL()
	for _, test := range []struct {
		route string
		want  []string
	}{
		{route: "/types", want: []string{"feat", "fix", "docs"}},
		{route: "/templates", want: []string{"feat({scope})", "fix({scope})"}},
		{route: "/builder", want: []string{`name="type"`, `name="description"`, `action="/builder"`}},
	} {
		response, err := http.Get(baseURL + test.route)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s returned %d", test.route, response.StatusCode)
		}
		for _, want := range test.want {
			if !strings.Contains(string(body), want) {
				t.Errorf("GET %s omitted catalog or builder content %q", test.route, want)
			}
		}
	}
}

func TestRESTAndGraphQLReturnTheCuratedCatalog(t *testing.T) {
	response, err := http.Get(e2eServerURL() + "/api/v1/types")
	if err != nil {
		t.Fatal(err)
	}
	restBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(restBody, []byte(`"feat"`)) || !bytes.Contains(restBody, []byte(`"fix"`)) {
		t.Fatalf("REST catalog returned status %d or omitted known commit types", response.StatusCode)
	}
	graphqlRequest, err := http.NewRequest(http.MethodPost, e2eServerURL()+"/api/graphql", strings.NewReader(`{"query":"{ types { type } }"}`))
	if err != nil {
		t.Fatal(err)
	}
	graphqlRequest.Header.Set("Content-Type", "application/json")
	graphqlResponse, err := http.DefaultClient.Do(graphqlRequest)
	if err != nil {
		t.Fatal(err)
	}
	graphqlBody, err := io.ReadAll(graphqlResponse.Body)
	graphqlResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if graphqlResponse.StatusCode != http.StatusOK || bytes.Contains(graphqlBody, []byte(`"errors"`)) || !bytes.Contains(graphqlBody, []byte(`"feat"`)) {
		t.Fatalf("GraphQL catalog returned status %d or omitted known commit types", graphqlResponse.StatusCode)
	}
}

func TestNoJavaScriptBuilderSubmitsMessage(t *testing.T) {
	response, err := http.PostForm(e2eServerURL()+"/builder", url.Values{"type": {"feat"}, "scope": {"browser"}, "description": {"build message"}, "convention": {"conventional-commits"}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "feat(browser): build message") {
		t.Fatalf("server-side builder returned %d without the formatted message", response.StatusCode)
	}
}

func TestNoJavaScriptGraphQLFormSubmitsQuery(t *testing.T) {
	browser := newBrowserContext(t)
	var body, location string
	runBrowserSteps(t, browser,
		browserStep{name: "disable JavaScript", action: emulation.SetScriptExecutionDisabled(true)},
		browserStep{name: "load GraphQL documentation", action: chromedp.Navigate(e2eServerURL() + "/server/docs/graphql")},
		browserStep{name: "decline optional cookies", action: chromedp.Click(`#cookie-consent .btn-decline`)},
		browserStep{name: "wait for consent redirect", action: chromedp.Sleep(time.Second)},
		browserStep{name: "clear GraphQL query", action: chromedp.Clear(`#query`)},
		browserStep{name: "set GraphQL query", action: chromedp.SendKeys(`#query`, "{ types { type } }")},
		browserStep{name: "submit GraphQL query", action: chromedp.Click(`#graphql-nojs-heading + form button[type="submit"]`)},
		browserStep{name: "wait for GraphQL response navigation", action: chromedp.Sleep(time.Second)},
		browserStep{name: "read GraphQL response URL", action: chromedp.Location(&location)},
		browserStep{name: "read GraphQL response", action: chromedp.Text("body", &body, chromedp.ByQuery)},
	)
	if !strings.Contains(location, "/api/v1/server/graphql") {
		t.Fatalf("no-JavaScript GraphQL form navigated to %q instead of its response page", location)
	}
	if !strings.Contains(body, `"types"`) || !strings.Contains(body, `"feat"`) {
		t.Fatalf("no-JavaScript GraphQL form did not return the catalog: %s", body)
	}
}

func TestGraphiQLBrowserRenders(t *testing.T) {
	browser := newBrowserContext(t)
	var rendered bool
	runBrowserSteps(t, browser,
		browserStep{name: "open GraphiQL documentation", action: chromedp.Navigate(e2eServerURL() + "/server/docs/graphql")},
		browserStep{name: "wait for GraphiQL application", action: chromedp.WaitVisible(`#graphiql .graphiql-container`, chromedp.ByQuery)},
		browserStep{name: "verify GraphiQL shell", action: chromedp.Evaluate(`!!document.querySelector('#graphiql .graphiql-container')`, &rendered)},
	)
	if !rendered {
		t.Fatal("GraphiQL application did not render")
	}
}

func TestBrowserWithoutJavaScript(t *testing.T) {
	baseURL := e2eServerURL()
	browser := newBrowserContext(t)
	var body string
	runBrowserSteps(t, browser,
		browserStep{name: "disable JavaScript", action: emulation.SetScriptExecutionDisabled(true)},
		browserStep{name: "load builder", action: chromedp.Navigate(baseURL + "/builder")},
		browserStep{name: "set scope", action: chromedp.SendKeys(`#scope`, "nojs")},
		browserStep{name: "set description", action: chromedp.SendKeys(`#description`, "build message")},
		browserStep{name: "submit builder", action: chromedp.SendKeys(`#description`, "\r")},
		browserStep{name: "wait for result", action: chromedp.WaitVisible("pre", chromedp.ByQuery)},
		browserStep{name: "read result", action: chromedp.Text("main", &body, chromedp.ByQuery)},
	)
	if !strings.Contains(body, "feat(nojs): build message") {
		t.Fatalf("builder form did not render the message with JavaScript disabled; page text: %q", body)
	}
}

func TestBrowserWithJavaScript(t *testing.T) {
	baseURL := e2eServerURL()
	browser := newBrowserContext(t)
	var title, body string
	runBrowserSteps(t, browser,
		browserStep{name: "load builder", action: chromedp.Navigate(baseURL + "/builder")},
		browserStep{name: "read page title", action: chromedp.Title(&title)},
		browserStep{name: "set scope", action: chromedp.SendKeys(`#scope`, "browser")},
		browserStep{name: "set description", action: chromedp.SendKeys(`#description`, "build message")},
		browserStep{name: "submit builder", action: chromedp.SendKeys(`#description`, "\r")},
		browserStep{name: "wait for result", action: chromedp.WaitVisible("pre", chromedp.ByQuery)},
		browserStep{name: "read result", action: chromedp.Text("main", &body, chromedp.ByQuery)},
	)
	if title == "" || !strings.Contains(body, "feat(browser): build message") {
		t.Fatalf("browser-driven message builder did not render the formatted message; title=%q page text=%q", title, body)
	}
}

func TestBrowserThemeLanguageAndMobileNavigation(t *testing.T) {
	baseURL := e2eServerURL()
	browser := newBrowserContext(t)
	if err := chromedp.Run(browser, emulation.SetDeviceMetricsOverride(375, 812, 1, false)); err != nil {
		t.Fatal(err)
	}
	backgrounds := make(map[string]string)
	for _, theme := range []string{"dark", "light", "auto"} {
		if err := chromedp.Run(browser, network.SetCookie("theme", theme).WithURL(baseURL)); err != nil {
			t.Fatalf("set %s theme cookie: %v", theme, err)
		}
		var actualTheme, background string
		if err := chromedp.Run(browser,
			chromedp.Navigate(baseURL+"/"),
			chromedp.Evaluate(`document.documentElement.dataset.theme`, &actualTheme),
			chromedp.Evaluate(`getComputedStyle(document.body).backgroundColor`, &background),
		); err != nil {
			t.Fatalf("render %s theme: %v", theme, err)
		}
		if actualTheme != theme || background == "" {
			t.Fatalf("theme %s rendered as %q with background %q", theme, actualTheme, background)
		}
		backgrounds[theme] = background
	}
	if backgrounds["dark"] == backgrounds["light"] {
		t.Fatalf("dark and light computed backgrounds are identical: %q", backgrounds["dark"])
	}
	var scrollWidth, viewportWidth int
	var navVisible bool
	if err := chromedp.Run(browser, chromedp.Evaluate(`(()=>{const toggle=document.querySelector('label[for="nav-toggle"]');if(!toggle||getComputedStyle(toggle).display==='none')return false;toggle.click();return getComputedStyle(document.querySelector('.nav-panel')).visibility==='visible'})()`, &navVisible)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(browser, chromedp.Evaluate(`document.documentElement.scrollWidth`, &scrollWidth), chromedp.Evaluate(`window.innerWidth`, &viewportWidth)); err != nil {
		t.Fatal(err)
	}
	if scrollWidth > viewportWidth {
		t.Fatalf("mobile page has horizontal overflow: scroll width %d, viewport %d", scrollWidth, viewportWidth)
	}
	if !navVisible {
		t.Fatal("mobile navigation did not open when its control was activated")
	}

	response, err := http.Get(baseURL + "/server/about?lang=es")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `lang="es"`) || !strings.Contains(string(body), "Acerca de") {
		t.Fatalf("Spanish preference did not change rendered page text (status %d)", response.StatusCode)
	}
}

func TestUnknownRouteReturnsThemedNotFoundPage(t *testing.T) {
	response, err := http.Get(e2eServerURL() + "/missing-e2e-page")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "theme-dark") || !strings.Contains(string(body), "404") || !strings.Contains(string(body), "<main") {
		t.Fatalf("unknown route returned %d without the themed 404 page", response.StatusCode)
	}
	request, err := http.NewRequest(http.MethodGet, e2eServerURL()+"/api/v1/missing-e2e-route", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var apiError struct {
		Error string `json:"error"`
	}
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Header.Get("Content-Type"), "application/json") || json.Unmarshal(body, &apiError) != nil || apiError.Error != "NOT_FOUND" {
		t.Fatalf("unknown API route returned %d without a structured JSON error", response.StatusCode)
	}
}

func TestPreferenceFormIncludesAndEnforcesCSRF(t *testing.T) {
	response, err := http.Get(e2eServerURL() + "/server/preferences")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`name="csrf_token"`)) {
		t.Fatal("preferences page did not render its CSRF form token")
	}
	form := url.Values{"theme": {"light"}, "redirect": {"/"}}
	request, err := http.NewRequest(http.MethodPost, e2eServerURL()+"/server/preferences", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte(`class="theme-dark"`)) || !bytes.Contains(body, []byte(`<main`)) {
		t.Fatalf("preference update without a CSRF token returned %d without a themed 403 page", response.StatusCode)
	}
}

func TestOwnerTokenLifecycleAndMCPAuthorization(t *testing.T) {
	baseURL := e2eServerURL()
	requestMCP := func(token string) (int, []byte) {
		t.Helper()
		payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
		request, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2026-07-28")
		request.Header.Set("Mcp-Method", "tools/list")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	requestJSON := func(method, path, token string, payload any) (int, []byte) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(encoded)
		}
		request, err := http.NewRequest(method, baseURL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Accept", "application/json")
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	var primary struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	status, body := requestJSON(http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", nil)
	if status != http.StatusCreated || json.Unmarshal(body, &primary) != nil || primary.Data.OwnerToken == "" {
		t.Fatalf("primary token creation returned status %d", status)
	}
	ownerToken := primary.Data.OwnerToken
	if status, _ = requestJSON(http.MethodPost, "/api/v1/server/preferences/tokens/primary", ownerToken, nil); status != http.StatusConflict {
		t.Fatalf("primary creation with an existing valid token returned %d, want 409", status)
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", ownerToken, nil); status != http.StatusOK {
		t.Fatalf("primary token could not list its tokens: %d", status)
	}
	var linked struct {
		Data struct {
			Token struct {
				ID string `json:"id"`
			} `json:"token"`
			Value string `json:"linked_token"`
		} `json:"data"`
	}
	status, body = requestJSON(http.MethodPost, "/api/v1/server/preferences/tokens", ownerToken, map[string]string{"name": "e2e linked", "scope": "read", "expires": "7d"})
	if status != http.StatusCreated || json.Unmarshal(body, &linked) != nil || linked.Data.Token.ID == "" || linked.Data.Value == "" {
		t.Fatalf("linked token creation returned status %d", status)
	}
	linkedToken := linked.Data.Value
	linkedPath := "/api/v1/server/preferences/tokens/" + linked.Data.Token.ID
	if status, _ = requestJSON(http.MethodPatch, linkedPath, ownerToken, map[string]string{"name": "e2e renamed", "expires": "never"}); status != http.StatusOK {
		t.Fatalf("linked token update returned %d", status)
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", linkedToken, nil); status != http.StatusForbidden {
		t.Fatalf("linked token management returned %d, want 403", status)
	}
	if status, _ = requestMCP(""); status != http.StatusUnauthorized {
		t.Fatalf("MCP without a token returned %d, want 401", status)
	}
	var rotatedPrimary struct {
		Data struct {
			Token string `json:"owner_token"`
		} `json:"data"`
	}
	status, body = requestJSON(http.MethodPost, "/api/v1/server/preferences/tokens/primary/rotate", ownerToken, nil)
	if status != http.StatusOK || json.Unmarshal(body, &rotatedPrimary) != nil || rotatedPrimary.Data.Token == "" {
		t.Fatalf("primary token rotation returned status %d", status)
	}
	ownerToken = rotatedPrimary.Data.Token
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", primary.Data.OwnerToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("rotated primary token remained valid: %d", status)
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", ownerToken, nil); status != http.StatusOK {
		t.Fatalf("rotated primary token returned %d", status)
	}
	if status, body = requestJSON(http.MethodPost, "/api/v1/server/preferences/tokens/"+linked.Data.Token.ID+"/rotate", ownerToken, nil); status != http.StatusOK {
		t.Fatalf("linked token rotation returned %d", status)
	}
	var rotatedLinked struct {
		Data struct {
			Value string `json:"linked_token"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &rotatedLinked) != nil || rotatedLinked.Data.Value == "" {
		t.Fatal("linked token rotation returned no replacement")
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", linkedToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("rotated linked token remained valid: %d", status)
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", rotatedLinked.Data.Value, nil); status != http.StatusForbidden {
		t.Fatalf("replacement linked token did not retain restricted scope: %d", status)
	}
	if status, _ = requestJSON(http.MethodDelete, linkedPath, ownerToken, nil); status != http.StatusOK {
		t.Fatalf("linked token revocation returned %d", status)
	}
	if status, _ = requestJSON(http.MethodGet, "/api/v1/server/preferences/tokens", rotatedLinked.Data.Value, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked linked token remained valid: %d", status)
	}
	status, body = requestMCP(ownerToken)
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"tools"`)) {
		t.Fatalf("authorized MCP tools/list returned %d", status)
	}
	request, err := http.NewRequest(http.MethodGet, baseURL+"/server/preferences/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "gitmessages_owner_token_7gT2qA", Value: ownerToken})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	pageBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(pageBody, []byte("tokens")) || bytes.Contains(pageBody, []byte(ownerToken)) {
		t.Fatalf("owner-token page returned %d or exposed the raw token", response.StatusCode)
	}
}
