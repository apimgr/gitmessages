package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/web"
)

var locales = []string{"ar", "de", "en", "es", "fr", "it", "ja", "zh"}

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, "generate OpenAPI documents:", err)
		os.Exit(1)
	}
}

func generate() (resultErr error) {
	projectTemp := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(projectTemp, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(projectTemp, 0o700); err != nil {
		return err
	}
	temporaryDirectory, err := os.MkdirTemp(projectTemp, "gitmessages-openapi-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(temporaryDirectory); cleanupErr != nil && resultErr == nil {
			resultErr = cleanupErr
		}
	}()
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(temporaryDirectory, "server.yml")
	configuration := []byte("server:\n  api_version: v1\n  metrics:\n    enabled: true\n    root:\n      enabled: true\n    auth:\n      allow_unauthenticated: false\n")
	if err := os.WriteFile(configPath, configuration, 0o600); err != nil {
		return err
	}
	if _, err := config.Load(configPath); err != nil {
		return err
	}
	outputDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, locale := range locales {
		request := httptest.NewRequest(http.MethodGet, "http://openapi.invalid/api/v1/server/swagger", nil)
		request.Header.Set("X-GitMessages-Language", locale)
		document := web.BuildOpenAPISuperset(request)
		content, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return err
		}
		content = append(content, '\n')
		path := filepath.Join(outputDirectory, "assets", "openapi-"+locale+".json")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
