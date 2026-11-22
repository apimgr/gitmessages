package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/email"
	"github.com/apimgr/gitmessages/src/metrics"
	"github.com/apimgr/gitmessages/src/mode"
	"github.com/apimgr/gitmessages/src/overlay"
	"github.com/apimgr/gitmessages/src/paths"
	servicepkg "github.com/apimgr/gitmessages/src/service"
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
	configDirFlag := flag.String("config", "", "Configuration directory")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.BoolVar(showVersion, "v", false, "Show version information")
	showStatus := flag.Bool("status", false, "Check server status (for health checks)")
	showHelp := flag.Bool("help", false, "Show help")
	flag.BoolVar(showHelp, "h", false, "Show help")
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
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup, token")
	i2pFlag := flag.Bool("i2p", false, "Enable the I2P eepsite")

	flag.Parse()
	if updateCmd == "yes" && len(flag.Args()) > 0 {
		switch flag.Args()[0] {
		case "check", "yes", "branch":
			updateCmd = optionalUpdate(flag.Args()[0])
		}
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

	// Override directories from flags
	configDir := dirs.Config
	if *configDirFlag != "" {
		configDir = *configDirFlag
	}

	// Override from environment
	if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" && *configDirFlag == "" {
		configDir = envConfig
	}

	// Ensure directories exist
	if err := paths.EnsureDirectories(dirs); err != nil {
		log.Printf("Warning: Failed to create directories: %v", err)
	}

	// Load configuration
	configPath := filepath.Join(configDir, "server.yml")
	var err error
	cfg, err = config.Load(configPath)
	if err != nil {
		log.Printf("Warning: Failed to load config: %v, using defaults", err)
		cfg = config.DefaultConfig()
	}
	if err := applyI2POverrides(cfg, *i2pFlag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	web.SetCORSOrigins(cfg.Server.CORS.AllowedOrigins)
	web.SetServerFQDN(cfg.Server.FQDN)
	web.SetDefaultTheme(cfg.Web.UI.Theme)
	var debugOverride *bool
	if flagWasProvided("debug") {
		debugOverride = debugFlag
	}
	mode.InitializeWithConfig(*modeFlag, cfg.Server.Mode, debugOverride)
	modeInfo := string(mode.Get())
	if mode.IsDebugEnabled() {
		modeInfo += " [debugging]"
	}
	log.Printf("Running in mode: %s", modeInfo)

	// Handle --status (health check)
	if *showStatus {
		checkPort := cfg.Server.Port
		if *port != "" {
			checkPort = *port
		}
		if checkPort == "" {
			checkPort = config.DefaultConfig().Server.Port
		}
		if err := checkHealth(checkPort); err != nil {
			fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
		os.Exit(0)
	}

	// Handle --mode
	if *modeFlag != "" {
		setApplicationMode(*modeFlag, configPath)
		return
	}

	// Handle --update
	if updateCmd != "" {
		handleUpdateCommand(string(updateCmd), cfg)
		return
	}

	// Handle service commands
	if *serviceCmd != "" {
		handleServiceCommand(*serviceCmd, configDir)
		return
	}

	// Handle maintenance commands
	if *maintenanceCmd != "" {
		handleMaintenanceCommand(*maintenanceCmd, configDir, dirs.Data, dirs.Logs)
		return
	}

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
	portNumber, portErr := strconv.Atoi(serverPort)
	if portErr != nil || portNumber < 1 || portNumber > 65535 {
		fmt.Fprintf(os.Stderr, "invalid server port %q: expected 1-65535\n", serverPort)
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
	listen := net.JoinHostPort(strings.Trim(serverAddress, "[]"), serverPort)

	// Log startup information
	log.Printf("gitmessages %s (commit: %s, built: %s)", Version, CommitID, buildDate())

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	// Setup HTTP server
	sharedDB, err := database.OpenSQLite(filepath.Join(dirs.Data, "db", "sqlite", "server.db"), 5)
	if err != nil {
		log.Fatalf("open application database: %v", err)
	}
	defer func() { _ = sharedDB.Close() }()
	if generated := config.ConsumeGeneratedSecrets(); len(generated) > 0 {
		if err := sharedDB.Write(context.Background(), func(tx *sql.Tx) error {
			for _, name := range generated {
				if _, err := tx.Exec(`INSERT INTO audit_log(level, category, action, details) VALUES ('info','config','config.secret_generated',?)`, fmt.Sprintf(`{"key":%q}`, name)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			log.Printf("could not record generated-secret audit entries: %v", err)
		}
	}
	authStore, err := auth.NewStore(sharedDB, cfg.Server.Token, cfg.Server.Security.EncryptionKey, cfg.Server.Tokens.MaxLinked, cfg.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(cfg.Server.Tokens.CookieMaxAge))
	if err != nil {
		log.Fatalf("initialize token management: %v", err)
	}
	web.ConfigureAuth(authStore)
	web.ConfigureContact(sharedDB)
	cleanupContext, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	go authStore.RunCleanup(cleanupContext)
	mux := http.NewServeMux()
	setupRoutes(mux)

	server := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	torManager := overlay.NewTorManager(configDir, dirs.Data, dirs.Logs, cfg.Server.Tor, mux)
	if err := torManager.Start(); err != nil {
		log.Printf("Tor hidden service unavailable; clearnet remains available: %v", err)
	}
	i2pManager := overlay.NewI2PManager(configDir, dirs.Data, dirs.Logs, cfg.Server.I2P, mux)
	i2pManager.Start()
	web.SetOverlayStatus(func() map[string]any {
		return map[string]any{"tor": torManager.Status(), "i2p": i2pManager.Status()}
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
		configureSMTP(loaded)
		cfg = loaded
		web.SetCORSOrigins(cfg.Server.CORS.AllowedOrigins)
		web.SetServerFQDN(cfg.Server.FQDN)
		web.SetDefaultTheme(cfg.Web.UI.Theme)
		if err := torManager.Close(); err != nil {
			log.Printf("Failed to stop Tor during reload: %v", err)
		}
		if err := i2pManager.Close(); err != nil {
			log.Printf("Failed to stop I2P during reload: %v", err)
		}
		torManager = overlay.NewTorManager(configDir, dirs.Data, dirs.Logs, cfg.Server.Tor, mux)
		i2pManager = overlay.NewI2PManager(configDir, dirs.Data, dirs.Logs, cfg.Server.I2P, mux)
		if err := torManager.Start(); err != nil {
			log.Printf("Tor hidden service unavailable after reload: %v", err)
		}
		i2pManager.Start()
		log.Println("Configuration reloaded")
	}

	log.Printf("Public endpoints: /types, /templates, /builder, /api/v1/*, /graphql")
	log.Printf("Listening on %s", listen)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe()
	}()
	reloadChan := make(chan struct{}, 1)
	watcherContext, stopWatcher := context.WithCancel(context.Background())
	go watchConfiguration(watcherContext, configDir, reloadChan)

	// Wait for shutdown signal or server error
	for {
		select {
		case err := <-errChan:
			log.Fatal(err)
		case <-reloadChan:
			reloadConfiguration()
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				reloadConfiguration()
			default:
				log.Printf("Received signal %v, shutting down...", sig)
				stopWatcher()
				if err := i2pManager.Close(); err != nil {
					log.Printf("I2P shutdown failed: %v", err)
				}
				if err := torManager.Close(); err != nil {
					log.Printf("Tor shutdown failed: %v", err)
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				if err := server.Shutdown(shutdownCtx); err != nil {
					log.Printf("Graceful shutdown failed: %v", err)
					_ = server.Close()
				}
				cancel()
				return
			}
		}
	}
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
		_ = os.Unsetenv("NO_COLOR")
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

func setupRoutes(mux *http.ServeMux) {
	cfg := config.Get().Server.RateLimit
	web.ConfigureTokenRateLimits(cfg.Strict.Requests, time.Duration(cfg.Strict.Window)*time.Second, cfg.AuthFailures.Requests, time.Duration(cfg.AuthFailures.Window)*time.Second)
	mux.Handle("/", web.TokenCookieMiddleware(web.Handler(Version, CommitID, buildDate())))
}

func printHelp(binary string) {
	fmt.Printf(`%s Server v%s

Usage: %s [options]

Options:
  -h, --help           Show this help message
  -v, --version        Print version information
  --port PORT          Server port (default: saved in server.yml)
  --address ADDRESS    Server address (default: from config or 0.0.0.0)
  --config DIR         Configuration directory
  --status             Check service status (for healthcheck)
  --debug              Enable debug logging
  --color MODE         Color output: auto, yes, no
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
  --maintenance backup [file]   Backup configuration
  --maintenance restore [file]  Restore from backup
  --maintenance update          Check for updates
  --maintenance token list      List token metadata
  --maintenance token revoke    Revoke a token by prefix

Environment Variables:
  PORT         Server port
  ADDRESS      Server address
  CONFIG_DIR   Configuration directory

Configuration:
  Root:    /etc/apimgr/gitmessages/server.yml
  User:    ~/.config/apimgr/gitmessages/server.yml
  Docker:  /config/gitmessages/server.yml

`, binary, Version, binary)
}

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s/api/v1/server/healthz", port)
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

func handleServiceCommand(cmd, configDir string) {
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
		serviceInstall(configDir, runtime.GOOS, "/etc/systemd/system/gitmessages.service", "/Library/LaunchDaemons/us.apimgr.gitmessages.plist", runCommand, os.Stdout)
	case "--uninstall":
		serviceUninstall(configDir)
	case "--disable":
		serviceDisable()
	default:
		fmt.Printf("Unknown service command: %s\n", cmd)
		os.Exit(1)
	}
}

func handleMaintenanceCommand(cmd, configDir, dataDir, logsDir string) {
	args := flag.Args()

	switch cmd {
	case "token":
		handleTokenMaintenance(args, dataDir)
	case "backup":
		backupFile := ""
		if len(args) > 0 {
			backupFile = args[0]
		} else {
			backupDir := paths.GetBackupDir()
			if err := os.MkdirAll(backupDir, 0755); err != nil {
				log.Fatalf("Failed to create backup directory: %v", err)
			}
			timestamp := time.Now().Format("20060102-150405")
			backupFile = filepath.Join(backupDir, fmt.Sprintf("gitmessages-backup-%s.tar.gz", timestamp))
		}
		maintenanceBackup(configDir, backupFile)
	case "restore":
		if len(args) == 0 {
			fmt.Println("Usage: gitmessages --maintenance restore <backup-file>")
			os.Exit(1)
		}
		maintenanceRestore(args[0], configDir)
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
			setApplicationMode(args[0], filepath.Join(configDir, "server.yml"))
		}
	case "setup":
		runSetupWizard(configDir)
	default:
		fmt.Printf("Unknown maintenance command: %s\n", cmd)
		os.Exit(1)
	}
}

func handleTokenMaintenance(args []string, dataDir string) {
	if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
		fmt.Fprintln(os.Stderr, "Usage: gitmessages --maintenance token list | revoke <prefix>")
		os.Exit(2)
	}
	store, err := database.OpenSQLite(filepath.Join(dataDir, "db", "sqlite", "server.db"), 5)
	if err != nil {
		log.Fatalf("open token database: %v", err)
	}
	defer func() { _ = store.Close() }()
	authStore, err := auth.NewStore(store, cfg.Server.Token, cfg.Server.Security.EncryptionKey, cfg.Server.Tokens.MaxLinked, cfg.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(cfg.Server.Tokens.CookieMaxAge))
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
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: gitmessages --maintenance token revoke <prefix>")
			os.Exit(2)
		}
		if err := authStore.RevokeByPrefix(ctx, args[1]); err != nil {
			log.Fatalf("revoke token: %v", err)
		}
		fmt.Printf("Revoked token %s\n", args[1])
	}
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
		if err := os.WriteFile(launchdFile, []byte(plist), 0644); err != nil {
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
	if err := os.WriteFile(serviceFile, []byte(service), 0644); err != nil {
		return fmt.Errorf("write systemd service file: %w", err)
	}
	command("systemctl", "daemon-reload")
	command("systemctl", "enable", "gitmessages")
	command("systemctl", "start", "gitmessages")
	return nil
}

func serviceUninstall(configDir string) {
	serviceUninstallWithInput(configDir, os.Stdin, os.Stdout)
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

func maintenanceBackup(configDir, backupFile string) {
	if err := createConfigBackup(configDir, backupFile); err != nil {
		log.Printf("Backup failed: %v", err)
		os.Exit(1)
	}
	fmt.Printf("Backup created successfully: %s\n", backupFile)
}

func maintenanceRestore(backupFile, configDir string) {
	if err := restoreConfigBackup(backupFile, configDir); err != nil {
		log.Printf("Restore failed: %v", err)
		os.Exit(1)
	}
	fmt.Println("Configuration restored successfully")
}

func createConfigBackup(configDir, backupFile string) error {
	info, err := os.Stat(configDir)
	if err != nil {
		return fmt.Errorf("inspect configuration directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("configuration path is not a directory")
	}
	if err := os.MkdirAll(filepath.Dir(backupFile), 0700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(backupFile), ".gitmessages-backup-*")
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove temporary backup file %s: %v", temporaryPath, err)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure backup file: %w", err)
	}
	compressed := gzip.NewWriter(temporary)
	archive := tar.NewWriter(compressed)
	base := filepath.Base(configDir)
	err = filepath.WalkDir(configDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("unsupported file type in config backup: %s", path)
		}
		relative, err := filepath.Rel(configDir, path)
		if err != nil {
			return err
		}
		name := base
		if relative != "." {
			name = filepath.ToSlash(filepath.Join(base, relative))
		}
		mode := int64(0600)
		if info.IsDir() {
			mode = 0700
			name += "/"
		}
		header := &tar.Header{Name: name, Mode: mode, Size: 0, Typeflag: tar.TypeDir}
		if !info.IsDir() {
			header.Typeflag = tar.TypeReg
			header.Size = info.Size()
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(archive, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err == nil {
		err = archive.Close()
	}
	if closeErr := compressed.Close(); err == nil {
		err = closeErr
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write backup archive: %w", err)
	}
	if err := os.Rename(temporaryPath, backupFile); err != nil {
		return fmt.Errorf("publish backup archive: %w", err)
	}
	return nil
}

func restoreConfigBackup(backupFile, configDir string) error {
	info, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("inspect backup file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<30 {
		return fmt.Errorf("backup must be a regular file no larger than 1 GiB")
	}
	parent := filepath.Dir(configDir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create configuration parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".gitmessages-restore-*")
	if err != nil {
		return fmt.Errorf("create restore staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("open backup file: %w", err)
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read backup compression: %w", err)
	}
	defer func() { _ = compressed.Close() }()
	archive := tar.NewReader(compressed)
	root := filepath.Base(configDir)
	var expanded int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read backup entry: %w", err)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in backup: %q", header.Name)
		}
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return fmt.Errorf("unexpected top-level path in backup: %q", header.Name)
		}
		target := filepath.Join(stage, clean)
		relative, err := filepath.Rel(stage, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in backup: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return fmt.Errorf("create restored directory: %w", err)
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > (1<<30)-expanded {
				return fmt.Errorf("expanded backup exceeds 1 GiB")
			}
			expanded += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return fmt.Errorf("create restored file directory: %w", err)
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return fmt.Errorf("create restored file: %w", err)
			}
			_, copyErr := io.CopyN(output, archive, header.Size)
			closeErr := output.Close()
			if copyErr != nil {
				return fmt.Errorf("write restored file: %w", copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close restored file: %w", closeErr)
			}
		default:
			return fmt.Errorf("unsupported archive entry type in %q", header.Name)
		}
	}
	restored := filepath.Join(stage, root)
	if _, err := os.Stat(restored); err != nil {
		return fmt.Errorf("backup does not contain the expected config directory")
	}
	oldRoot, err := os.MkdirTemp(parent, ".gitmessages-restore-old-*")
	if err != nil {
		return fmt.Errorf("prepare current configuration backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(oldRoot) }()
	old := filepath.Join(oldRoot, filepath.Base(configDir))
	if err := os.Rename(configDir, old); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("move current configuration aside: %w", err)
	}
	if err := os.Rename(restored, configDir); err != nil {
		_ = os.Rename(old, configDir)
		return fmt.Errorf("install restored configuration: %w", err)
	}
	return nil
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
	if err := os.MkdirAll(configDir, 0755); err != nil {
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
