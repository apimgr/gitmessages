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
	"runtime"
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

var configProfile string

type clientConfig struct {
	Language string `yaml:"language"`
	Display  struct {
		Mode string `yaml:"mode"`
	} `yaml:"display"`
	Server struct {
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
	configFlag := flags.String("config", "", "Configuration profile or file path")
	languageFlag := flags.String("lang", "", "Language for output")
	jsonOutput := flags.Bool("json", false, "Print the API response as JSON")
	versionFlag := flags.Bool("version", false, "Print version")
	flags.BoolVar(versionFlag, "v", false, "Print version")
	helpFlag := flags.Bool("help", false, "Print help")
	flags.BoolVar(helpFlag, "h", false, "Print help")
	debugFlag := flags.Bool("debug", false, "Print API requests to stderr")
	colorFlag := flags.String("color", "auto", "Color output: auto, yes, no")
	shellFlag := flags.String("shell", "", "Print shell completions: bash, zsh, fish, or powershell")
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
		printVersion(binary)
		return nil
	}
	configProfile = strings.TrimSpace(*configFlag)
	if err := ensureClientDirectories(); err != nil {
		return err
	}

	config, err := loadConfig()
	if err != nil {
		return err
	}
	lang := selectLanguage(*languageFlag, config.Language)
	if *helpFlag {
		printUsage(binary, lang)
		return nil
	}
	if *shellFlag != "" {
		return printClientCompletions(*shellFlag, binary)
	}

	server := strings.TrimSpace(*serverFlag)
	if server == "" {
		server = strings.TrimSpace(os.Getenv("GITMESSAGES_SERVER_PRIMARY"))
	}
	if server == "" {
		server = strings.TrimSpace(config.Server.Primary)
	}
	if *serverFlag != "" {
		if !validServer(*serverFlag) {
			fmt.Fprintln(os.Stderr, "warning: invalid --server value; using configured server")
			server = strings.TrimSpace(config.Server.Primary)
		} else if !validServer(config.Server.Primary) {
			config.Server.Primary = *serverFlag
			if err := saveConfig(config); err != nil {
				return err
			}
		}
	}
	if flags.NArg() == 0 {
		return launchInteractive(server, lang, config)
	}
	if !validServer(server) {
		return fmt.Errorf("%s", i18n.T(lang, "cli.server_required"))
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

func isInteractive() bool {
	input, inputErr := os.Stdin.Stat()
	output, outputErr := os.Stdout.Stat()
	return inputErr == nil && outputErr == nil && input.Mode()&os.ModeCharDevice != 0 && output.Mode()&os.ModeCharDevice != 0
}

func printVersion(binary string) {
	fmt.Printf("%s %s (%s)\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\n", binary, Version, CommitID, clientBuildDate(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
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
	defer func() { _ = response.Body.Close() }()
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("parse client config: %w", err)
	}
	if config.Display.Mode != "" && config.Display.Mode != "auto" && config.Display.Mode != "gui" && config.Display.Mode != "tui" {
		return nil, fmt.Errorf("invalid client display.mode %q: expected auto, gui, or tui", config.Display.Mode)
	}
	if config.Language != "" && !i18n.IsSupported(normalizeLanguage(config.Language)) {
		return nil, fmt.Errorf("unsupported language in client config: %q", config.Language)
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
	file, err := os.CreateTemp(filepath.Dir(path), ".cli.yml-*")
	if err != nil {
		return fmt.Errorf("create temporary client config: %w", err)
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure temporary client config: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write client config: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync client config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close client config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace client config: %w", err)
	}
	return nil
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	base := filepath.Join(dir, "apimgr", projectName)
	if configProfile == "" {
		return filepath.Join(base, "cli.yml"), nil
	}
	profile := configProfile
	if strings.HasPrefix(profile, "~/") || strings.HasPrefix(profile, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		profile = filepath.Join(home, profile[2:])
	}
	if filepath.IsAbs(profile) || strings.ContainsRune(profile, filepath.Separator) {
		return filepath.Abs(profile)
	}
	if filepath.Ext(profile) == "" {
		profile += ".yml"
	}
	return filepath.Join(base, filepath.Base(profile)), nil
}

func ensureClientDirectories() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve client config directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve client home directory: %w", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("resolve client cache directory: %w", err)
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share")
	}
	logDir := filepath.Join(home, ".local", "log")
	if runtime.GOOS == "windows" {
		dataDir = os.Getenv("LOCALAPPDATA")
		if dataDir == "" {
			dataDir = filepath.Join(home, "AppData", "Local")
		}
		logDir = filepath.Join(dataDir, "log")
	}
	dirs := []string{
		filepath.Join(configDir, "apimgr", projectName),
		filepath.Join(dataDir, "apimgr", projectName),
		filepath.Join(cacheDir, "apimgr", projectName),
		filepath.Join(logDir, "apimgr", projectName),
	}
	for _, path := range dirs {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create client directory: %w", err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0700); err != nil {
				return fmt.Errorf("secure client directory: %w", err)
			}
		}
	}
	logPath := filepath.Join(dirs[3], "cli.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create client log: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := logFile.Chmod(0600); err != nil {
			_ = logFile.Close()
			return fmt.Errorf("secure client log: %w", err)
		}
	}
	return logFile.Close()
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
%s [--server URL] [--config PROFILE|PATH] [--lang CODE] [--json] <command>

%s:
  types         %s
  conventions   %s
  templates     %s
  random        %s
  build         %s

%s:
  --type TYPE --description TEXT [--scope SCOPE] [--body TEXT]
  [--breaking] [--convention conventional-commits|angular]

With no command in an interactive terminal, opens the terminal menu.
Shell completions: %s --shell bash|zsh|fish|powershell

%s
`, binary, Version, i18n.T(lang, "cli.usage"), binary, i18n.T(lang, "cli.commands"), i18n.T(lang, "cli.types"), i18n.T(lang, "cli.conventions"), i18n.T(lang, "cli.templates"), i18n.T(lang, "cli.random"), i18n.T(lang, "cli.build"), binary, i18n.T(lang, "cli.build_options"), i18n.T(lang, "cli.server_source"))
}
