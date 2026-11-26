package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/cache"
	"github.com/apimgr/gitmessages/src/common/display"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/email"
	"github.com/apimgr/gitmessages/src/metrics"
	"github.com/apimgr/gitmessages/src/mode"
	"github.com/apimgr/gitmessages/src/overlay"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/privilege"
	schedulerpkg "github.com/apimgr/gitmessages/src/scheduler"
	"github.com/apimgr/gitmessages/src/securitydata"
	servicepkg "github.com/apimgr/gitmessages/src/service"
	"github.com/apimgr/gitmessages/src/ssl"
	updatepkg "github.com/apimgr/gitmessages/src/update"
	"github.com/apimgr/gitmessages/src/web"
	"golang.org/x/term"
)

// Version information (set by build flags)
var (
	Version      = "dev"
	CommitID     = "unknown"
	BuildEpoch   = "0"
	OfficialSite = ""
)

// buildDate derives the RFC 3339 UTC build date from the embedded BuildEpoch; N/A when unset or invalid
func buildDate() string {
	seconds, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil || seconds <= 0 {
		return "N/A"
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

var cfg *config.Config

var startedElevated = privilege.IsElevated()

var pendingUpdateFailure string

var updateRestartRequests = make(chan struct{}, 1)

type optionalUpdate string

func (value *optionalUpdate) String() string { return string(*value) }

func (value *optionalUpdate) Set(argument string) error {
	if argument == "true" {
		*value = "yes"
		return nil
	}
	*value = optionalUpdate(argument)
	return nil
}

func (*optionalUpdate) IsBoolFlag() bool { return true }

func resolveDirectoryOverride(name, flagValue, environmentValue, fallback string) (string, error) {
	value := fallback
	if environmentValue != "" {
		value = environmentValue
	}
	if flagValue != "" {
		value = flagValue
	}
	safe, err := paths.SafeDirPath(value)
	if err != nil {
		return "", fmt.Errorf("invalid %s directory: %w", name, err)
	}
	absolute, err := filepath.Abs(safe)
	if err != nil {
		return "", fmt.Errorf("resolve %s directory: %w", name, err)
	}
	return absolute, nil
}

func resolvePIDFileOverride(flagValue, environmentValue, fallback string) (string, error) {
	return resolveDirectoryOverride("PID file", flagValue, environmentValue, fallback)
}

func restartRequiredSettings(current, incoming config.ServerConfig) []string {
	var changed []string
	if current.Port != incoming.Port {
		changed = append(changed, "server.port")
	}
	if current.Address != incoming.Address {
		changed = append(changed, "server.address")
	}
	if current.SSL.Enabled != incoming.SSL.Enabled {
		changed = append(changed, "server.ssl.enabled")
	}
	if current.SSL.Cert != incoming.SSL.Cert {
		changed = append(changed, "server.ssl.cert")
	}
	if current.SSL.Key != incoming.SSL.Key {
		changed = append(changed, "server.ssl.key")
	}
	if current.SSL.MinVersion != incoming.SSL.MinVersion {
		changed = append(changed, "server.ssl.min_version")
	}
	if current.Daemonize != incoming.Daemonize {
		changed = append(changed, "server.daemonize")
	}
	if current.PIDFile != incoming.PIDFile {
		changed = append(changed, "server.pidfile")
	}
	if !reflect.DeepEqual(current.Database, incoming.Database) {
		changed = append(changed, "server.database")
	}
	return changed
}

func preserveRestartRequiredSettings(current config.ServerConfig, incoming *config.ServerConfig) {
	incoming.Port = current.Port
	incoming.Address = current.Address
	incoming.Daemonize = current.Daemonize
	incoming.PIDFile = current.PIDFile
	incoming.Database = current.Database
	incoming.SSL.Enabled = current.SSL.Enabled
	incoming.SSL.Cert = current.SSL.Cert
	incoming.SSL.Key = current.SSL.Key
	incoming.SSL.MinVersion = current.SSL.MinVersion
}

func configuredTLSHosts(configuration *config.Config) []string {
	if configuration == nil {
		return nil
	}
	fqdn := strings.TrimSpace(configuration.Server.FQDN)
	if fqdn == "" {
		fqdn = strings.TrimSpace(strings.Split(os.Getenv("DOMAIN"), ",")[0])
	}
	if fqdn == "" {
		return nil
	}
	return []string{fqdn}
}

type serverListener struct {
	port int
	tls  bool
}

type overlayManagers struct {
	mu  sync.RWMutex
	tor *overlay.TorManager
	i2p *overlay.I2PManager
}

func (managers *overlayManagers) current() (*overlay.TorManager, *overlay.I2PManager) {
	managers.mu.RLock()
	defer managers.mu.RUnlock()
	return managers.tor, managers.i2p
}

func (managers *overlayManagers) replaceTor(manager *overlay.TorManager) {
	managers.mu.Lock()
	managers.tor = manager
	managers.mu.Unlock()
}

func (managers *overlayManagers) replaceI2P(manager *overlay.I2PManager) {
	managers.mu.Lock()
	managers.i2p = manager
	managers.mu.Unlock()
}

func parseServerListeners(value string, sslEnabled bool) ([]serverListener, error) {
	parts := strings.Split(value, ",")
	if len(parts) > 2 || value == "" {
		return nil, fmt.Errorf("expected one port or an HTTP,HTTPS pair")
	}
	listeners := make([]serverListener, 0, len(parts))
	for index, part := range parts {
		part = strings.TrimSpace(part)
		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid server port %q", part)
		}
		useTLS := (len(parts) == 2 && index == 1) || (len(parts) == 1 && (port == 443 || sslEnabled))
		listeners = append(listeners, serverListener{port: port, tls: useTLS})
	}
	return listeners, nil
}

func validateTLSChallenge(listeners []serverListener, letsEncrypt config.LetsEncryptConfig) error {
	if !letsEncrypt.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(letsEncrypt.Challenge)) {
	case "", "http-01":
		for _, listener := range listeners {
			if listener.port == 80 && !listener.tls {
				return nil
			}
		}
		return fmt.Errorf("HTTP-01 requires a plain HTTP listener on port 80")
	case "tls-alpn-01":
		for _, listener := range listeners {
			if listener.port == 443 && listener.tls {
				return nil
			}
		}
		return fmt.Errorf("TLS-ALPN-01 requires an HTTPS listener on port 443")
	case "dns-01":
		return nil
	default:
		return fmt.Errorf("unsupported ACME challenge type %q", letsEncrypt.Challenge)
	}
}

func init() {
	log.SetPrefix("gitmessages: ")
	log.SetFlags(log.Lshortfile)
	log.SetOutput(io.MultiWriter(os.Stderr, metrics.LogWriter()))
}

func main() {
	// Get default directories
	dirs := paths.GetDirectories()

	// Flags
	port := flag.String("port", "", "Server port (overrides config)")
	address := flag.String("address", "", "Server address (overrides config)")
	baseURLFlag := flag.String("baseurl", "", "URL path prefix (overrides config)")
	configDirFlag := flag.String("config", "", "Configuration directory")
	dataDirFlag := flag.String("data", "", "Data directory")
	cacheDirFlag := flag.String("cache", "", "Cache directory")
	logDirFlag := flag.String("log", "", "Log directory")
	backupDirFlag := flag.String("backup", "", "Backup directory")
	pidFileFlag := flag.String("pid", "", "PID file")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.BoolVar(showVersion, "v", false, "Show version information")
	showStatus := flag.Bool("status", false, "Check server status (for health checks)")
	showHelp := flag.Bool("help", false, "Show help")
	flag.BoolVar(showHelp, "h", false, "Show help")
	daemonFlag := flag.Bool("daemon", false, "Run as a daemon")
	debugFlag := flag.Bool("debug", false, "Enable debug logging")
	colorFlag := flag.String("color", "auto", "Color output: auto, yes, no")
	languageFlag := flag.String("lang", "", "Language for output")
	shellFlag := flag.String("shell", "", "Shell integration: completions or init")

	// Mode and update flags
	modeFlag := flag.String("mode", "", "Application mode: production, development, debug")
	updateCmd := optionalUpdate("")
	flag.Var(&updateCmd, "update", "Update commands: check, yes, branch {stable|beta|devel}")

	// Service commands
	serviceCmd := flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable")

	// Maintenance commands
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup, pgp, secret, security, token, data, compliance")
	includeSSLFlag := flag.Bool("include-ssl", false, "Include SSL files with --maintenance backup")
	includeDataFlag := flag.Bool("include-data", false, "Include data files with --maintenance backup")
	i2pFlag := flag.Bool("i2p", false, "Enable the I2P eepsite")
	if name, ok := commandHelp(os.Args[1:], "shell", "service", "maintenance", "update"); ok {
		printCommandHelp(name)
		return
	}

	flag.Parse()
	if updateCmd == "yes" && len(flag.Args()) > 0 {
		switch flag.Args()[0] {
		case "check", "yes", "branch":
			updateCmd = optionalUpdate(flag.Args()[0])
		}
	}
	if (*includeSSLFlag || *includeDataFlag) && *maintenanceCmd != "backup" {
		fmt.Fprintln(os.Stderr, "--include-ssl and --include-data are only valid with --maintenance backup")
		os.Exit(2)
	}

	if err := configureOutput(*colorFlag, *languageFlag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Handle --help
	if *showHelp {
		printHelp(filepath.Base(os.Args[0]))
		return
	}
	if *shellFlag != "" {
		shell := ""
		if len(flag.Args()) > 0 {
			shell = flag.Args()[0]
		}
		if err := printShellIntegration(*shellFlag, shell); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	// Handle --version
	if *showVersion {
		printVersion(filepath.Base(os.Args[0]))
		return
	}

	// Resolve directory overrides on every startup: flags take precedence over environment variables.
	directoryOverrides := []struct {
		name      string
		flagValue string
		envName   string
		target    *string
	}{
		{name: "config", flagValue: *configDirFlag, envName: "CONFIG_DIR", target: &dirs.Config},
		{name: "data", flagValue: *dataDirFlag, envName: "DATA_DIR", target: &dirs.Data},
		{name: "cache", flagValue: *cacheDirFlag, envName: "CACHE_DIR", target: &dirs.Cache},
		{name: "log", flagValue: *logDirFlag, envName: "LOG_DIR", target: &dirs.Logs},
		{name: "backup", flagValue: *backupDirFlag, envName: "BACKUP_DIR", target: &dirs.Backup},
		{name: "database", envName: "DATABASE_DIR", target: &dirs.Database},
	}
	for _, override := range directoryOverrides {
		resolved, err := resolveDirectoryOverride(override.name, override.flagValue, os.Getenv(override.envName), *override.target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(2)
		}
		*override.target = resolved
	}
	pidFile, err := resolvePIDFileOverride(*pidFileFlag, os.Getenv("PID_FILE"), dirs.PID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid PID file path: %v\n", err)
		os.Exit(2)
	}
	dirs.PID = pidFile
	configDir := dirs.Config

	if *showStatus {
		checkPort := *port
		if checkPort == "" {
			checkPort = strings.TrimSpace(os.Getenv("PORT"))
		}
		if checkPort == "" {
			checkPort, _, _, _ = config.EarlyServerSettings(filepath.Join(configDir, "server.yml"))
		}
		if checkPort == "" {
			checkPort = "64000"
		}
		checkPort = strings.Split(checkPort, ",")[0]
		checkBaseURL := *baseURLFlag
		if checkBaseURL == "" {
			checkBaseURL = config.EarlyBaseURL(filepath.Join(configDir, "server.yml"))
		}
		checkBaseURL, err := config.NormalizeBaseURL(checkBaseURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --baseurl: %v\n", err)
			os.Exit(2)
		}
		if err := checkHealth(checkPort, checkBaseURL); err != nil {
			fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
		return
	}
	if *maintenanceCmd == "--help" {
		fmt.Println("Usage: gitmessages --maintenance {backup|restore|update|mode|setup|pgp|secret|security|token|data|compliance} [arguments]")
		return
	}
	if updateCmd == "help" {
		fmt.Println("Usage: gitmessages --update [check|yes|branch {stable|beta|devel}]")
		return
	}

	// Service commands exit before normal startup creates paths or loads config.
	if *serviceCmd != "" {
		servicepkg.SetStartupPreparation(func() error {
			_, err := prepareStartupDirectories(dirs)
			return err
		})
		handleServiceCommand(*serviceCmd, configDir, dirs)
		return
	}
	configPath := filepath.Join(configDir, "server.yml")
	if *modeFlag != "" {
		if err := paths.EnsureDirectories(dirs); err != nil {
			fmt.Fprintf(os.Stderr, "prepare command directories: %v\n", err)
			os.Exit(1)
		}
		setApplicationMode(*modeFlag, configPath)
		return
	}
	if updateCmd != "" || *maintenanceCmd != "" {
		if err := paths.EnsureDirectories(dirs); err != nil {
			fmt.Fprintf(os.Stderr, "prepare command directories: %v\n", err)
			os.Exit(1)
		}
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load command configuration: %v\n", err)
			os.Exit(1)
		}
		if updateCmd != "" {
			handleUpdateCommand(string(updateCmd), cfg)
			return
		}
		handleMaintenanceCommand(*maintenanceCmd, configDir, dirs.Data, dirs.Database, dirs.Logs, dirs.Backup, dirs.PID, cfg, *includeSSLFlag, *includeDataFlag)
		return
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "email" {
		if err := paths.EnsureDirectories(dirs); err != nil {
			fmt.Fprintf(os.Stderr, "prepare email command directories: %v\n", err)
			os.Exit(1)
		}
		loaded, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load email command configuration: %v\n", err)
			os.Exit(1)
		}
		dryRun := false
		for _, arg := range args[1:] {
			if arg == "--dry-run" {
				dryRun = true
				break
			}
		}
		if !dryRun {
			configureSMTP(loaded)
		}
		var store *database.Store
		if !dryRun {
			store, err = database.OpenConfigured(loaded.Server.Database, filepath.Join(dirs.Database, "server.db"), 2)
			if err != nil {
				fmt.Fprintf(os.Stderr, "open email audit database: %v\n", err)
				os.Exit(1)
			}
			defer func() { _ = store.Close() }()
		}
		if err := runEmailCommand(args[1:], loaded, configDir, store, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if startedElevated && runtime.GOOS != "windows" {
		if stagedUpdateExists(dirs.Data) {
			stagedVersion := stagedUpdateVersion(dirs.Data)
			epoch, _ := strconv.ParseInt(BuildEpoch, 10, 64)
			executable, executableErr := os.Executable()
			if executableErr == nil {
				if _, applyErr := updatepkg.ApplyStaged(context.Background(), dirs.Data, paths.ProjectName, executable, Version, epoch, startedElevated); applyErr != nil {
					pendingUpdateFailure = stagedVersion
					log.Printf("ERROR update.failed version=%s error=%q", pendingUpdateFailure, applyErr.Error())
				}
			} else {
				pendingUpdateFailure = stagedVersion
				_ = os.RemoveAll(filepath.Join(dirs.Data, "update"))
				log.Printf("ERROR update.failed version=%s error=%q", pendingUpdateFailure, "locate executable failed")
			}
		}
	}
	startupIdentity, err := prepareStartupDirectories(dirs)
	if err != nil {
		log.Fatalf("prepare startup directories: %v", err)
	}
	earlyPort, earlyAddress, earlyDaemonize, earlyPIDFile := config.EarlyServerSettings(configPath)
	if !earlyPIDFile || paths.IsRunningInContainer() {
		dirs.PID = ""
	}
	selectedEarlyPort := earlyPort
	if envPort := strings.TrimSpace(os.Getenv("PORT")); envPort != "" {
		selectedEarlyPort = envPort
	}
	if *port != "" {
		selectedEarlyPort = *port
	}
	if selectedEarlyPort == "" {
		selectedEarlyPort = config.DefaultConfig().Server.Port
	}
	selectedEarlyAddress := earlyAddress
	if selectedEarlyAddress == "" {
		selectedEarlyAddress = "[::]"
	}
	if envAddress := strings.TrimSpace(os.Getenv("LISTEN")); envAddress != "" {
		selectedEarlyAddress = envAddress
	}
	if *address != "" {
		selectedEarlyAddress = *address
	}
	if (*daemonFlag || earlyDaemonize) && *serviceCmd == "" && !paths.IsRunningInContainer() {
		if err := daemonizeProcess(); err != nil {
			log.Fatalf("daemonize server: %v", err)
		}
	}
	selectedEarlyAddress = strings.Trim(selectedEarlyAddress, "[]")
	if net.ParseIP(selectedEarlyAddress) == nil {
		log.Fatalf("invalid server address %q: expected an IP address", selectedEarlyAddress)
	}
	privilegedListeners := make(map[int]net.Listener)
	if selectedEarlyPort != "" && runtime.GOOS != "windows" {
		parts := strings.Split(selectedEarlyPort, ",")
		for _, part := range parts {
			value, parseErr := strconv.Atoi(strings.TrimSpace(part))
			if parseErr != nil || value < 1 || value > 65535 {
				log.Fatalf("invalid server port %q: expected 1-65535", part)
			}
			if value < 1024 {
				if !startupIdentity.system && !paths.IsRunningInContainer() {
					log.Fatalf("port %d requires elevated privileges; use a port >= 1024 or install the service", value)
				}
				if !startupIdentity.system {
					continue
				}
				listener, listenErr := net.Listen("tcp", net.JoinHostPort(selectedEarlyAddress, strconv.Itoa(value)))
				if listenErr != nil {
					log.Fatalf("bind privileged port %d: %v", value, listenErr)
				}
				privilegedListeners[value] = listener
			}
		}
	}
	if err := dropToServiceAccount(startupIdentity, dirs.Data); err != nil {
		log.Fatalf("drop service privileges: %v", err)
	}
	pidMode := os.FileMode(0o600)
	if startupIdentity.system {
		pidMode = 0o644
	}
	if earlyPIDFile && !paths.IsRunningInContainer() {
		if err := paths.WritePIDFileMode(dirs.PID, pidMode); err != nil {
			log.Fatalf("check and write PID file: %v", err)
		}
		defer func() {
			if err := paths.RemovePIDFile(dirs.PID); err != nil {
				log.Printf("remove PID file: %v", err)
			}
		}()
	}

	// Load configuration
	_, configStatErr := os.Stat(configPath)
	firstConfigRun := errors.Is(configStatErr, os.ErrNotExist)
	if firstConfigRun {
		_, legacyConfigErr := os.Stat(strings.TrimSuffix(configPath, ".yml") + ".yaml")
		firstConfigRun = errors.Is(legacyConfigErr, os.ErrNotExist)
	}
	cfg, err = config.Load(configPath)
	if err != nil {
		log.Fatalf("load server configuration: %v", err)
	}
	if err := applyI2POverrides(cfg, *i2pFlag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *baseURLFlag != "" {
		baseURL, err := config.NormalizeBaseURL(*baseURLFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --baseurl: %v\n", err)
			os.Exit(2)
		}
		cfg.Server.BaseURL = baseURL
	}
	web.SetCORSOrigins(cfg.Server.CORS.AllowedOrigins)
	web.SetServerFQDN(cfg.Server.FQDN)
	web.SetDefaultTheme(cfg.Web.UI.Theme)
	var debugOverride *bool
	if flagWasProvided("debug") {
		debugOverride = debugFlag
	}
	mode.InitializeWithConfig(*modeFlag, cfg.Server.Mode, debugOverride)
	mode.SetProfilingRates(cfg.Server.Debug.BlockProfileRate, cfg.Server.Debug.MutexProfileFraction)
	if err := configureStructuredLogger(cfg.Server.Logging.Level); err != nil {
		log.Fatalf("configure structured logger: %v", err)
	}
	if firstConfigRun {
		log.Printf("Configuration file: %s", configPath)
	}
	modeInfo := string(mode.Get())
	if mode.IsDebugEnabled() {
		modeInfo += " [debugging]"
	}
	log.Printf("Running in mode: %s", modeInfo)

	if len(flag.Args()) != 0 {
		flag.Usage()
		return
	}
	configureSMTP(cfg)

	// Determine port (flag > env > config > default)
	serverPort := cfg.Server.Port
	if envPort := strings.TrimSpace(os.Getenv("PORT")); envPort != "" {
		serverPort = envPort
	}
	if *port != "" {
		serverPort = *port
	}
	if serverPort == "" {
		serverPort = config.DefaultConfig().Server.Port
	}
	listeners, portErr := parseServerListeners(serverPort, cfg.Server.SSL.Enabled)
	if portErr != nil {
		fmt.Fprintf(os.Stderr, "invalid server port %q: %v\n", serverPort, portErr)
		os.Exit(2)
	}
	if challengeErr := validateTLSChallenge(listeners, cfg.Server.SSL.LetsEncrypt); challengeErr != nil {
		fmt.Fprintf(os.Stderr, "invalid server SSL configuration: %v\n", challengeErr)
		os.Exit(2)
	}

	// Determine address (flag > env > config > default)
	serverAddress := cfg.Server.Address
	if envAddress := strings.TrimSpace(os.Getenv("LISTEN")); envAddress != "" {
		serverAddress = envAddress
	}
	if *address != "" {
		serverAddress = *address
	}
	if serverAddress == "" {
		serverAddress = config.DefaultConfig().Server.Address
	}
	if net.ParseIP(strings.Trim(serverAddress, "[]")) == nil {
		fmt.Fprintf(os.Stderr, "invalid server address %q: expected an IP address\n", serverAddress)
		os.Exit(2)
	}

	// Build listen address
	listenAddresses := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		listenAddresses = append(listenAddresses, net.JoinHostPort(strings.Trim(serverAddress, "[]"), strconv.Itoa(listener.port)))
	}

	// Log startup information
	displayEnvironment := display.DetectDisplayEnv()
	if displayEnvironment.IsTerminal && !paths.IsRunningInContainer() && !*daemonFlag && !earlyDaemonize {
		publicURL := startupPublicURL(cfg.Server.FQDN, cfg.Server.BaseURL, listeners)
		if err := writeStartupBanner(os.Stdout, displayEnvironment, *colorFlag, Version, CommitID, buildDate(), modeInfo, publicURL); err != nil {
			log.Printf("write startup banner: %v", err)
		}
	}
	log.Printf("gitmessages %s (commit: %s, built: %s)", Version, CommitID, buildDate())

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	// Setup HTTP server
	sharedDB, err := database.OpenConfigured(cfg.Server.Database, filepath.Join(dirs.Database, "server.db"), 5)
	if err != nil {
		log.Fatalf("open application database: %v", err)
	}
	defer func() { _ = sharedDB.Close() }()
	if err := recoverEncryptionKeyRotation(context.Background(), sharedDB, cfg); err != nil {
		log.Fatalf("recover encryption key rotation: %v", err)
	}
	if err := recoverInstallationSecretPGPKey(context.Background(), sharedDB, configDir); err != nil {
		log.Fatalf("recover PGP private key after secret rotation: %v", err)
	}
	if err := recoverPGPKeyState(context.Background(), sharedDB, configDir); err != nil {
		log.Fatalf("recover PGP key state: %v", err)
	}
	cacheCounter, err := cache.New(cfg.Server.Cache, configDir)
	if err != nil {
		log.Fatalf("configure rate limit cache: %v", err)
	}
	if cacheCounter != nil {
		web.ConfigureRateLimitCache(cacheCounter)
		defer func() {
			web.ConfigureRateLimitCache(nil)
			if err := cacheCounter.Close(); err != nil {
				log.Printf("WARN cache.close_failed error=%q", err)
			}
		}()
	}
	securityDirectory, err := securityDataDirectory(cfg.Data.SecurityDir, dirs.Data)
	if err != nil {
		log.Fatalf("resolve security data directory: %v", err)
	}
	securityManager := securitydata.New(securityDirectory)
	securityLogFile, err := os.OpenFile(filepath.Join(dirs.Logs, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatalf("open background task log: %v", err)
	}
	defer func() { _ = securityLogFile.Close() }()
	securityLogger := log.New(securityLogFile, "", log.LstdFlags)
	var geoIPReader *securitydata.GeoIP
	if opened, openErr := securitydata.OpenGeoIP(filepath.Join(securityDirectory, "geoip")); openErr == nil {
		geoIPReader = opened
		defer func() { _ = opened.Close() }()
	}
	web.ConfigureSecurityData(securityManager, geoIPReader)
	defer web.CloseSecurityData()
	securityContext, cancelSecurity := context.WithCancel(context.Background())
	defer cancelSecurity()
	go initializeSecurityData(securityContext, securityManager, cfg, securityLogger)
	if generated := config.ConsumeGeneratedSecrets(); len(generated) > 0 {
		operatorTokenRegenerated := false
		if err := sharedDB.Write(context.Background(), func(tx *sql.Tx) error {
			for _, name := range generated {
				if name == "server.token" {
					operatorTokenRegenerated = true
				}
				if _, err := tx.Exec(`INSERT INTO audit_log(level, category, action, details) VALUES ('info','config','config.secret_generated',?)`, fmt.Sprintf(`{"key":%q}`, name)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			log.Printf("could not record generated-secret audit entries: %v", err)
		}
		if operatorTokenRegenerated {
			if err := sharedDB.RotateAppSecret(context.Background(), "csrf_token_secret"); err != nil {
				log.Fatalf("rotate CSRF secret after operator token regeneration: %v", err)
			}
		}
	}
	authStore, err := auth.NewStore(sharedDB, cfg.Server.Token, cfg.Server.Security.EncryptionKey, cfg.Server.Tokens.MaxLinked, cfg.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(cfg.Server.Tokens.CookieMaxAge))
	if err != nil {
		log.Fatalf("initialize token management: %v", err)
	}
	web.ConfigureAuth(authStore)
	web.ConfigureContact(sharedDB)
	if pendingUpdateFailure != "" {
		if err := recordUpdateEvent(context.Background(), sharedDB, "update.failed", pendingUpdateFailure, "verification_failed"); err != nil {
			log.Printf("WARN update.failed_audit_write version=%s", pendingUpdateFailure)
		}
		if err := sendOperatorEmail(context.Background(), cfg, "update_failed", "", map[string]string{"version": pendingUpdateFailure, "error": "staged update verification failed; the previous binary remains active"}); err != nil {
			log.Printf("WARN update.failed_notification version=%s", pendingUpdateFailure)
		}
		pendingUpdateFailure = ""
	}
	mux := http.NewServeMux()
	setupRoutes(mux, securityLogger)
	web.ConfigureAuthMetrics(authStore)
	web.ConfigureOwnerTokenMetrics(authStore)
	web.ConfigureDatabaseMetrics(sharedDB)
	sharedDB.SetBusyObserver(func(wait time.Duration, caller string) {
		log.Printf("WARN db.busy wait_ms=%d caller=%s", wait.Milliseconds(), caller)
		web.ObserveDatabaseBusy(wait)
	})

	var tlsManager *ssl.Manager
	var tlsConfig *tls.Config
	var tlsDomainsMu sync.RWMutex
	var tlsDomains []string
	for _, listener := range listeners {
		if !listener.tls {
			continue
		}
		domains := configuredTLSHosts(cfg)
		if len(domains) == 0 {
			log.Fatalf("TLS requires server.fqdn or DOMAIN to be configured")
		}
		tlsDomains = append([]string(nil), domains...)
		le := cfg.Server.SSL.LetsEncrypt
		tlsManager = ssl.NewManager(ssl.Config{
			Enabled: true, CertPath: filepath.Join(configDir, "ssl"),
			Cert: cfg.Server.SSL.Cert, Key: cfg.Server.SSL.Key,
			MinVersion:  cfg.Server.SSL.MinVersion,
			LetsEncrypt: ssl.LetsEncryptConfig{Enabled: le.Enabled, Email: le.Email, Challenge: le.Challenge, Staging: le.Staging, DNSProvider: le.DNSProvider, DNSCredentials: le.DNSCredentials},
		})
		if le.Enabled && strings.EqualFold(strings.TrimSpace(le.Challenge), "dns-01") {
			if err := tlsManager.ValidateDNSProvider(); err != nil {
				log.Fatalf("validate DNS-01 provider: %v", err)
			}
		}
		tlsConfig, err = tlsManager.GetTLSConfig(domains)
		if err != nil {
			log.Fatalf("configure TLS: %v", err)
		}
		tlsConfig.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			tlsDomainsMu.RLock()
			currentDomains := append([]string(nil), tlsDomains...)
			tlsDomainsMu.RUnlock()
			if len(currentDomains) == 0 {
				return nil, errors.New("TLS requires server.fqdn or DOMAIN to be configured")
			}
			updated, err := tlsManager.GetTLSConfig(currentDomains)
			if err != nil || updated == nil {
				return updated, err
			}
			updated.GetConfigForClient = nil
			return updated, nil
		}
		break
	}
	servers := make([]*http.Server, len(listeners))
	for index, listener := range listeners {
		handler := http.Handler(mux)
		if tlsManager != nil && !listener.tls && listener.port == 80 {
			handler = tlsManager.GetHTTPHandler(handler)
		}
		servers[index] = &http.Server{
			Addr: listenAddresses[index], Handler: handler,
			ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		}
	}
	torManager := overlay.NewTorManager(configDir, dirs.Data, dirs.Logs, cfg.Server.Tor, mux)
	if err := torManager.Start(); err != nil {
		log.Printf("Tor hidden service unavailable; clearnet remains available: %v", err)
	}
	i2pManager := overlay.NewI2PManager(configDir, dirs.Data, dirs.Logs, cfg.Server.I2P, mux)
	i2pManager.Start()
	activeOverlays := &overlayManagers{tor: torManager, i2p: i2pManager}
	backgroundScheduler, err := newApplicationScheduler(sharedDB, securityManager, authStore, cfg, dirs, securityLogger, tlsManager, torManager, i2pManager, activeOverlays.current, func() []string {
		tlsDomainsMu.RLock()
		defer tlsDomainsMu.RUnlock()
		return append([]string(nil), tlsDomains...)
	})
	if err != nil {
		log.Fatalf("initialize scheduler: %v", err)
	}
	web.ConfigureSchedulerMetrics(backgroundScheduler)
	backgroundScheduler.Start()
	defer backgroundScheduler.Stop()
	web.SetOverlayStatus(func() map[string]any {
		currentTor, currentI2P := activeOverlays.current()
		return map[string]any{"tor": currentTor.Status(), "i2p": currentI2P.Status()}
	})
	reloadConfiguration := func() {
		loaded, err := config.Load(configPath)
		if err != nil {
			log.Printf("Configuration reload failed: %v", err)
			return
		}
		if err := applyI2POverrides(loaded, *i2pFlag); err != nil {
			log.Printf("Configuration reload override failed: %v", err)
			return
		}
		if *baseURLFlag != "" {
			baseURL, err := config.NormalizeBaseURL(*baseURLFlag)
			if err != nil {
				log.Printf("Configuration reload base URL override failed: %v", err)
				return
			}
			loaded.Server.BaseURL = baseURL
		}
		restartRequired := restartRequiredSettings(cfg.Server, loaded.Server)
		if len(restartRequired) > 0 {
			log.Printf("WARN config.restart_required settings=%s apply_at_next_start=true", strings.Join(restartRequired, ","))
		}
		preserveRestartRequiredSettings(cfg.Server, &loaded.Server)
		if tlsManager != nil {
			le := loaded.Server.SSL.LetsEncrypt
			if err := tlsManager.ReloadLetsEncrypt(ssl.LetsEncryptConfig{Enabled: le.Enabled, Email: le.Email, Challenge: le.Challenge, Staging: le.Staging, DNSProvider: le.DNSProvider, DNSCredentials: le.DNSCredentials}, configuredTLSHosts(loaded)); err != nil {
				log.Printf("WARN TLS configuration reload failed: %v", err)
			}
		}
		torConfigChanged := cfg == nil || cfg.Server.Tor != loaded.Server.Tor
		i2pConfigChanged := cfg == nil || cfg.Server.I2P != loaded.Server.I2P
		if err := authStore.ReloadEncryptionKey(context.Background(), loaded.Server.Security.EncryptionKey, loaded.Server.Security.EncryptionKeyVersion); err != nil {
			log.Printf("Encryption key reload failed: %v", err)
			return
		}
		configureSMTP(loaded)
		cfg = loaded
		tlsDomainsMu.Lock()
		tlsDomains = configuredTLSHosts(cfg)
		tlsDomainsMu.Unlock()
		web.SetCORSOrigins(cfg.Server.CORS.AllowedOrigins)
		web.SetServerFQDN(cfg.Server.FQDN)
		web.SetDefaultTheme(cfg.Web.UI.Theme)
		if torConfigChanged {
			if err := torManager.Close(); err != nil {
				log.Printf("Failed to stop Tor during reload: %v", err)
			}
			torManager = overlay.NewTorManager(configDir, dirs.Data, dirs.Logs, cfg.Server.Tor, mux)
			activeOverlays.replaceTor(torManager)
			if err := torManager.Start(); err != nil {
				log.Printf("Tor hidden service unavailable after reload: %v", err)
			}
		}
		if i2pConfigChanged {
			if err := i2pManager.Close(); err != nil {
				log.Printf("Failed to stop I2P during reload: %v", err)
			}
			i2pManager = overlay.NewI2PManager(configDir, dirs.Data, dirs.Logs, cfg.Server.I2P, mux)
			activeOverlays.replaceI2P(i2pManager)
			i2pManager.Start()
		}
		log.Println("Configuration reloaded")
	}

	log.Printf("Public endpoints: /types, /templates, /builder, %s/*, /api/graphql", web.APIBasePath())
	log.Printf("Listening on %s", strings.Join(listenAddresses, ", "))

	// Start server in goroutine
	errChan := make(chan error, len(servers))
	for index, listener := range listeners {
		server := servers[index]
		bound := privilegedListeners[listener.port]
		go func(server *http.Server, spec serverListener, bound net.Listener) {
			if bound == nil {
				var err error
				bound, err = net.Listen("tcp", server.Addr)
				if err != nil {
					errChan <- err
					return
				}
			}
			if spec.tls {
				errChan <- server.Serve(tls.NewListener(bound, tlsConfig))
				return
			}
			errChan <- server.Serve(bound)
		}(server, listener, bound)
	}
	reloadChan := make(chan struct{}, 1)
	watcherContext, stopWatcher := context.WithCancel(context.Background())
	go watchConfiguration(watcherContext, configDir, reloadChan)

	// Wait for shutdown signal or server error
	for {
		select {
		case err := <-errChan:
			if err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP listener stopped: %v", err)
			}
			stopWatcher()
			return
		case <-reloadChan:
			reloadConfiguration()
		case <-updateRestartRequests:
			log.Printf("INFO update.restart_requested exit_code=75")
			stopWatcher()
			backgroundScheduler.Stop()
			cancelSecurity()
			_, currentI2P := activeOverlays.current()
			currentTor, _ := activeOverlays.current()
			if err := currentI2P.Close(); err != nil {
				log.Printf("I2P shutdown failed: %v", err)
			}
			if err := currentTor.Close(); err != nil {
				log.Printf("Tor shutdown failed: %v", err)
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			for _, server := range servers {
				if err := server.Shutdown(shutdownCtx); err != nil {
					log.Printf("Graceful update shutdown failed: %v", err)
					_ = server.Close()
				}
			}
			cancel()
			os.Exit(75)
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				reloadConfiguration()
			default:
				log.Printf("Received signal %v, shutting down...", sig)
				stopWatcher()
				_, currentI2P := activeOverlays.current()
				currentTor, _ := activeOverlays.current()
				if err := currentI2P.Close(); err != nil {
					log.Printf("I2P shutdown failed: %v", err)
				}
				if err := currentTor.Close(); err != nil {
					log.Printf("Tor shutdown failed: %v", err)
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				for _, server := range servers {
					if err := server.Shutdown(shutdownCtx); err != nil {
						log.Printf("Graceful shutdown failed: %v", err)
						_ = server.Close()
					}
				}
				cancel()
				return
			}
		}
	}
}

func newApplicationScheduler(
	store *database.Store,
	securityManager *securitydata.Manager,
	authStore *auth.Store,
	configuration *config.Config,
	directories paths.Directories,
	logger *log.Logger,
	tlsManager *ssl.Manager,
	torManager *overlay.TorManager,
	i2pManager *overlay.I2PManager,
	currentOverlays func() (*overlay.TorManager, *overlay.I2PManager),
	tlsDomains func() []string,
) (*schedulerpkg.Scheduler, error) {
	schedulerConfig := configuration.Server.Scheduler
	catchUpWindow, err := time.ParseDuration(schedulerConfig.CatchUpWindow)
	if err != nil || catchUpWindow <= 0 {
		catchUpWindow = time.Hour
	}
	scheduled, err := schedulerpkg.NewWithLogger(store, schedulerConfig.Timezone, catchUpWindow, func(level, message string) {
		logger.Printf("%s %s", level, message)
	})
	if err != nil {
		return nil, err
	}
	scheduled.SetObserver(func(event schedulerpkg.ExecutionEvent) {
		if event.Running {
			return
		}
		if event.Status == "error" && (event.Task == "backup_daily" || event.Task == "backup_hourly") {
			return
		}
		level, action, success := "info", "scheduler.task_succeeded", 1
		if event.Status == "error" {
			level, action, success = "error", "scheduler.task_failed", 0
		}
		details := fmt.Sprintf(`{"duration_ms":%d,"error":%q}`, event.Duration.Milliseconds(), event.Error)
		if err := store.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO audit_log(level,category,action,target_type,target_id,details,success) VALUES(?, 'scheduler', ?, 'task', ?, ?, ?)`, level, action, event.Task, details, success)
			return err
		}); err != nil {
			logger.Printf("ERROR write scheduler audit event task=%s: %v", event.Task, err)
		}
	})
	scheduled.SetFailureHandler(func(task schedulerpkg.Task, taskErr error) {
		event := "scheduler_error"
		values := map[string]string{
			"task_name": task.Name,
			"error":     taskErr.Error(),
			"next_run":  task.NextRun.Format(time.RFC3339),
		}
		switch task.ID {
		case "backup_daily":
			event = "backup_failed"
			values["filename"] = paths.ProjectName + "-daily.tar.gz"
			values["size"] = "unknown"
		case "backup_hourly":
			event = "backup_failed"
			values["filename"] = paths.ProjectName + "-hourly.tar.gz"
			values["size"] = "unknown"
		case "ssl_renewal":
			event = "ssl_renewal_failed"
			values["fqdn"] = configuration.Server.FQDN
			values["expires_in"] = "unknown"
			values["expiry_date"] = "unknown"
			values["next_retry"] = task.NextRun.Format(time.RFC3339)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sendOperatorEmail(ctx, configuration, event, "", values); err != nil {
			logger.Printf("WARN scheduler notification failed task=%s: %v", task.ID, err)
		}
	})
	if err := applyBackupRetention(context.Background(), store, directories.Backup, configuration); err != nil {
		return nil, fmt.Errorf("initialize backup retention cleanup: %w", err)
	}
	defaults := config.DefaultConfig().Server.Scheduler.Tasks
	add := func(id string, callback func() error) error {
		taskConfig, ok := schedulerConfig.Tasks[id]
		if !ok {
			taskConfig = defaults[id]
		}
		if id == "disk_check" {
			if interval, err := time.ParseDuration(configuration.Server.Notifications.DiskLow.CheckInterval); err == nil && interval > 0 {
				taskConfig.Schedule = "@every " + interval.String()
			}
		}
		retryDelay, _ := time.ParseDuration(taskConfig.RetryDelay)
		return scheduled.AddScheduledTask(schedulerpkg.TaskConfig{
			ID: id, Name: id, Schedule: taskConfig.Schedule,
			Enabled: taskConfig.Enabled, RetryOnFail: taskConfig.RetryOnFail,
			RetryDelay: retryDelay,
		}, callback)
	}
	withTaskContext := func(run func(context.Context) error) func() error {
		return func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			return run(ctx)
		}
	}
	callbacks := map[string]func() error{
		"keyserver_retry": withTaskContext(func(ctx context.Context) error {
			if !configuration.Web.Security.PublishPGPKey {
				return nil
			}
			return publishPGPKey(ctx, store, configuration, filepath.Join(config.ConfigDirectory(), "security"))
		}),
		"healthcheck_self": func() error { return store.Read().Ping() },
		"log_rotation": withTaskContext(func(context.Context) error {
			return rotateLogFiles(directories.Logs, configuration.Server.Logging, time.Now())
		}),
		"disk_check": withTaskContext(func(context.Context) error {
			return checkDiskSpace(directories, configuration, logger, time.Now())
		}),
		"backup_daily": withTaskContext(func(ctx context.Context) error {
			err := runScheduledBackup(ctx, store, configuration, directories, false)
			if err != nil {
				_ = recordBackupEvent(ctx, store, "backup.failed", paths.ProjectName+"-daily.tar.gz", 0, configuration.Server.Backup.EncryptionPassword != "", false, err.Error())
				return err
			}
			return nil
		}),
		"backup_hourly": withTaskContext(func(ctx context.Context) error {
			err := runScheduledBackup(ctx, store, configuration, directories, true)
			if err != nil {
				_ = recordBackupEvent(ctx, store, "backup.failed", paths.ProjectName+"-hourly.tar.gz", 0, configuration.Server.Backup.EncryptionPassword != "", false, err.Error())
			}
			return err
		}),
		"security_cleanup": withTaskContext(func(ctx context.Context) error {
			return cleanupSecurityData(ctx, store, config.ConfigDirectory(), configuration.Server.Security.Reports.RetentionDays, time.Now())
		}),
		"token_cleanup": withTaskContext(func(ctx context.Context) error {
			if err := store.Write(ctx, func(tx *sql.Tx) error {
				result, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE datetime(expires_at) <= datetime('now')`)
				if err != nil {
					return err
				}
				_, err = result.RowsAffected()
				return err
			}); err != nil {
				return err
			}
			_, err := authStore.PurgeIdleOwners(ctx)
			return err
		}),
		"geoip_update": withTaskContext(func(ctx context.Context) error {
			if !configuration.Server.GeoIP.Enabled {
				return nil
			}
			if err := securityManager.UpdateGeoIP(ctx); err != nil {
				return err
			}
			reader, err := securitydata.OpenGeoIP(filepath.Join(securityManager.Dir, "geoip"))
			if err != nil {
				return err
			}
			web.ReplaceGeoIP(reader)
			return nil
		}),
		"blocklist_update": withTaskContext(func(ctx context.Context) error {
			blocklists := configuration.Server.Security.Blocklists
			if !blocklists.Enabled {
				return nil
			}
			sources := make([]securitydata.Source, 0, len(blocklists.ExtraSources))
			for _, source := range blocklists.ExtraSources {
				sources = append(sources, securitydata.Source{ID: source.ID, Type: source.Type, URL: source.URL, Enabled: source.Enabled})
			}
			_, err := securityManager.UpdateBlocklists(ctx, sources, nil)
			return err
		}),
		"cve_update": withTaskContext(func(ctx context.Context) error {
			var failures []error
			if err := securityManager.UpdateNVD(ctx, configuration.Data.CVE.APIKey); err != nil {
				failures = append(failures, err)
			}
			if configuration.Data.Trivy.Enabled {
				if _, err := securityManager.UpdateTrivy(ctx, configuration.Data.Trivy.Source); err != nil {
					failures = append(failures, err)
				}
			}
			return errors.Join(failures...)
		}),
		"update_check": withTaskContext(func(ctx context.Context) error {
			branch := normalizeUpdateBranch(configuration.Server.Update.Branch)
			if branch == "" {
				branch = "stable"
			}
			epoch, _ := strconv.ParseInt(BuildEpoch, 10, 64)
			release, available, err := updatepkg.CheckDeferred(ctx, branch, Version, epoch, configuration.Server.Update.DeferDays, time.Now().UTC())
			if err != nil || !available {
				return err
			}
			if configuration.Server.Update.AutoInstall {
				if !startedElevated || runtime.GOOS == "windows" {
					reason := "not_elevated"
					if runtime.GOOS == "windows" {
						reason = "windows_service"
					}
					if !updateEventRecorded(ctx, store, "update.install_skipped", release.TagName) {
						if err := recordUpdateEvent(ctx, store, "update.install_skipped", release.TagName, reason); err != nil {
							return err
						}
						if err := recordUpdateEvent(ctx, store, "update.available", release.TagName, ""); err != nil {
							return err
						}
						logger.Printf("WARN update.install_skipped reason=%s version=%s command='gitmessages --update yes'", reason, release.TagName)
						if err := sendOperatorEmail(ctx, configuration, "update_available", "", updateEmailValues(Version, release.TagName, branch)); err != nil {
							logger.Printf("WARN update.notification_failed version=%s", release.TagName)
						}
					}
					return nil
				}
				if updateEventRecorded(ctx, store, "update.staged", release.TagName) || updateEventRecorded(ctx, store, "update.failed", release.TagName) {
					return nil
				}
				stageDir := filepath.Join(directories.Data, "update")
				if err := updatepkg.Stage(ctx, release, stageDir, paths.ProjectName); err != nil {
					_ = os.RemoveAll(stageDir)
					_ = recordUpdateEvent(ctx, store, "update.failed", release.TagName, "staging_failed")
					_ = sendOperatorEmail(ctx, configuration, "update_failed", "", map[string]string{"version": release.TagName, "error": "release download or checksum verification failed"})
					return fmt.Errorf("stage update %s: %w", release.TagName, err)
				}
				if err := recordUpdateEvent(ctx, store, "update.staged", release.TagName, ""); err != nil {
					_ = os.RemoveAll(stageDir)
					return err
				}
				if err := sendOperatorEmail(ctx, configuration, "update_staged", "", updateEmailValues(Version, release.TagName, branch)); err != nil {
					logger.Printf("WARN update.staged_notification_failed version=%s", release.TagName)
				}
				if restartAfterTempFailure() {
					select {
					case updateRestartRequests <- struct{}{}:
					default:
					}
				}
				return nil
			}
			if !updateEventRecorded(ctx, store, "update.available", release.TagName) {
				if err := recordUpdateEvent(ctx, store, "update.available", release.TagName, ""); err != nil {
					return err
				}
				logger.Printf("WARN update.available version=%s command='gitmessages --update yes'", release.TagName)
				if err := sendOperatorEmail(ctx, configuration, "update_available", "", updateEmailValues(Version, release.TagName, branch)); err != nil {
					logger.Printf("WARN update.notification_failed version=%s", release.TagName)
				}
			}
			return nil
		}),
	}
	callbacks["ssl_renewal"] = func() error { return nil }
	if tlsManager != nil {
		callbacks["ssl_renewal"] = func() error { return tlsManager.RenewCertificate(tlsDomains()) }
	}
	if torManager != nil {
		callbacks["tor_health"] = func() error {
			currentTor, _ := currentOverlays()
			if currentTor == nil {
				return nil
			}
			status := currentTor.Status()
			if !status.Enabled {
				return nil
			}
			if !status.Running || !status.CircuitEstablished {
				return fmt.Errorf("tor is unhealthy (%s)", status.Status)
			}
			return nil
		}
	}
	if i2pManager != nil {
		callbacks["i2p_health"] = func() error {
			_, currentI2P := currentOverlays()
			if currentI2P == nil {
				return nil
			}
			status := currentI2P.Status()
			if !status.Enabled {
				return nil
			}
			if !status.Running {
				if err := currentI2P.Restart(); err != nil {
					return fmt.Errorf("I2P is unhealthy (%s) and restart failed: %w", status.Status, err)
				}
			}
			return nil
		}
	}
	for _, id := range []string{"healthcheck_self", "security_cleanup", "keyserver_retry", "token_cleanup", "geoip_update", "blocklist_update", "cve_update", "update_check", "ssl_renewal", "log_rotation", "disk_check", "backup_daily", "backup_hourly", "tor_health", "i2p_health"} {
		callback, ok := callbacks[id]
		if !ok {
			continue
		}
		if err := add(id, callback); err != nil {
			return nil, fmt.Errorf("register task %q: %w", id, err)
		}
	}
	return scheduled, nil
}

func cleanupSecurityData(ctx context.Context, store *database.Store, configDir string, retentionDays int, now time.Time) error {
	if err := store.RetireExpiredAppSecrets(ctx, now); err != nil {
		return fmt.Errorf("retire expired application secrets: %w", err)
	}
	if retentionDays <= 0 {
		retentionDays = 365
	}
	cutoff := now.UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM security_reports WHERE closed_at IS NOT NULL AND datetime(closed_at) <= datetime(?)`, cutoff)
		return err
	}); err != nil {
		return err
	}
	oldKey := filepath.Join(configDir, "security", "pgp.priv.old.asc.enc")
	info, err := os.Stat(oldKey)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired PGP key: %w", err)
	}
	if now.Sub(info.ModTime()) >= 30*24*time.Hour {
		if err := os.Remove(oldKey); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove retired PGP key: %w", err)
		}
	}
	return nil
}

func configureSMTP(configuration *config.Config) {
	mail := &configuration.Server.Notifications.Email
	smtpConfiguration := email.SMTPConfig{Host: mail.SMTP.Host, Port: mail.SMTP.Port, Username: mail.SMTP.Username, Password: mail.SMTP.Password, TLS: mail.SMTP.TLS}
	if strings.TrimSpace(smtpConfiguration.Host) == "" {
		if _, explicitlyConfigured := os.LookupEnv("SMTP_HOST"); explicitlyConfigured {
			mail.Enabled = false
			log.Printf("email.configured=false")
			log.Printf("SMTP_HOST is empty; email notifications are disabled")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		detected, err := email.DetectSMTP(ctx, configuration.Server.FQDN, smtpConfiguration)
		cancel()
		if err != nil {
			mail.Enabled = false
			log.Printf("email.configured=false")
			log.Printf("SMTP autodetection found no relay; email notifications are disabled")
			return
		}
		mail.SMTP.Host = detected.Host
		mail.SMTP.Port = detected.Port
		mail.Enabled = true
		log.Printf("email.configured=true")
		if err := config.SaveSMTPDetection(detected.Host, detected.Port); err != nil {
			log.Printf("could not save detected SMTP relay %s:%d: %v", detected.Host, detected.Port, err)
		}
		log.Printf("detected SMTP relay %s:%d", detected.Host, detected.Port)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	err := email.Probe(ctx, smtpConfiguration, 3*time.Second)
	cancel()
	if err != nil {
		mail.Enabled = false
		log.Printf("email.configured=false")
		log.Printf("configured SMTP relay %s:%d is unavailable; email notifications are disabled: %v", smtpConfiguration.Host, smtpConfiguration.Port, err)
		return
	}
	mail.Enabled = true
	log.Printf("email.configured=true")
}

func configuredTokenCookieAge(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 365 * 24 * time.Hour
	}
	if strings.HasSuffix(value, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(value, "y"))
		if err == nil && years > 0 {
			return time.Duration(years) * 365 * 24 * time.Hour
		}
		return 365 * 24 * time.Hour
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 365 * 24 * time.Hour
	}
	return duration
}

func printVersion(binary string) {
	fmt.Printf("%s %s (%s)\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\n", binary, Version, CommitID, buildDate(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func flagWasProvided(name string) bool {
	flag := "--" + name
	for _, arg := range os.Args[1:] {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func configureOutput(color, language string) error {
	switch color {
	case "auto":
	case "yes":
	case "no":
		_ = os.Setenv("NO_COLOR", "1")
	default:
		return fmt.Errorf("invalid --color value %q: expected auto, yes, or no", color)
	}
	if language != "" {
		if !i18n.IsSupported(language) {
			language = "en"
		}
		if err := os.Setenv("GITMESSAGES_LANG", language); err != nil {
			return fmt.Errorf("set output language: %w", err)
		}
	}
	return nil
}

func applyI2POverrides(current *config.Config, enabled bool) error {
	if current == nil {
		return fmt.Errorf("I2P configuration is unavailable")
	}
	if value := strings.TrimSpace(os.Getenv("I2P_ENABLED")); value != "" {
		parsed, err := config.ParseBool(value, current.Server.I2P.Enabled)
		if err != nil {
			return fmt.Errorf("invalid I2P_ENABLED value %q: expected true or false", value)
		}
		current.Server.I2P.Enabled = parsed
	}
	if flagWasProvided("i2p") {
		current.Server.I2P.Enabled = enabled
	}
	return nil
}

func securityDataDirectory(configured, dataDirectory string) (string, error) {
	value := strings.TrimSpace(configured)
	if value == "" {
		value = filepath.Join(dataDirectory, "security")
	} else {
		value = strings.ReplaceAll(value, "{data_dir}", dataDirectory)
	}
	safe, err := paths.SafeDirPath(value)
	if err != nil {
		return "", err
	}
	return filepath.Abs(safe)
}

func initializeSecurityData(ctx context.Context, manager *securitydata.Manager, configuration *config.Config, logger *log.Logger) {
	if configuration.Server.GeoIP.Enabled {
		if reader, err := securitydata.OpenGeoIP(filepath.Join(manager.Dir, "geoip")); err != nil {
			if err := refreshGeoIP(ctx, manager); err != nil {
				logger.Printf("WARN geoip initial update failed: %v", err)
			}
		} else {
			web.ReplaceGeoIP(reader)
		}
	}
	if configuration.Server.Security.Blocklists.Enabled {
		if status, _ := manager.CheckBlocklist("firehol_level1", 0); status == "error" {
			if _, err := manager.UpdateBlocklists(ctx, blocklistSources(configuration.Server.Security.Blocklists.ExtraSources), trustedBlocklistPrefixes(configuration)); err != nil {
				logger.Printf("WARN blocklist initial update failed: %v", err)
			}
		}
	}
	if status, _ := manager.Check("cve", 0); status == "error" {
		if err := manager.UpdateNVD(ctx, configuration.Data.CVE.APIKey); err != nil {
			logger.Printf("WARN NVD initial update failed: %v", err)
		}
	}
	if configuration.Data.Trivy.Enabled {
		if status, _ := manager.Check("trivy", 0); status == "error" {
			if _, err := manager.UpdateTrivy(ctx, configuration.Data.Trivy.Source); err != nil {
				logger.Printf("WARN Trivy initial update failed: %v", err)
			}
		}
	}
}

func refreshGeoIP(ctx context.Context, manager *securitydata.Manager) error {
	if err := manager.UpdateGeoIP(ctx); err != nil {
		return err
	}
	reader, err := securitydata.OpenGeoIP(filepath.Join(manager.Dir, "geoip"))
	if err != nil {
		return fmt.Errorf("open refreshed GeoIP databases: %w", err)
	}
	web.ReplaceGeoIP(reader)
	return nil
}

func blocklistSources(values []config.BlocklistSourceConfig) []securitydata.Source {
	sources := make([]securitydata.Source, 0, len(values))
	for _, source := range values {
		sources = append(sources, securitydata.Source{ID: source.ID, Type: source.Type, URL: source.URL, Enabled: source.Enabled})
	}
	return sources
}

func trustedBlocklistPrefixes(configuration *config.Config) []netip.Prefix {
	values := append([]string(nil), configuration.Server.TrustedProxies.Additional...)
	for _, entry := range configuration.Server.Security.Allowlist {
		values = append(values, entry.CIDR)
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(value)); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		if address, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
			address = address.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
		}
	}
	return prefixes
}

func setupRoutes(mux *http.ServeMux, panicLoggers ...*log.Logger) {
	cfg := config.Get().Server.RateLimit
	web.ConfigureTokenRateLimits(cfg.Strict.Requests, time.Duration(cfg.Strict.Window)*time.Second, cfg.AuthFailures.Requests, time.Duration(cfg.AuthFailures.Window)*time.Second)
	var panicLogger *log.Logger
	if len(panicLoggers) > 0 {
		panicLogger = panicLoggers[0]
	}
	web.ConfigurePanicLogger(panicLogger)
	mux.Handle("/", web.RecoverMiddleware(web.TokenCookieMiddleware(web.Handler(Version, CommitID, buildDate()))))
}

func commandHelp(args []string, commands ...string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		next := args[index+1]
		if next != "--help" && next != "-h" && next != "help" {
			continue
		}
		for _, command := range commands {
			if args[index] == "--"+command {
				return command, true
			}
		}
	}
	return "", false
}

func printCommandHelp(command string) {
	binary := filepath.Base(os.Args[0])
	switch command {
	case "shell":
		fmt.Printf("Usage: %s --shell {completions|init} [bash|zsh|fish|sh|dash|ksh|powershell|pwsh]\n", binary)
	case "service":
		fmt.Printf("Usage: %s --service {start|stop|restart|reload|status|--install|--uninstall|--disable|--help}\n", binary)
	case "maintenance":
		fmt.Printf("Usage: %s --maintenance {backup|restore|update|mode|setup|pgp|secret|security|token|data|compliance} [arguments]\nBackup modifiers: --include-ssl, --include-data\n", binary)
	case "update":
		fmt.Printf("Usage: %s --update [check|yes|branch {stable|beta|devel}]\n", binary)
	}
}

func printHelp(binary string) {
	fmt.Printf(`%s Server v%s

Usage: %s [options]

Options:
  -h, --help           Show this help message
  -v, --version        Print version information
  --port PORT          Server port (default: saved in server.yml)
  --address ADDRESS    Server address (default: [::], IPv4 fallback if unavailable)
  --baseurl PATH       URL path prefix (default: /)
  --config DIR         Configuration directory
  --data DIR           Data directory
  --cache DIR          Cache directory
  --log DIR            Log directory
  --backup DIR         Backup directory
  --pid FILE           PID file path
  --status             Check service status (for healthcheck)
  --daemon             Run as a daemon
  --debug              Enable debug logging
  --lang CODE          Language for output
  --shell ACTION [SHELL] Print shell completions, init command, or help
  --i2p                Enable the optional I2P eepsite

Service Commands:
  --service start      Start the service
  --service stop       Stop the service
  --service restart    Restart the service
  --service reload     Reload configuration
  --service status     Show service status
  --service --install  Install as system service
  --service --uninstall Remove system service

Maintenance Commands:
  --maintenance backup [file]   Back up configuration and server.db
      --include-ssl             Include SSL files
      --include-data            Include data files
  --maintenance restore [file]  Restore from backup
  --maintenance update          Check for updates
  --maintenance pgp generate|rotate|publish|export|import|delete  Manage the security PGP key
  --maintenance secret rotate installation_secret|encryption_key   Rotate a project secret
  --maintenance security list|show|export|update  Manage security report workflow
  --maintenance token list      List token metadata
  --maintenance token revoke    Revoke a token by prefix
  --maintenance data export     Export owner data by token prefix
  --maintenance data delete     Delete owner data by token prefix
  --maintenance compliance report  Print aggregate compliance status

Email Commands:
  email test [--to ADDRESS]  Send a test message to the configured recipient
  email test --dry-run       Preview and validate every email template

Environment Variables:
  PORT         Server port
  LISTEN       Server listen address
  PID_FILE     PID file path
  CONFIG_DIR   Configuration directory
  DATA_DIR     Data directory
  CACHE_DIR    Cache directory
  LOG_DIR      Log directory
  BACKUP_DIR   Backup directory

Configuration:
  Root:    /etc/apimgr/gitmessages/server.yml
  User:    ~/.config/apimgr/gitmessages/server.yml
  Docker:  /config/gitmessages/server.yml

`, binary, Version, binary)
}

func checkHealth(port string, baseURL ...string) error {
	prefix := ""
	if len(baseURL) > 0 {
		prefix = baseURL[0]
	}
	url := fmt.Sprintf("http://127.0.0.1:%s%s/api/v1/server/healthz", port, prefix)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

func watchConfiguration(ctx context.Context, configDir string, changed chan<- struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previous := configurationFingerprint(configDir)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := configurationFingerprint(configDir)
			if current == previous {
				continue
			}
			previous = current
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

func configurationFingerprint(configDir string) string {
	paths := []string{filepath.Join(configDir, "server.yml"), filepath.Join(configDir, "server.yaml"), filepath.Join(configDir, "secrets.yml"), filepath.Join(configDir, "yaml.d"), filepath.Join(configDir, "assets")}
	files := make([]string, 0)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if info.Mode().IsRegular() {
				files = append(files, path)
			}
			continue
		}
		_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !entry.IsDir() {
				if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
					files = append(files, current)
				}
			}
			return nil
		})
	}
	sort.Strings(files)
	var fingerprint strings.Builder
	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(contents)
		fmt.Fprintf(&fingerprint, "%s:%x\n", filepath.ToSlash(path), sum)
	}
	return fingerprint.String()
}

func handleServiceCommand(cmd, configDir string, dirs paths.Directories) {
	switch cmd {
	case "start":
		serviceStart()
	case "stop":
		serviceStop()
	case "restart":
		serviceRestart()
	case "reload":
		serviceReload()
	case "status":
		serviceStatus()
	case "--install":
		logServiceError(servicepkg.Install())
	case "--uninstall":
		if confirmServiceUninstall(os.Stdin, os.Stdout) {
			if err := servicepkg.Uninstall(); err != nil {
				logServiceError(err)
				return
			}
			if err := privilege.DeleteServiceAccount(dirs.Data); err != nil {
				logServiceError(err)
				return
			}
			if err := removeServiceData(dirs); err != nil {
				logServiceError(err)
				return
			}
			fmt.Println("Service uninstalled and application data removed")
		} else {
			fmt.Println("Service uninstall cancelled")
		}
	case "--disable":
		serviceDisable()
	case "--help":
		fmt.Println("Usage: gitmessages --service {start|stop|restart|reload|status|--install|--uninstall|--disable|--help}")
	default:
		fmt.Printf("Unknown service command: %s\n", cmd)
		os.Exit(1)
	}
}

func handleMaintenanceCommand(cmd, configDir, dataDir, databaseDir, logsDir, backupDir, pidPath string, configuration *config.Config, includeSSL, includeData bool) {
	args := flag.Args()

	switch cmd {
	case "security":
		if err := handleSecurityMaintenance(args, databaseDir, logsDir, configuration); err != nil {
			log.Fatalf("security maintenance failed: %v", err)
		}
	case "pgp":
		if err := handlePGPMaintenance(args, configDir, databaseDir, configuration); err != nil {
			log.Fatalf("PGP maintenance failed: %v", err)
		}
	case "secret":
		if err := handleSecretMaintenance(args, databaseDir, configDir, logsDir, configuration); err != nil {
			log.Fatalf("secret maintenance failed: %v", err)
		}
	case "token":
		handleTokenMaintenance(args, databaseDir, configuration)
	case "data", "compliance":
		store, err := database.OpenConfigured(configuration.Server.Database, filepath.Join(databaseDir, "server.db"), 5)
		if err != nil {
			log.Fatalf("open maintenance database: %v", err)
		}
		defer func() { _ = store.Close() }()
		authStore, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, configuration.Server.Tokens.MaxLinked, configuration.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(configuration.Server.Tokens.CookieMaxAge))
		if err != nil {
			log.Fatalf("initialize maintenance authorization: %v", err)
		}
		if err := authorizeTokenMaintenance(authStore); err != nil {
			log.Fatalf("authorize maintenance command: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if cmd == "data" {
			if err := runDataMaintenance(ctx, args, store, authStore); err != nil {
				log.Fatalf("data maintenance failed: %v", err)
			}
		} else if err := runComplianceReport(ctx, args, store, configuration); err != nil {
			log.Fatalf("compliance report failed: %v", err)
		}
	case "backup":
		if configuration.Server.Database.Driver == "libsql" {
			log.Fatalf("remote libSQL backups are unavailable")
		}
		if len(args) > 0 && args[0] == "test" {
			if len(args) > 2 {
				log.Fatalf("invalid backup test arguments")
			}
			backupFile := ""
			if len(args) == 2 {
				backupFile = args[1]
			}
			if backupFile == "" {
				selected, err := newestBackupFile(backupDir)
				if err != nil {
					log.Fatalf("select backup for verification: %v", err)
				}
				backupFile = selected
			}
			password := ""
			if strings.HasSuffix(strings.ToLower(backupFile), ".enc") {
				var err error
				password, err = promptBackupPassword(os.Stdin, os.Stderr)
				if err != nil {
					log.Fatalf("read backup password: %v", err)
				}
			}
			manifest, err := verifyApplicationBackup(context.Background(), backupFile, backupDir, password, configuration.Server.Restore)
			if err != nil {
				log.Fatalf("backup verification failed: %v", err)
			}
			fmt.Print(i18n.Format(os.Getenv("GITMESSAGES_LANG"), "cli.backup_verified", map[string]string{
				"backup_file": backupFile,
				"file_count":  strconv.Itoa(len(manifest.Files)),
				"encrypted":   strconv.FormatBool(manifest.Encrypted),
			}))
			return
		}
		if len(args) > 1 {
			log.Fatalf("invalid backup arguments")
		}
		backupFile := ""
		if len(args) > 0 {
			backupFile = args[0]
		}
		if backupFile == "" {
			extension := ".tar.gz"
			if configuration.Server.Backup.EncryptionPassword != "" {
				extension += ".enc"
			}
			backupFile = filepath.Join(backupDir, paths.ProjectName+"_backup_"+time.Now().UTC().Format("2006-01-02_150405")+extension)
		}
		store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
		if err != nil {
			log.Fatalf("open database for backup: %v", err)
		}
		manifest, backupErr := createApplicationBackup(context.Background(), store, configuration, configDir, dataDir, backupDir, backupFile, includeSSL, includeData)
		if backupErr != nil {
			_ = recordBackupEvent(context.Background(), store, "backup.failed", backupFile, 0, configuration.Server.Backup.EncryptionPassword != "", false, backupErr.Error())
		}
		if backupErr == nil {
			info, statErr := os.Stat(backupFile)
			if statErr != nil {
				backupErr = fmt.Errorf("inspect completed backup: %w", statErr)
			} else if err := recordBackupEvent(context.Background(), store, "backup.created", backupFile, info.Size(), manifest.Encrypted, true, "verified"); err != nil {
				log.Printf("WARN backup.audit_write_failed error=%q", err)
			}
			if backupErr == nil {
				backupErr = applyBackupRetention(context.Background(), store, backupDir, configuration)
			}
		}
		closeErr := store.Close()
		if backupErr != nil || closeErr != nil {
			log.Fatalf("backup failed: %v", errors.Join(backupErr, closeErr))
		}
		fmt.Print(i18n.Format(os.Getenv("GITMESSAGES_LANG"), "cli.backup_created", map[string]string{
			"backup_file": backupFile,
			"file_count":  strconv.Itoa(len(manifest.Files)),
			"encrypted":   strconv.FormatBool(manifest.Encrypted),
		}))
	case "restore":
		if configuration.Server.Database.Driver == "libsql" {
			log.Fatalf("remote libSQL restore is unavailable")
		}
		if len(args) != 1 {
			fmt.Println("Usage: gitmessages --maintenance restore <backup-file>")
			os.Exit(2)
		}
		databaseEmpty, err := applicationDatabaseEmpty(filepath.Join(databaseDir, "server.db"))
		if err != nil {
			log.Fatalf("inspect database before restore: %v", err)
		}
		currentUser, _ := user.Current()
		serviceUser := currentUser != nil && currentUser.Username == paths.ProjectName
		var suppliedToken string
		if !databaseEmpty && !privilege.IsElevated() && serviceUser {
			suppliedToken, err = promptOperatorToken(os.Stdin, os.Stderr)
			if err != nil {
				log.Fatalf("read operator token: %v", err)
			}
		}
		if err := authorizeApplicationRestore(configuration.Server.Token, suppliedToken, databaseEmpty, privilege.IsElevated(), serviceUser, os.Stdin, os.Stderr); err != nil {
			log.Fatalf("restore authorization failed: %v", err)
		}
		password := ""
		if strings.HasSuffix(strings.ToLower(args[0]), ".enc") {
			password, err = promptBackupPassword(os.Stdin, os.Stderr)
			if err != nil {
				log.Fatalf("read backup password: %v", err)
			}
		}
		if err := restoreApplicationBackup(context.Background(), args[0], configDir, dataDir, databaseDir, pidPath, configuration.Server.Address, configuration.Server.Port, configuration.Server.Restore, password); err != nil {
			log.Fatalf("restore failed: %v", err)
		}
		if info, err := os.Stat(args[0]); err == nil {
			if restoredStore, openErr := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1); openErr == nil {
				if auditErr := recordBackupEvent(context.Background(), restoredStore, "backup.restored", args[0], info.Size(), strings.HasSuffix(strings.ToLower(args[0]), ".enc"), true, "verified"); auditErr != nil {
					log.Printf("WARN backup.audit_write_failed error=%q", auditErr)
				}
				if closeErr := restoredStore.Close(); closeErr != nil {
					log.Printf("WARN backup.audit_close_failed error=%q", closeErr)
				}
			} else {
				log.Printf("WARN backup.audit_open_failed error=%q", openErr)
			}
		} else {
			log.Printf("WARN backup.audit_stat_failed error=%q", err)
		}
		fmt.Println(i18n.T(os.Getenv("GITMESSAGES_LANG"), "cli.restore_completed"))
	case "update":
		updateArgs := args
		if len(updateArgs) > 1 && updateArgs[0] == "update" {
			updateArgs = updateArgs[1:]
		}
		command := "yes"
		if len(updateArgs) > 0 {
			command = updateArgs[0]
		}
		if command == "branch" && len(updateArgs) > 1 {
			command += " " + updateArgs[1]
		}
		handleUpdateCommand(command, cfg)
	case "mode":
		if len(args) == 0 {
			// Show current mode
			currentCfg, err := config.Load(filepath.Join(configDir, "server.yml"))
			if err != nil {
				fmt.Printf("Current mode: production (default)\n")
			} else {
				mode := currentCfg.Server.Mode
				if mode == "" {
					mode = "production"
				}
				fmt.Printf("Current mode: %s\n", mode)
			}
		} else {
			currentUser, _ := user.Current()
			root := privilege.IsElevated()
			serviceUser := currentUser != nil && currentUser.Username == paths.ProjectName
			var suppliedToken string
			if root {
				log.Printf("WARN application mode change requested by root; review the security implications")
			} else if serviceUser {
				var err error
				suppliedToken, err = promptOperatorToken(os.Stdin, os.Stderr)
				if err != nil {
					log.Fatalf("read operator token: %v", err)
				}
			}
			if err := authorizeModeChange(configuration.Server.Token, suppliedToken, root, serviceUser); err != nil {
				log.Fatalf("authorize mode change: %v", err)
			}
			setApplicationMode(args[0], filepath.Join(configDir, "server.yml"))
		}
	case "setup":
		runSetupWizard(configDir)
	default:
		fmt.Printf("Unknown maintenance command: %s\n", cmd)
		os.Exit(1)
	}
}

func authorizeModeChange(serverToken, suppliedToken string, root, serviceUser bool) error {
	if root {
		return nil
	}
	if serviceUser && serverToken != "" && suppliedToken != "" && subtle.ConstantTimeCompare([]byte(serverToken), []byte(suppliedToken)) == 1 {
		return nil
	}
	return errors.New("mode change requires root access or a valid operator token from the service account")
}

func handleTokenMaintenance(args []string, databaseDir string, configuration *config.Config) {
	if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
		fmt.Fprintln(os.Stderr, "Usage: gitmessages --maintenance token list | revoke <prefix>")
		os.Exit(2)
	}
	store, err := database.OpenConfigured(configuration.Server.Database, filepath.Join(databaseDir, "server.db"), 5)
	if err != nil {
		log.Fatalf("open token database: %v", err)
	}
	defer func() { _ = store.Close() }()
	authStore, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, configuration.Server.Tokens.MaxLinked, configuration.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(configuration.Server.Tokens.CookieMaxAge))
	if err != nil {
		log.Fatalf("initialize token management: %v", err)
	}
	if err := authorizeTokenMaintenance(authStore); err != nil {
		log.Fatalf("authorize token maintenance: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch args[0] {
	case "list":
		tokens, err := authStore.ListAll(ctx)
		if err != nil {
			log.Fatalf("list tokens: %v", err)
		}
		fmt.Println("prefix\tkind\towner\tname\tcreated\tlast used\tstatus")
		for _, token := range tokens {
			used := "never"
			if token.LastUsedAt != nil {
				used = token.LastUsedAt.Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", token.Prefix, token.Kind, token.OwnerID, token.Name, token.CreatedAt.Format(time.RFC3339), used, token.Status)
		}
	case "revoke":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--yes") {
			fmt.Fprintln(os.Stderr, "Usage: gitmessages --maintenance token revoke <prefix> [--yes]")
			os.Exit(2)
		}
		tokens, err := authStore.ListAll(ctx)
		if err != nil {
			log.Fatalf("find token to revoke: %v", err)
		}
		var selected auth.Token
		for _, token := range tokens {
			if token.Prefix == args[1] && token.Status == "active" {
				selected = token
			}
		}
		if selected.Prefix == "" {
			log.Fatalf("revoke token: %v", auth.ErrNotFound)
		}
		var ownerTokenCount int64
		for _, token := range tokens {
			if token.OwnerID == selected.OwnerID {
				ownerTokenCount++
			}
		}
		if err := confirmTokenRevocation(os.Stdin, os.Stderr, selected, ownerTokenCount, len(args) == 3 && args[2] == "--yes"); err != nil {
			log.Fatalf("token revocation confirmation failed: %v", err)
		}
		if err := authStore.RevokeByPrefix(ctx, args[1]); err != nil {
			log.Fatalf("revoke token: %v", err)
		}
		fmt.Printf("Revoked token %s\n", args[1])
	}
}

func confirmTokenRevocation(input io.Reader, output io.Writer, token auth.Token, tokenCount int64, skipPrompt bool) error {
	if skipPrompt {
		return nil
	}
	expected, description := token.Prefix, "linked token prefix"
	if token.Kind == "primary" {
		expected = token.OwnerID
		description = fmt.Sprintf("owner id %s (%d tokens, 0 resources)", token.OwnerID, tokenCount)
	}
	if _, err := fmt.Fprintf(output, "Revoking %s. Type %s to confirm: ", description, expected); err != nil {
		return err
	}
	var confirmation string
	if _, err := fmt.Fscanln(input, &confirmation); err != nil {
		return fmt.Errorf("read token revocation confirmation: %w", err)
	}
	if confirmation != expected {
		return errors.New("token revocation confirmation did not match")
	}
	return nil
}

func authorizeTokenMaintenance(store *auth.Store) error {
	if current, err := user.Current(); err == nil && current.Username == "root" {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("root access or an interactive operator-token confirmation is required")
	}
	fmt.Fprint(os.Stderr, "Operator token: ")
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read operator token: %w", err)
	}
	identity, err := store.AuthenticateBearer(context.Background(), string(value))
	if err != nil || !identity.Admin {
		return fmt.Errorf("invalid operator token")
	}
	return nil
}

// Service management functions
func serviceStart() {
	logServiceError(servicepkg.Start())
}

func serviceStop() {
	logServiceError(servicepkg.Stop())
}

func serviceRestart() {
	logServiceError(servicepkg.Restart())
}

func serviceReload() {
	logServiceError(servicepkg.Reload())
}

func serviceStatus() {
	logServiceError(servicepkg.Status())
}

func logServiceError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "service command failed: %v\n", err)
	}
}

func serviceInstall(configDir, goos, systemdFile, launchdFile string, command func(string, ...string), output io.Writer) {
	if err := installServiceFor(goos, configDir, systemdFile, launchdFile, command, output); err != nil {
		log.Printf("Service installation failed: %v", err)
	}
}

func installServiceFor(goos, configDir, systemdFile, launchdFile string, command func(string, ...string), output io.Writer) error {
	if _, err := fmt.Fprintln(output, "Installing gitmessages service..."); err != nil {
		return fmt.Errorf("write installation status: %w", err)
	}
	switch goos {
	case "linux":
		if err := installSystemdService(configDir, systemdFile, command); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(output, "Service installed and started successfully"); err != nil {
			return fmt.Errorf("write installation status: %w", err)
		}
	case "darwin":
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>us.apimgr.gitmessages</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/gitmessages</string>
        <string>--config</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`, configDir)
		if err := os.WriteFile(launchdFile, []byte(plist), 0o644); err != nil {
			return fmt.Errorf("write launchd plist: %w", err)
		}
		command("launchctl", "load", launchdFile)
		if _, err := fmt.Fprintln(output, "Service installed and started successfully"); err != nil {
			return fmt.Errorf("write installation status: %w", err)
		}
	default:
		if _, err := fmt.Fprintf(output, "Service installation not supported on %s\n", goos); err != nil {
			return fmt.Errorf("write installation status: %w", err)
		}
	}
	return nil
}

func installSystemdService(configDir, serviceFile string, command func(string, ...string)) error {
	service := fmt.Sprintf(`[Unit]
Description=GitMessages Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/gitmessages --config %s
Restart=always
RestartSec=5
User=gitmessages
Group=gitmessages

[Install]
WantedBy=multi-user.target
`, configDir)
	if err := os.WriteFile(serviceFile, []byte(service), 0o644); err != nil {
		return fmt.Errorf("write systemd service file: %w", err)
	}
	command("systemctl", "daemon-reload")
	command("systemctl", "enable", "gitmessages")
	command("systemctl", "start", "gitmessages")
	return nil
}

func serviceUninstallWithInput(configDir string, input io.Reader, output io.Writer) {
	if !confirmServiceUninstall(input, output) {
		if _, err := fmt.Fprintln(output, "Service uninstall cancelled"); err != nil {
			log.Printf("Service uninstall cancelled; write response: %v", err)
		}
		return
	}

	dirs := paths.GetDirectories()
	dirs.Config = configDir
	serviceFile := ""
	switch runtime.GOOS {
	case "linux":
		serviceFile = "/etc/systemd/system/gitmessages.service"
	case "darwin":
		serviceFile = "/Library/LaunchDaemons/us.apimgr.gitmessages.plist"
	}
	binaryPath, err := os.Executable()
	if err != nil {
		binaryPath = "/usr/local/bin/gitmessages"
	}
	if err := uninstallService(runtime.GOOS, dirs, serviceFile, binaryPath, output, runCommand); err != nil {
		log.Printf("Service uninstallation failed: %v", err)
	}
}

func uninstallService(goos string, dirs paths.Directories, serviceFile, binaryPath string, output io.Writer, command func(string, ...string)) error {
	switch goos {
	case "linux":
		command("systemctl", "stop", "gitmessages")
		command("systemctl", "disable", "gitmessages")
		if err := removeIfPresent(serviceFile); err != nil {
			return fmt.Errorf("remove systemd unit: %w", err)
		}
		command("systemctl", "daemon-reload")
	case "darwin":
		command("launchctl", "unload", serviceFile)
		if err := removeIfPresent(serviceFile); err != nil {
			return fmt.Errorf("remove launchd plist: %w", err)
		}
	default:
		return fmt.Errorf("service uninstallation not supported on %s", goos)
	}
	if err := removeServiceData(dirs); err != nil {
		return fmt.Errorf("remove service data: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Service uninstalled. Delete binary manually: rm %s\n", binaryPath); err != nil {
		return fmt.Errorf("write uninstall confirmation: %w", err)
	}
	return nil
}

func confirmServiceUninstall(input io.Reader, output io.Writer) bool {
	if _, err := fmt.Fprintln(output, "This will delete ALL data, configs, and the system user. Continue? [y/N]"); err != nil {
		return false
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes")
}

func removeServiceData(dirs paths.Directories) error {
	seen := make(map[string]struct{})
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup} {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	if err := removeIfPresent(dirs.PID); err != nil {
		return fmt.Errorf("remove PID file %s: %w", dirs.PID, err)
	}
	return nil
}

func removeIfPresent(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func serviceDisable() {
	logServiceError(servicepkg.Disable())
}

func runCommand(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Command failed: %s %v: %v", name, args, err)
	}
}

// setApplicationMode sets the application mode in config
func setApplicationMode(mode, configPath string) {
	validModes := map[string]bool{
		"production":  true,
		"development": true,
		"debug":       true,
	}

	if !validModes[mode] {
		fmt.Printf("Invalid mode: %s\n", mode)
		fmt.Println("Valid modes: production, development, debug")
		os.Exit(1)
	}

	currentCfg, err := config.Load(configPath)
	if err != nil {
		currentCfg = config.DefaultConfig()
	}

	currentCfg.Server.Mode = mode
	if err := config.Save(); err != nil {
		log.Fatalf("Failed to save configuration: %v", err)
	}

	fmt.Printf("Application mode set to: %s\n", mode)
}

// handleUpdateCommand handles update-related commands
func handleUpdateCommand(cmd string, currentCfg *config.Config) {
	switch cmd {
	case "check":
		latest, found, err := checkForUpdate(currentCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update check failed: %v\n", err)
			return
		}
		if !found {
			fmt.Printf("Git Messages %s is current on the %s channel.\n", Version, currentCfg.Server.Update.Branch)
			return
		}
		fmt.Printf("Update available: %s → %s (%s channel)\n", Version, latest.TagName, currentCfg.Server.Update.Branch)
	case "yes":
		latest, found, err := checkForUpdate(currentCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update check failed: %v\n", err)
			return
		}
		if !found {
			fmt.Printf("Git Messages %s is already current.\n", Version)
			return
		}
		fmt.Printf("Installing Git Messages %s...\n", latest.TagName)
		if err := updatepkg.Install(context.Background(), latest); err != nil {
			fmt.Fprintf(os.Stderr, "update installation failed: %v\n", err)
		}
	default:
		branch := ""
		if strings.HasPrefix(cmd, "branch ") {
			branch = strings.TrimSpace(strings.TrimPrefix(cmd, "branch "))
		} else if cmd == "branch" {
			args := flag.Args()
			for index, argument := range args {
				if argument == "branch" && index+1 < len(args) {
					branch = args[index+1]
					break
				}
			}
		}
		branch = normalizeUpdateBranch(branch)
		if branch == "" {
			fmt.Fprintf(os.Stderr, "unknown update command: %s\nUsage: --update check|yes|branch {stable|beta|devel}\n", cmd)
			return
		}
		currentCfg.Server.Update.Branch = branch
		if err := config.SaveUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to save configuration: %v\n", err)
			return
		}
		fmt.Printf("Update branch set to: %s\n", branch)
	}
}

func stagedUpdateVersion(dataDirectory string) string {
	path := filepath.Join(dataDirectory, "update", paths.ProjectName+".json")
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "unknown"
		}
		return "unknown"
	}
	var metadata struct {
		TagName string `json:"tag_name"`
	}
	if json.Unmarshal(contents, &metadata) != nil || metadata.TagName == "" || strings.ContainsAny(metadata.TagName, `/\\`) {
		return "unknown"
	}
	return metadata.TagName
}

func stagedUpdateExists(dataDirectory string) bool {
	info, err := os.Stat(filepath.Join(dataDirectory, "update"))
	return err == nil && info.IsDir()
}

func recordUpdateEvent(ctx context.Context, store *database.Store, action, version, reason string) error {
	if store == nil || action == "" || version == "" {
		return errors.New("update event requires a store, action, and version")
	}
	level, success := "info", 1
	if action == "update.install_skipped" || action == "update.available" {
		level = "warn"
	}
	if action == "update.failed" {
		level, success = "error", 0
	}
	details := fmt.Sprintf(`{"version":%q}`, version)
	if reason != "" {
		details = fmt.Sprintf(`{"reason":%q,"version":%q}`, reason, version)
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,target_type,target_id,details,success) VALUES(?, 'update', ?, 'release', ?, ?, ?)`, level, action, version, details, success)
		return err
	})
}

func updateEventRecorded(ctx context.Context, store *database.Store, action, version string) bool {
	if store == nil {
		return false
	}
	var count int
	if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE category='update' AND action=? AND target_id=?`, action, version).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func updateEmailValues(currentVersion, newVersion, branch string) map[string]string {
	return map[string]string{"current_version": currentVersion, "new_version": newVersion, "version": newVersion, "channel": branch}
}

func restartAfterTempFailure() bool {
	switch servicepkg.DetectServiceManager() {
	case servicepkg.ServiceSystemd, servicepkg.ServiceLaunchd, servicepkg.ServiceRunit, servicepkg.ServiceS6, servicepkg.ServiceBSDRC:
		return true
	default:
		return false
	}
}

func checkForUpdate(currentCfg *config.Config) (updatepkg.Release, bool, error) {
	branch := currentCfg.Server.Update.Branch
	if branch == "" {
		branch = "stable"
	}
	epoch, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil {
		epoch = 0
	}
	return updatepkg.Check(context.Background(), branch, Version, epoch)
}

func normalizeUpdateBranch(branch string) string {
	switch strings.ToLower(strings.TrimSpace(branch)) {
	case "stable", "beta":
		return strings.ToLower(strings.TrimSpace(branch))
	case "devel", "daily", "rolling", "dev", "development":
		return "devel"
	default:
		return ""
	}
}

// runSetupWizard runs the interactive setup wizard
func runSetupWizard(configDir string) {
	fmt.Println("GitMessages Setup Wizard")
	fmt.Println("========================")
	fmt.Println()

	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}

	configPath := filepath.Join(configDir, "server.yml")
	currentCfg, err := config.Load(configPath)
	if err != nil {
		currentCfg = config.DefaultConfig()
	}

	fmt.Printf("Configuration file: %s\n", configPath)
	fmt.Printf("Current mode: %s\n", currentCfg.Server.Mode)
	fmt.Printf("Current port: %s\n", currentCfg.Server.Port)
	fmt.Println()
	fmt.Println("Setup complete. Edit the configuration file to customize settings.")
}
