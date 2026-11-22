//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/web"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(web.Handler("e2e"))
	t.Cleanup(server.Close)
	return server
}

func TestSSRPagesContainRenderedContent(t *testing.T) {
	server := startServer(t)
	for _, route := range []string{"/", "/types", "/templates", "/builder", "/server/about", "/server/help", "/server/help/mcp", "/server/privacy", "/server/terms", "/server/security", "/server/preferences"} {
		t.Run(strings.Trim(route, "/"), func(t *testing.T) {
			response, err := http.Get(server.URL + route)
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
			if !strings.Contains(body, "<title>") || !strings.Contains(body, "<main") {
				t.Fatalf("GET %s did not return a complete server-rendered page", route)
			}
		})
	}
}

func TestBrowserWithoutJavaScript(t *testing.T) {
	server := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.Headless)
	allocator, cancelAllocator := chromedp.NewExecAllocator(ctx, options...)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()
	var html string
	if err := chromedp.Run(browser,
		emulation.SetScriptExecutionDisabled(true),
		chromedp.Navigate(server.URL+"/builder"),
		chromedp.OuterHTML("html", &html),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<main") || !strings.Contains(html, "<form") {
		t.Fatal("builder page did not render its content and native form without JavaScript")
	}
}

func TestBrowserWithJavaScript(t *testing.T) {
	server := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.Headless)
	allocator, cancelAllocator := chromedp.NewExecAllocator(ctx, options...)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()
	var title string
	if err := chromedp.Run(browser,
		chromedp.Navigate(server.URL+"/"),
		chromedp.Title(&title),
	); err != nil {
		t.Fatal(err)
	}
	if title == "" {
		t.Fatal("home page has no browser title")
	}
}
