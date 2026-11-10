package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/messages"
	"gopkg.in/yaml.v3"
)

// Build information embedded via -ldflags; BuildDate is derived from BuildEpoch
var (
	Version    = "dev"
	CommitID   = "unknown"
	BuildEpoch = "0"
)

// clientBuildDate derives the RFC 3339 UTC build date from the embedded BuildEpoch; N/A when unset or invalid
func clientBuildDate() string {
	seconds, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil || seconds <= 0 {
		return "N/A"
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// debugEnabled prints each API request to stderr when --debug is set
var debugEnabled bool

const projectName = "gitmessages"

type clientConfig struct {
	Language string `yaml:"language"`
	Server   struct {
		Primary string `yaml:"primary"`
	} `yaml:"server"`
}

type envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	binary := filepath.Base(os.Args[0])
	flags := flag.NewFlagSet(binary, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serverFlag := flags.String("server", "", "Server base URL")
	languageFlag := flags.String("lang", "", "Language for output")
	jsonOutput := flags.Bool("json", false, "Print the API response as JSON")
	versionFlag := flags.Bool("version", false, "Print version")
	flags.BoolVar(versionFlag, "v", false, "Print version")
	helpFlag := flags.Bool("help", false, "Print help")
	flags.BoolVar(helpFlag, "h", false, "Print help")
	debugFlag := flags.Bool("debug", false, "Print API requests to stderr")
	colorFlag := flags.String("color", "auto", "Color output: auto, yes, no")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", binary, err)
	}
	debugEnabled = *debugFlag
	switch *colorFlag {
	case "no":
		_ = os.Setenv("NO_COLOR", "1")
	case "yes":
		_ = os.Unsetenv("NO_COLOR")
	}
	if *versionFlag {
		fmt.Printf("%s %s (%s) built %s\n", binary, Version, CommitID, clientBuildDate())
		return nil
	}

	config, err := loadConfig()
	if err != nil {
		return err
	}
	lang := selectLanguage(*languageFlag, config.Language)
	if *helpFlag || flags.NArg() == 0 {
		printUsage(binary, lang)
		return nil
	}

	server := strings.TrimSpace(*serverFlag)
	if server == "" {
		server = strings.TrimSpace(os.Getenv("GITMESSAGES_SERVER_PRIMARY"))
	}
	if server == "" {
		server = strings.TrimSpace(config.Server.Primary)
	}
	if !validServer(server) {
		return fmt.Errorf("%s", i18n.T(lang, "cli.server_required"))
	}
	if *serverFlag != "" && config.Server.Primary == "" {
		config.Server.Primary = server
		if err := saveConfig(config); err != nil {
			return err
		}
	}

	command := flags.Arg(0)
	if command == "build" {
		return runBuild(server, flags.Args()[1:], *jsonOutput, lang)
	}
	paths := map[string]string{
		"types": "/api/v1/types", "list": "/api/v1/types", "conventions": "/api/v1/conventions",
		"templates": "/api/v1/templates", "random": "/api/v1/messages/random",
	}
	path, ok := paths[command]
	if !ok {
		return fmt.Errorf("%s %q; use %s --help", i18n.T(lang, "cli.unknown_command"), command, binary)
	}
	data, err := request(server, http.MethodGet, path, nil, lang)
	if err != nil {
		return err
	}
	if *jsonOutput {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	if command == "random" {
		var result struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return err
		}
		fmt.Println(result.Message)
		return nil
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, data, "", "  "); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, formatted.String())
	return err
}

func runBuild(server string, args []string, jsonOutput bool, lang string) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("type", "", "Commit type")
	scope := flags.String("scope", "", "Optional scope")
	description := flags.String("description", "", "Commit description")
	body := flags.String("body", "", "Optional commit body")
	convention := flags.String("convention", "conventional-commits", "Commit convention")
	breaking := flags.Bool("breaking", false, "Mark as a breaking change")
	if err := flags.Parse(args); err != nil {
		return err
	}
	input := messages.BuildInput{Type: *kind, Scope: *scope, Description: *description, Body: *body, Convention: *convention, Breaking: *breaking}
	if _, err := messages.Build(input); err != nil {
		return err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	data, err := request(server, http.MethodPost, "/api/v1/messages", payload, lang)
	if err != nil {
		return err
	}
	if jsonOutput {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	fmt.Println(result.Message)
	return nil
}

func request(server, method, path string, payload []byte, lang string) ([]byte, error) {
	endpoint := strings.TrimRight(server, "/") + path
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", lang)
	request.Header.Set("User-Agent", projectName+"-cli/"+Version)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "DEBUG %s %s\n", method, endpoint)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var result envelope
		if json.Unmarshal(data, &result) == nil && result.Message != "" {
			return nil, fmt.Errorf("API returned %s: %s", response.Status, result.Message)
		}
		return nil, fmt.Errorf("API returned %s", response.Status)
	}
	var result envelope
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}
	if !result.OK {
		return nil, errors.New(result.Message)
	}
	return result.Data, nil
}

func loadConfig() (*clientConfig, error) {
	config := &clientConfig{}
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read client config: %w", err)
	}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("parse client config: %w", err)
	}
	return config, nil
}

func saveConfig(config *clientConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create client config directory: %w", err)
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode client config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("save client config: %w", err)
	}
	return nil
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "apimgr", projectName, "cli.yml"), nil
}

func validServer(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && !strings.ContainsAny(value, "\r\n\t ")
}

func selectLanguage(flagValue, configValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		if code := normalizeLanguage(flagValue); i18n.IsSupported(code) {
			return code
		}
		return "en"
	}
	for _, value := range []string{flagValue, os.Getenv("GITMESSAGES_LANG"), configValue, os.Getenv("LANG")} {
		code := normalizeLanguage(value)
		if i18n.IsSupported(code) {
			return code
		}
	}
	return "en"
}

func normalizeLanguage(value string) string {
	code := strings.ToLower(strings.TrimSpace(value))
	code = strings.TrimSuffix(code, ".utf-8")
	code = strings.TrimSuffix(code, ".utf8")
	code = strings.SplitN(code, "_", 2)[0]
	return strings.SplitN(code, "-", 2)[0]
}

func printUsage(binary, lang string) {
	fmt.Printf(`%s %s

%s:
%s [--server URL] [--lang CODE] [--json] <command>

%s:
  types         %s
  conventions   %s
  templates     %s
  random        %s
  build         %s

%s:
  --type TYPE --description TEXT [--scope SCOPE] [--body TEXT]
  [--breaking] [--convention conventional-commits|angular]

%s
`, binary, Version, i18n.T(lang, "cli.usage"), binary, i18n.T(lang, "cli.commands"), i18n.T(lang, "cli.types"), i18n.T(lang, "cli.conventions"), i18n.T(lang, "cli.templates"), i18n.T(lang, "cli.random"), i18n.T(lang, "cli.build"), i18n.T(lang, "cli.build_options"), i18n.T(lang, "cli.server_source"))
}
