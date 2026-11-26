//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"gopkg.in/yaml.v3"
)

func TestDebugErrorUsesThemedRendererAndRequiresOperatorToken(t *testing.T) {
	configDir := os.Getenv("E2E_CONFIG_DIR")
	if configDir == "" {
		t.Fatal("E2E_CONFIG_DIR is not configured")
	}
	secretData, err := os.ReadFile(filepath.Join(configDir, "secrets.yml"))
	if err != nil {
		t.Fatalf("read runtime secrets: %v", err)
	}
	var secrets struct {
		Server struct {
			Token string `yaml:"token"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(secretData, &secrets); err != nil {
		t.Fatalf("parse runtime secrets: %v", err)
	}
	if secrets.Server.Token == "" {
		t.Fatal("runtime operator token is missing")
	}

	browser := newBrowserContext(t)
	if err := chromedp.Run(browser, chromedp.Navigate(e2eServerURL()+"/")); err != nil {
		t.Fatalf("open application before debug request: %v", err)
	}
	var result struct {
		Unauthorized int  `json:"unauthorized"`
		Status       int  `json:"status"`
		Themed       bool `json:"themed"`
		Main         bool `json:"main"`
	}
	expression, err := json.Marshal(secrets.Server.Token)
	if err != nil {
		t.Fatal("encode runtime operator token")
	}
	script := `(async () => {
  const unauthorized = await fetch('/debug/error500', {headers:{Accept:'text/html'}});
  const response = await fetch('/debug/error500', {headers:{Accept:'text/html',Authorization:'Bearer '+` + string(expression) + `}});
  const body = await response.text();
  return {unauthorized:unauthorized.status,status:response.status,themed:body.includes('class="theme-'),main:body.includes('<main')};
})()`
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := chromedp.Run(browser, chromedp.Evaluate(script, &result, awaitPromise)); err != nil {
		t.Fatalf("request debug error page in browser: %v", err)
	}
	if result.Unauthorized != 401 {
		t.Fatalf("unauthenticated debug request returned %d", result.Unauthorized)
	}
	if result.Status != 500 || !result.Themed || !result.Main {
		t.Fatalf("authenticated debug error page status=%d themed=%t main=%t", result.Status, result.Themed, result.Main)
	}
}
