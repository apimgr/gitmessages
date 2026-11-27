package main

import (
	"bufio"
	"bytes"
	"context"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/apimgr/gitmessages/src/common/display"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/messages"
	projectpaths "github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/update"
	"github.com/mattn/go-runewidth"
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

var apiVersions sync.Map

const projectName = projectpaths.ProjectName

var configProfile string
var apiToken string

type clientConfig struct {
	Language string `yaml:"language"`
	Output   struct {
		Format string `yaml:"format"`
		Color  any    `yaml:"color"`
	} `yaml:"output"`
	Display struct {
		Mode string `yaml:"mode"`
	} `yaml:"display"`
	Server struct {
		Primary    string `yaml:"primary"`
		APIVersion string `yaml:"api_version"`
		Token      string `yaml:"token"`
	} `yaml:"server"`
	Update struct {
		Auto          bool   `yaml:"auto"`
		CheckInterval string `yaml:"check_interval"`
		Channel       string `yaml:"channel"`
	} `yaml:"update"`
}

type envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
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
	tokenFlag := flags.String("token", "", "API bearer token")
	configFlag := flags.String("config", "", "Configuration profile or file path")
	languageFlag := flags.String("lang", "", "Language for output")
	outputFlag := flags.String("output", "", "Output format: json, table, or plain")
	versionFlag := flags.Bool("version", false, "Print version")
	flags.BoolVar(versionFlag, "v", false, "Print version")
	helpFlag := flags.Bool("help", false, "Print help")
	flags.BoolVar(helpFlag, "h", false, "Print help")
	debugFlag := flags.Bool("debug", false, "Print API requests to stderr")
	updateFlag := flags.String("update", "", "Check for or install a CLI update: check, yes")
	colorFlag := flags.String("color", "", "Color output: auto, yes, no")
	shellFlag := flags.String("shell", "", "Print shell completions or shell initialization")
	args = moveClientGlobalFlags(args, "--output", "--color")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", binary, err)
	}
	debugEnabled = *debugFlag
	if *updateFlag != "" && *updateFlag != "check" && *updateFlag != "yes" {
		return errors.New("invalid --update value: expected check or yes")
	}
	if *colorFlag != "" && !validColorMode(*colorFlag) {
		return fmt.Errorf("invalid --color value %q: expected auto, yes, or no", *colorFlag)
	}
	if *outputFlag != "" && !validOutputFormat(*outputFlag) {
		return fmt.Errorf("invalid --output value %q: expected json, table, or plain", *outputFlag)
	}
	displayEnvironment := display.DetectDisplayEnv()
	configProfile = strings.TrimSpace(*configFlag)
	config, err := loadConfig()
	if err != nil {
		return err
	}
	colorMode := firstNonEmpty(*colorFlag, os.Getenv("GITMESSAGES_OUTPUT_COLOR"), configuredColor(config.Output.Color), "auto")
	if !validColorMode(colorMode) {
		return fmt.Errorf("invalid output color mode %q: expected auto, yes, or no", colorMode)
	}
	colorEnabled := display.ColorEnabled(&displayEnvironment, colorMode)
	if colorEnabled && colorMode == "yes" {
		_ = os.Unsetenv("NO_COLOR")
	} else if colorMode == "no" {
		_ = os.Setenv("NO_COLOR", "1")
	}
	if *shellFlag != "" {
		return printClientShellIntegration(*shellFlag, flags.Args(), binary)
	}
	if *versionFlag {
		printVersion(binary)
		return nil
	}
	if previous := os.Getenv("GITMESSAGES_CLI_UPDATE_FROM"); previous != "" {
		recordCLIAudit("cli.update_completed", map[string]string{"old_version": previous, "new_version": os.Getenv("GITMESSAGES_CLI_UPDATE_TO"), "os_arch": runtime.GOOS + "-" + runtime.GOARCH})
		_ = os.Unsetenv("GITMESSAGES_CLI_UPDATE_FROM")
		_ = os.Unsetenv("GITMESSAGES_CLI_UPDATE_TO")
	}
	if err := ensureClientDirectories(); err != nil {
		return err
	}

	lang := selectLanguage(*languageFlag, config.Language)
	outputFormat := firstNonEmpty(*outputFlag, os.Getenv("GITMESSAGES_OUTPUT_FORMAT"), config.Output.Format, "table")
	if !validOutputFormat(outputFormat) {
		return fmt.Errorf("invalid output format %q: expected json, table, or plain", outputFormat)
	}
	apiToken = strings.TrimSpace(config.Server.Token)
	if *tokenFlag != "" {
		apiToken = strings.TrimSpace(*tokenFlag)
		if config.Server.Token == "" {
			config.Server.Token = apiToken
			if err := saveConfig(config); err != nil {
				return err
			}
		}
	}
	if *helpFlag {
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
	if !validServer(server) {
		if flags.NArg() == 0 {
			return launchInteractive(server, lang, config)
		}
		return fmt.Errorf("%s", i18n.T(lang, "cli.server_required"))
	}
	discovery, err := readAutodiscover(server, lang)
	if err != nil {
		return err
	}
	apiVersion := discovery.API.CurrentVersion
	if !validAPIVersion(apiVersion) {
		apiVersion = discovery.Server.APIVersion
	}
	if !validAPIVersion(apiVersion) {
		return errors.New("server returned invalid API version")
	}
	apiVersions.Store(server, apiVersion)
	if err := checkCLIUpdate(server, lang, config, discovery, *updateFlag); err != nil {
		return err
	}
	if *updateFlag == "check" && flags.NArg() == 0 || *updateFlag == "yes" && flags.NArg() == 0 {
		return nil
	}
	paths := map[string]string{
		"types": "/api/{api_version}/types", "list": "/api/{api_version}/types", "conventions": "/api/{api_version}/conventions",
		"templates": "/api/{api_version}/templates", "random": "/api/{api_version}/messages/random",
	}
	if flags.NArg() > 0 {
		command := flags.Arg(0)
		if command != "build" {
			if _, ok := paths[command]; !ok {
				return fmt.Errorf("%s %q; use %s --help", i18n.T(lang, "cli.unknown_command"), command, binary)
			}
		}
	}
	if config.Server.Primary == server && config.Server.APIVersion != apiVersion {
		config.Server.APIVersion = apiVersion
		if err := saveConfig(config); err != nil {
			return err
		}
	}
	if flags.NArg() == 0 {
		return launchInteractive(server, lang, config)
	}
	command := flags.Arg(0)
	if command == "build" {
		return runBuild(server, flags.Args()[1:], outputFormat, colorEnabled, lang)
	}
	path, ok := paths[command]
	if !ok {
		return fmt.Errorf("%s %q; use %s --help", i18n.T(lang, "cli.unknown_command"), command, binary)
	}
	data, err := request(server, http.MethodGet, path, nil, lang)
	if err != nil {
		return err
	}
	formatted, err := formatClientOutputWithColor(data, outputFormat, colorEnabled)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(bytes.TrimRight(formatted, "\n"), '\n'))
	return err
}

func isInteractive() bool {
	return display.DetectDisplayEnv().IsAutoDetectDisplayModeTUI()
}

func printVersion(binary string) {
	fmt.Printf("%s %s (%s)\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\n", binary, Version, CommitID, clientBuildDate(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func runBuild(server string, args []string, outputFormat string, colorEnabled bool, lang string) error {
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
	data, err := request(server, http.MethodPost, "/api/{api_version}/messages", payload, lang)
	if err != nil {
		return err
	}
	formatted, err := formatClientOutputWithColor(data, outputFormat, colorEnabled)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(bytes.TrimRight(formatted, "\n"), '\n'))
	return err
}

func validOutputFormat(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "json", "table", "plain":
		return true
	default:
		return false
	}
}

func moveClientGlobalFlags(args []string, names ...string) []string {
	global := make([]string, 0, len(names)*2)
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		matched := false
		for _, name := range names {
			if argument == name {
				matched = true
				global = append(global, argument)
				if index+1 < len(args) {
					index++
					global = append(global, args[index])
				}
				break
			}
			if strings.HasPrefix(argument, name+"=") {
				matched = true
				global = append(global, argument)
				break
			}
		}
		if !matched {
			remaining = append(remaining, argument)
		}
	}
	return append(global, remaining...)
}

func validColorMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "yes", "no":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func formatClientOutput(data []byte, format string) ([]byte, error) {
	return formatClientOutputWithColor(data, format, false)
}

func formatClientOutputWithColor(data []byte, format string, colorEnabled bool) ([]byte, error) {
	if !validOutputFormat(format) {
		return nil, fmt.Errorf("unsupported output format %q", format)
	}
	format = strings.ToLower(strings.TrimSpace(format))
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode response for %s output: %w", format, err)
	}
	if format == "json" {
		return json.MarshalIndent(value, "", "  ")
	}
	if object, ok := value.(map[string]any); ok {
		if message, ok := object["message"].(string); ok {
			value = message
		}
	}
	if format == "plain" {
		return []byte(plainClientOutput(value)), nil
	}
	return tableClientOutput(value, colorEnabled)
}

func plainClientOutput(value any) string {
	switch typed := value.(type) {
	case string:
		return safeClientText(typed, true)
	case []any:
		lines := make([]string, 0, len(typed))
		for _, item := range typed {
			lines = append(lines, plainClientOutput(item))
		}
		return strings.Join(lines, "\n")
	case map[string]any:
		if message, ok := typed["message"].(string); ok {
			return safeClientText(message, true)
		}
		keys := sortedMapKeys(typed)
		lines := make([]string, 0, len(keys))
		for _, key := range keys {
			lines = append(lines, safeClientText(key, false)+"\t"+clientCell(typed[key]))
		}
		return strings.Join(lines, "\n")
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}

func tableClientOutput(value any, colorEnabled bool) ([]byte, error) {
	rows, ok := value.([]any)
	if !ok {
		if object, objectOK := value.(map[string]any); objectOK {
			rows = make([]any, 0, len(object))
			for _, key := range sortedMapKeys(object) {
				rows = append(rows, map[string]any{"field": key, "value": object[key]})
			}
		} else {
			return []byte(plainClientOutput(value)), nil
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	columns := make([]string, 0)
	seen := make(map[string]bool)
	for _, row := range rows {
		if object, ok := row.(map[string]any); ok {
			for _, key := range sortedMapKeys(object) {
				if !seen[key] {
					seen[key] = true
					columns = append(columns, key)
				}
			}
		}
	}
	if len(columns) == 0 {
		return []byte(plainClientOutput(value)), nil
	}
	widths := make([]int, len(columns))
	for index, column := range columns {
		widths[index] = runewidth.StringWidth(strings.ToUpper(safeClientText(column, false)))
	}
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			continue
		}
		for index, column := range columns {
			if width := runewidth.StringWidth(clientCell(object[column])); width > widths[index] {
				widths[index] = width
			}
		}
	}
	var output bytes.Buffer
	writeBorder := func(left, middle, right, horizontal string) {
		output.WriteString(left)
		for index, width := range widths {
			if index > 0 {
				output.WriteString(middle)
			}
			output.WriteString(strings.Repeat(horizontal, width+2))
		}
		output.WriteString(right + "\n")
	}
	writeRow := func(values []string) {
		output.WriteString("│")
		for index, value := range values {
			output.WriteString(" " + value + strings.Repeat(" ", widths[index]-runewidth.StringWidth(value)+1) + "│")
		}
		output.WriteByte('\n')
	}
	writeBorder("┌", "┬", "┐", "─")
	headers := make([]string, len(columns))
	for index, column := range columns {
		headers[index] = strings.ToUpper(safeClientText(column, false))
	}
	writeRow(headers)
	writeBorder("├", "┼", "┤", "─")
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			continue
		}
		values := make([]string, len(columns))
		for index, column := range columns {
			values[index] = clientCell(object[column])
		}
		writeRow(values)
	}
	writeBorder("└", "┴", "┘", "─")
	result := output.Bytes()
	if colorEnabled {
		lines := bytes.Split(result, []byte("\n"))
		if len(lines) > 2 {
			lines[1] = []byte("\x1b[1;36m" + string(lines[1]) + "\x1b[0m")
		}
		result = bytes.Join(lines, []byte("\n"))
	}
	return bytes.TrimRight(result, "\n"), nil
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func clientCell(value any) string {
	if text, ok := value.(string); ok {
		return safeClientText(text, false)
	}
	encoded, _ := json.Marshal(value)
	return safeClientText(string(encoded), false)
}

func safeClientText(value string, preserveNewlines bool) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' && preserveNewlines {
			return character
		}
		if unicode.IsControl(character) {
			if character == '\t' {
				return ' '
			}
			return -1
		}
		return character
	}, value)
}

func request(server, method, path string, payload []byte, lang string) ([]byte, error) {
	if strings.HasPrefix(path, "/api/{api_version}") {
		version, err := discoverAPIVersion(server, lang)
		if err != nil {
			return nil, err
		}
		path = strings.Replace(path, "/api/{api_version}", "/api/"+version, 1)
	}
	return requestRaw(server, method, path, payload, lang)
}

func discoverAPIVersion(server, lang string) (string, error) {
	if value, ok := apiVersions.Load(server); ok {
		return value.(string), nil
	}
	discovery, err := readAutodiscover(server, lang)
	if err != nil {
		return "", err
	}
	version := discovery.API.CurrentVersion
	if version == "" {
		version = discovery.Server.APIVersion
	}
	if !validAPIVersion(version) {
		return "", errors.New("server returned invalid API version")
	}
	apiVersions.Store(server, version)
	return version, nil
}

type cliDiscovery struct {
	API struct {
		CurrentVersion string `json:"current_version"`
	} `json:"api"`
	Server struct {
		APIVersion string `json:"api_version"`
	} `json:"server"`
	CLIVersions map[string]struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"cli_versions"`
	CLIMinVersion string `json:"cli_min_version"`
}

func readAutodiscover(server, lang string) (*cliDiscovery, error) {
	data, err := requestRaw(server, http.MethodGet, "/api/autodiscover", nil, lang)
	if err != nil {
		return nil, fmt.Errorf("discover API version: %w", err)
	}
	var result cliDiscovery
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode API discovery response: %w", err)
	}
	return &result, nil
}

func checkCLIUpdate(server, lang string, config *clientConfig, discovery *cliDiscovery, requested string) error {
	if config.Update.CheckInterval == "" {
		config.Update.CheckInterval = "per_invocation"
	}
	if config.Update.CheckInterval != "per_invocation" {
		return errors.New("invalid client update.check_interval: expected per_invocation")
	}
	if config.Update.Channel == "" {
		config.Update.Channel = "stable"
	}
	if config.Update.Channel != "stable" {
		return errors.New("invalid client update.channel: expected stable")
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	asset, available := discovery.CLIVersions[platform]
	if !available || asset.Version == "" {
		if requested == "check" || requested == "yes" {
			if _, err := fmt.Fprintln(os.Stdout, "No CLI update is available."); err != nil {
				return fmt.Errorf("write CLI update status: %w", err)
			}
		}
		if discovery.CLIMinVersion != "" && compareCLIVersions(Version, discovery.CLIMinVersion) < 0 {
			return minimumCLIVersionError(discovery.CLIMinVersion)
		}
		return nil
	}
	if discovery.CLIMinVersion != "" && compareCLIVersions(asset.Version, discovery.CLIMinVersion) < 0 {
		return minimumCLIVersionError(discovery.CLIMinVersion)
	}
	comparison := compareCLIVersions(Version, asset.Version)
	if comparison >= 0 {
		if requested == "check" || requested == "yes" {
			if _, err := fmt.Fprintln(os.Stdout, "No CLI update is available."); err != nil {
				return fmt.Errorf("write CLI update status: %w", err)
			}
		}
		return nil
	}
	if requested == "check" {
		if _, err := fmt.Fprintf(os.Stdout, "CLI update available: %s → %s\n", Version, asset.Version); err != nil {
			return fmt.Errorf("write CLI update status: %w", err)
		}
		if discovery.CLIMinVersion != "" && compareCLIVersions(Version, discovery.CLIMinVersion) < 0 {
			return minimumCLIVersionError(discovery.CLIMinVersion)
		}
		return nil
	}
	install := requested == "yes" || (!isInteractive() && config.Update.Auto)
	if !install && isInteractive() {
		fmt.Fprintf(os.Stderr, "CLI update available: %s → %s. Install now? [y/N] ", Version, asset.Version)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		install = strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes")
	}
	if !install {
		if discovery.CLIMinVersion != "" && compareCLIVersions(Version, discovery.CLIMinVersion) < 0 {
			return minimumCLIVersionError(discovery.CLIMinVersion)
		}
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(server), "https://") {
		return errors.New("CLI updates require a server URL using HTTPS")
	}
	assetName := projectpaths.ProjectName + "-cli-" + platform
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	if len(asset.SHA256) != 64 {
		return errors.New("server returned an invalid CLI update checksum")
	}
	url := strings.TrimRight(server, "/") + "/cli/binaries/" + assetName
	var installErr error
	if apiToken == "" {
		installErr = update.InstallFromURL(context.Background(), url, asset.SHA256, Version, asset.Version)
	} else {
		installErr = update.InstallFromURLWithBearerToken(context.Background(), url, asset.SHA256, Version, asset.Version, apiToken)
	}
	if installErr != nil {
		if strings.Contains(installErr.Error(), "SHA-256") {
			recordCLIAudit("cli.update_checksum_invalid", map[string]string{"old_version": Version, "new_version": asset.Version, "os_arch": platform})
		}
		return installErr
	}
	return nil
}

func minimumCLIVersionError(minimum string) error {
	recordCLIAudit("cli.update_forced", map[string]string{"current_version": Version, "minimum_version": minimum})
	return fmt.Errorf("this CLI is too old; the server requires %s — run '%s-cli --update yes' to upgrade", minimum, projectpaths.ProjectName)
}

func compareCLIVersions(left, right string) int {
	type parsedVersion struct {
		core       []int
		prerelease string
	}
	parse := func(value string) (parsedVersion, bool) {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value = strings.SplitN(value, "+", 2)[0]
		parts := strings.SplitN(value, "-", 2)
		prerelease := ""
		if len(parts) == 2 {
			prerelease = parts[1]
		}
		fields := strings.Split(parts[0], ".")
		if len(fields) != 3 {
			return parsedVersion{}, false
		}
		result := make([]int, 3)
		for index, field := range fields {
			parsed, err := strconv.Atoi(field)
			if err != nil || parsed < 0 {
				return parsedVersion{}, false
			}
			result[index] = parsed
		}
		return parsedVersion{core: result, prerelease: prerelease}, true
	}
	a, okA := parse(left)
	b, okB := parse(right)
	if !okA || !okB {
		return strings.Compare(left, right)
	}
	for index := range a.core {
		if a.core[index] < b.core[index] {
			return -1
		}
		if a.core[index] > b.core[index] {
			return 1
		}
	}
	if a.prerelease == "" && b.prerelease != "" {
		return 1
	}
	if a.prerelease != "" && b.prerelease == "" {
		return -1
	}
	if a.prerelease != b.prerelease {
		return strings.Compare(a.prerelease, b.prerelease)
	}
	return 0
}

func validAPIVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func requestRaw(server, method, path string, payload []byte, lang string) ([]byte, error) {
	endpoint := strings.TrimRight(server, "/") + path
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", lang)
	request.Header.Set("User-Agent", projectName+"-cli/"+Version)
	if apiToken != "" {
		request.Header.Set("Authorization", "Bearer "+apiToken)
	}
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
	config.Server.APIVersion = "v1"
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
	if config.Output.Format != "" && !validOutputFormat(config.Output.Format) {
		return nil, fmt.Errorf("invalid client output.format %q: expected json, table, or plain", config.Output.Format)
	}
	if config.Output.Color != nil && configuredColor(config.Output.Color) == "" {
		return nil, fmt.Errorf("invalid client output.color %v: expected boolean or auto, yes, or no", config.Output.Color)
	}
	if config.Language != "" && !i18n.IsSupported(normalizeLanguage(config.Language)) {
		return nil, fmt.Errorf("unsupported language in client config: %q", config.Language)
	}
	return config, nil
}

func configuredColor(value any) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "yes"
		}
		return "no"
	case string:
		if validColorMode(typed) {
			return strings.ToLower(strings.TrimSpace(typed))
		}
	}
	return ""
}

func saveConfig(config *clientConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
	if err := file.Chmod(0o600); err != nil {
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
	base := filepath.Join(dir, projectpaths.OrgName, projectName)
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
		filepath.Join(configDir, projectpaths.OrgName, projectName),
		filepath.Join(dataDir, projectpaths.OrgName, projectName),
		filepath.Join(cacheDir, projectpaths.OrgName, projectName),
		filepath.Join(logDir, projectpaths.OrgName, projectName),
	}
	for _, path := range dirs {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create client directory: %w", err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o700); err != nil {
				return fmt.Errorf("secure client directory: %w", err)
			}
		}
	}
	logPath := filepath.Join(dirs[3], "cli.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create client log: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := logFile.Chmod(0o600); err != nil {
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
	if strings.TrimSpace(configValue) != "" && strings.TrimSpace(configValue) != "auto" {
		if code := normalizeLanguage(configValue); i18n.IsSupported(code) {
			return code
		}
		return "en"
	}
	for _, value := range []string{os.Getenv("LC_ALL"), os.Getenv("LANG")} {
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
%s [--server URL] [--token TOKEN] [--config PROFILE|PATH] [--lang CODE] [--output json|table|plain] [--color auto|yes|no] [--update check|yes] <command>

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
