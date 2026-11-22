package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestValidServer(t *testing.T) {
	for _, input := range []struct {
		value string
		valid bool
	}{
		{"https://api.example.com", true},
		{"http://127.0.0.1:8080/base", true},
		{"ftp://api.example.com", false},
		{"https://user:password@api.example.com", false},
		{"https://api.example.com?token=value", false},
		{"https://api.example.com#fragment", false},
		{"https://api.example.com path", false},
	} {
		if got := validServer(input.value); got != input.valid {
			t.Errorf("validServer(%q) = %t, want %t", input.value, got, input.valid)
		}
	}
}

func TestClientRunAndBuild(t *testing.T) {
	root := filepath.Join(os.TempDir(), "apimgr")
	configHome, err := os.MkdirTemp(root, "gitmessages-")
	if err != nil {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		configHome, err = os.MkdirTemp(root, "gitmessages-")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(configHome) })
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(configHome, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(configHome, "data"))
	t.Setenv("HOME", configHome)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept-Language") == "" {
			t.Errorf("request is missing CLI headers: %#v", r.Header)
		}
		if r.Method == http.MethodPost {
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode build request: %v", err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"data":{"message":"feat: add test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"types":[]}}`))
	}))
	defer server.Close()

	if err := run([]string{"--server", server.URL, "types"}); err != nil {
		t.Fatalf("run(types): %v", err)
	}
	if err := run([]string{"--server", server.URL, "build", "--type", "feat", "--description", "add test"}); err != nil {
		t.Fatalf("run(build): %v", err)
	}
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("run(--help): %v", err)
	}
	if err := run([]string{"--version"}); err != nil {
		t.Fatalf("run(--version): %v", err)
	}
	if err := run([]string{"--shell", "bash"}); err != nil {
		t.Fatalf("run(--shell bash): %v", err)
	}
	if err := run(nil); err == nil {
		t.Fatal("non-interactive invocation without a server unexpectedly succeeded")
	}
	if err := run([]string{"--server", "https://example.test", "unknown"}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown command error = %v", err)
	}
}

func TestClientRequestErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"message":"bad request"}`))
	}))
	defer server.Close()
	if _, err := request(server.URL, http.MethodGet, "/", nil, "en"); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("request error = %v", err)
	}
	if _, err := request("http://127.0.0.1:1", http.MethodGet, "/", nil, "en"); err == nil {
		t.Fatal("request accepted a refused connection")
	}
}

func TestTerminalModelNavigationAndBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"ok":true,"data":{"message":"feat(ui): add navigation"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"items":[]}}`))
	}))
	defer server.Close()
	model := terminalModel{server: server.URL, language: "en", config: &clientConfig{}}
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model.selection = 4
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(terminalModel)
	if !model.build || !strings.Contains(model.View(), "Commit type") {
		t.Fatalf("build screen did not start: %#v", model)
	}
	for _, input := range []string{"feat", "ui", "add navigation"} {
		for _, character := range input {
			updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
			model = updated.(terminalModel)
		}
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(terminalModel)
	}
	if model.build || model.output != "feat(ui): add navigation" {
		t.Fatalf("build result = %q, build active = %v", model.output, model.build)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(terminalModel)
	if !model.closed || command == nil {
		t.Fatal("Ctrl-C did not quit the menu")
	}
}

func TestTerminalSetupAndDisplaySelection(t *testing.T) {
	root := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	configHome, err := os.MkdirTemp(root, "gitmessages-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(configHome) })
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", configHome)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"data":{}}`))
	}))
	defer server.Close()
	config := &clientConfig{}
	model := terminalModel{setup: true, language: "en", config: config}
	for _, character := range server.URL {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(terminalModel)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(terminalModel)
	if model.setup || config.Server.Primary != server.URL {
		t.Fatalf("setup did not save server: %#v", model)
	}

	badConfig := &clientConfig{}
	badConfig.Display.Mode = "invalid"
	if err := launchInteractive("", "en", badConfig); err == nil {
		t.Fatal("invalid display mode was accepted")
	}
}

func TestSelectLanguage(t *testing.T) {
	t.Setenv("GITMESSAGES_LANG", "fr-CA")
	if got := selectLanguage("", "de"); got != "fr" {
		t.Fatalf("environment language priority = %q, want fr", got)
	}
	if got := selectLanguage("ja", "de"); got != "ja" {
		t.Fatalf("flag language priority = %q, want ja", got)
	}
	t.Setenv("GITMESSAGES_LANG", "")
	if got := selectLanguage("unsupported", "unsupported"); got != "en" {
		t.Fatalf("unsupported language fallback = %q, want en", got)
	}
}

func TestClientCompletionsAndTerminalRendering(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		previous := os.Stdout
		os.Stdout = write
		err = printClientCompletions(shell, "/usr/local/bin/gitmessages-cli")
		_ = write.Close()
		os.Stdout = previous
		output, readErr := io.ReadAll(read)
		_ = read.Close()
		if err != nil || readErr != nil || !strings.Contains(string(output), "gitmessages-cli") {
			t.Fatalf("%s completions failed: err=%v readErr=%v output=%q", shell, err, readErr, output)
		}
	}
	if err := printClientCompletions("unknown", "gitmessages-cli"); err == nil {
		t.Fatal("unsupported completion shell was accepted")
	}
	if err := printClientCompletions("bash", "unsafe name"); err == nil {
		t.Fatal("unsafe binary name was accepted for completions")
	}
	if got := trimLastRune("漢字"); got != "漢" {
		t.Fatalf("trimLastRune() = %q", got)
	}
	model := terminalModel{server: "https://messages.example.test", selection: 5}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	closed := updated.(terminalModel)
	if !closed.closed || command == nil || closed.View() != "" {
		t.Fatal("terminal exit action did not close the view")
	}
}
