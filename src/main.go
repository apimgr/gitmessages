package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/apimgr/gitmessages/src/admin"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/messages"
	"github.com/apimgr/gitmessages/src/paths"
)

// Version information (set by build flags)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

const projectName = "gitmessages"

var msgManager *messages.Manager
var cfg *config.Config

func init() {
	log.SetPrefix("gitmessages: ")
	log.SetFlags(log.Lshortfile)
}

func main() {
	// Get default directories
	dirs := paths.GetDirectories()

	// Flags
	port := flag.String("port", "", "Server port (overrides config)")
	address := flag.String("address", "", "Server address (overrides config)")
	configDirFlag := flag.String("config", "", "Configuration directory")
	showVersion := flag.Bool("version", false, "Show version information")
	showStatus := flag.Bool("status", false, "Check server status (for health checks)")
	showHelp := flag.Bool("help", false, "Show help")

	// Mode and update flags
	modeFlag := flag.String("mode", "", "Application mode: production, development")
	updateCmd := flag.String("update", "", "Update commands: check, yes, branch {stable|beta|daily}")

	// Service commands
	serviceCmd := flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable")

	// Maintenance commands
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup")

	flag.Parse()

	// Handle --help
	if *showHelp {
		printHelp()
		return
	}

	// Handle --version
	if *showVersion {
		fmt.Println(Version)
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

	// Handle --status (health check)
	if *showStatus {
		checkPort := cfg.Server.Port
		if checkPort == "" {
			checkPort = "8080"
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
	if *updateCmd != "" {
		handleUpdateCommand(*updateCmd, cfg)
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

	// Determine port (flag > env > config > default)
	serverPort := cfg.Server.Port
	if *port != "" {
		serverPort = *port
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		serverPort = envPort
	}
	if serverPort == "" {
		serverPort = "8080"
	}

	// Determine address (flag > env > config > default)
	serverAddress := cfg.Server.Address
	if *address != "" {
		serverAddress = *address
	} else if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		serverAddress = envAddr
	}
	if serverAddress == "" {
		serverAddress = "0.0.0.0"
	}

	// Build listen address
	listen := fmt.Sprintf("%s:%s", serverAddress, serverPort)
	if serverAddress == "0.0.0.0" || serverAddress == "::" {
		listen = ":" + serverPort
	}

	// Log startup information
	log.Printf("gitmessages %s (commit: %s, built: %s)", Version, Commit, BuildDate)

	// Load messages
	log.Println("Loading git commit messages...")
	msgManager, err = messages.New()
	if err != nil {
		log.Fatalf("Failed to load messages: %v", err)
	}
	log.Printf("Loaded %d messages", msgManager.Count())

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	// Setup HTTP server
	mux := http.NewServeMux()
	setupRoutes(mux)

	// Setup admin handler
	sessionTimeout := 3600
	if cfg.Server.Session.Timeout > 0 {
		sessionTimeout = cfg.Server.Session.Timeout
	}
	adminHandler := admin.NewHandler(
		cfg.Server.Admin.Username,
		cfg.Server.Admin.Password,
		cfg.Server.Admin.APIToken,
		sessionTimeout,
		false, // SSL enabled
		Version,
		Commit,
		BuildDate,
	)
	adminHandler.RegisterRoutes(mux)

	server := &http.Server{
		Addr:         listen,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Log endpoints
	log.Printf("")
	log.Printf("API Endpoints:")
	log.Printf("  GET /                        - Home page")
	log.Printf("  GET /api/v1/random           - Random message (JSON)")
	log.Printf("  GET /api/v1/random.txt       - Random message (text)")
	log.Printf("  GET /api/v1/messages         - All messages (JSON)")
	log.Printf("  GET /api/v1/stats            - Statistics")
	log.Printf("  POST /api/v1/reset           - Reset cycle")
	log.Printf("")
	log.Printf("Special Files:")
	log.Printf("  GET /robots.txt              - Robots file")
	log.Printf("  GET /security.txt            - Security contact")
	log.Printf("  GET /manifest.json           - PWA manifest")
	log.Printf("")
	log.Printf("Listening on %s", listen)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe()
	}()

	// Wait for shutdown signal or server error
	for {
		select {
		case err := <-errChan:
			log.Fatal(err)
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				if _, err := config.Load(configPath); err != nil {
					log.Printf("Failed to reload config: %v", err)
				} else {
					log.Println("Configuration reloaded")
				}
			default:
				log.Printf("Received signal %v, shutting down...", sig)
				os.Exit(0)
			}
		}
	}
}

func setupRoutes(mux *http.ServeMux) {
	// Health checks
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/api/v1/healthz", handleHealthz)
	mux.HandleFunc("/api/v1/healthz.txt", handleHealthzText)

	// Special files
	mux.HandleFunc("/robots.txt", handleRobotsTxt)
	mux.HandleFunc("/security.txt", handleSecurityTxt)
	mux.HandleFunc("/.well-known/security.txt", handleSecurityTxt)
	mux.HandleFunc("/manifest.json", handleManifest)
	mux.HandleFunc("/sw.js", handleServiceWorker)

	// API endpoints
	mux.HandleFunc("/api/v1/", handleAPIInfo)
	mux.HandleFunc("/api/v1/random", handleRandom)
	mux.HandleFunc("/api/v1/random.txt", handleRandomText)
	mux.HandleFunc("/api/v1/messages", handleMessages)
	mux.HandleFunc("/api/v1/messages.txt", handleMessagesText)
	mux.HandleFunc("/api/v1/stats", handleStats)
	mux.HandleFunc("/api/v1/stats.txt", handleStatsText)
	mux.HandleFunc("/api/v1/reset", handleReset)

	// Home page
	mux.HandleFunc("/", handleHome)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corsOrigin := "*"
		if cfg != nil && cfg.WebSecurity.CORS != "" {
			corsOrigin = cfg.WebSecurity.CORS
		}

		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handlers

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>GitMessages API</title></head>
<body>
<h1>GitMessages API v%s</h1>
<p>Random git commit messages API</p>
<ul>
<li><a href="/api/v1/random">GET /api/v1/random</a> - Random message (JSON)</li>
<li><a href="/api/v1/random.txt">GET /api/v1/random.txt</a> - Random message (text)</li>
<li><a href="/api/v1/messages">GET /api/v1/messages</a> - All messages</li>
<li><a href="/api/v1/stats">GET /api/v1/stats</a> - Statistics</li>
</ul>
</body>
</html>`, Version)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"version": Version,
	})
}

func handleHealthzText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "OK")
}

func handleAPIInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":    "GitMessages API",
		"version": Version,
		"endpoints": map[string]string{
			"random":   "/api/v1/random",
			"messages": "/api/v1/messages",
			"stats":    "/api/v1/stats",
			"reset":    "/api/v1/reset (POST)",
		},
	})
}

func handleRandom(w http.ResponseWriter, r *http.Request) {
	msg, err := msgManager.GetRandom()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	stats := msgManager.Stats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"message": msg,
		},
		"meta": stats,
	})
}

func handleRandomText(w http.ResponseWriter, r *http.Request) {
	msg, err := msgManager.GetRandom()
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, msg)
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	data, err := msgManager.GetAllJSON()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func handleMessagesText(w http.ResponseWriter, r *http.Request) {
	msgs := msgManager.GetAll()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, msg := range msgs {
		fmt.Fprintln(w, msg)
	}
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	stats := msgManager.Stats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    stats,
	})
}

func handleStatsText(w http.ResponseWriter, r *http.Request) {
	stats := msgManager.Stats()
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "Cycle: %d\n", stats["cycle"])
	fmt.Fprintf(w, "Total Messages: %d\n", stats["total_messages"])
	fmt.Fprintf(w, "Used in Cycle: %d\n", stats["used_in_cycle"])
	fmt.Fprintf(w, "Remaining: %d\n", stats["remaining_in_cycle"])
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Method not allowed, use POST",
		})
		return
	}

	msgManager.ResetCycle()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Cycle reset successfully",
	})
}

func handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "User-agent: *")
	if cfg != nil {
		for _, path := range cfg.WebRobots.Allow {
			fmt.Fprintf(w, "Allow: %s\n", path)
		}
		for _, path := range cfg.WebRobots.Deny {
			fmt.Fprintf(w, "Disallow: %s\n", path)
		}
	} else {
		fmt.Fprintln(w, "Allow: /")
	}
}

func handleSecurityTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	admin := "security@apimgr.us"
	if cfg != nil && cfg.WebSecurity.Admin != "" {
		admin = cfg.WebSecurity.Admin
	}
	fmt.Fprintf(w, "Contact: mailto:%s\n", admin)
	fmt.Fprintln(w, "Expires: 2026-12-31T23:59:59.000Z")
	fmt.Fprintln(w, "Preferred-Languages: en")
	fmt.Fprintln(w, "Canonical: https://gitmessages.apimgr.us/.well-known/security.txt")
}

func handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	fmt.Fprint(w, `{
  "name": "GitMessages API",
  "short_name": "GitMessages",
  "description": "Random git commit messages API",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#1a1a1a",
  "theme_color": "#0066cc",
  "icons": []
}`)
}

func handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	fmt.Fprint(w, `// GitMessages Service Worker
const CACHE_NAME = 'gitmessages-v1';
self.addEventListener('fetch', function(event) {
  event.respondWith(fetch(event.request));
});
`)
}

func printHelp() {
	fmt.Printf(`GitMessages Server v%s

Usage: gitmessages [options]

Options:
  --port PORT          Server port (default: from config or 8080)
  --address ADDRESS    Server address (default: from config or 0.0.0.0)
  --config DIR         Configuration directory
  --version            Print version information
  --status             Check service status (for healthcheck)
  --help               Show this help message

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

Environment Variables:
  PORT         Server port
  ADDRESS      Server address
  CONFIG_DIR   Configuration directory

Configuration:
  Root:    /etc/apimgr/gitmessages/server.yml
  User:    ~/.config/apimgr/gitmessages/server.yml
  Docker:  /config/server.yml

`, Version)
}

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s/healthz", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
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
		serviceInstall(configDir)
	case "--uninstall":
		serviceUninstall()
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
		maintenanceUpdate()
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

// Service management functions
func serviceStart() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "start", "gitmessages")
	case "darwin":
		runCommand("launchctl", "start", "us.apimgr.gitmessages")
	default:
		fmt.Printf("Service management not supported on %s\n", runtime.GOOS)
	}
}

func serviceStop() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "stop", "gitmessages")
	case "darwin":
		runCommand("launchctl", "stop", "us.apimgr.gitmessages")
	default:
		fmt.Printf("Service management not supported on %s\n", runtime.GOOS)
	}
}

func serviceRestart() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "restart", "gitmessages")
	case "darwin":
		runCommand("launchctl", "stop", "us.apimgr.gitmessages")
		runCommand("launchctl", "start", "us.apimgr.gitmessages")
	default:
		fmt.Printf("Service management not supported on %s\n", runtime.GOOS)
	}
}

func serviceReload() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "reload", "gitmessages")
	case "darwin":
		runCommand("pkill", "-HUP", "gitmessages")
	default:
		fmt.Printf("Service management not supported on %s\n", runtime.GOOS)
	}
}

func serviceStatus() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "status", "gitmessages")
	case "darwin":
		runCommand("launchctl", "list", "us.apimgr.gitmessages")
	default:
		fmt.Printf("Service management not supported on %s\n", runtime.GOOS)
	}
}

func serviceInstall(configDir string) {
	fmt.Println("Installing gitmessages service...")
	switch runtime.GOOS {
	case "linux":
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
		if err := os.WriteFile("/etc/systemd/system/gitmessages.service", []byte(service), 0644); err != nil {
			log.Fatalf("Failed to write systemd service file: %v", err)
		}
		runCommand("systemctl", "daemon-reload")
		runCommand("systemctl", "enable", "gitmessages")
		runCommand("systemctl", "start", "gitmessages")
		fmt.Println("Service installed and started successfully")
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
		if err := os.WriteFile("/Library/LaunchDaemons/us.apimgr.gitmessages.plist", []byte(plist), 0644); err != nil {
			log.Fatalf("Failed to write launchd plist: %v", err)
		}
		runCommand("launchctl", "load", "/Library/LaunchDaemons/us.apimgr.gitmessages.plist")
		fmt.Println("Service installed and started successfully")
	default:
		fmt.Printf("Service installation not supported on %s\n", runtime.GOOS)
	}
}

func serviceUninstall() {
	fmt.Println("Uninstalling gitmessages service...")
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "stop", "gitmessages")
		runCommand("systemctl", "disable", "gitmessages")
		os.Remove("/etc/systemd/system/gitmessages.service")
		runCommand("systemctl", "daemon-reload")
	case "darwin":
		runCommand("launchctl", "unload", "/Library/LaunchDaemons/us.apimgr.gitmessages.plist")
		os.Remove("/Library/LaunchDaemons/us.apimgr.gitmessages.plist")
	default:
		fmt.Printf("Service uninstallation not supported on %s\n", runtime.GOOS)
	}
}

func serviceDisable() {
	switch runtime.GOOS {
	case "linux":
		runCommand("systemctl", "disable", "gitmessages")
	case "darwin":
		runCommand("launchctl", "unload", "/Library/LaunchDaemons/us.apimgr.gitmessages.plist")
	default:
		fmt.Printf("Service disable not supported on %s\n", runtime.GOOS)
	}
}

func maintenanceBackup(configDir, backupFile string) {
	fmt.Printf("Creating backup: %s\n", backupFile)
	cmd := exec.Command("tar", "-czf", backupFile, "-C", filepath.Dir(configDir), filepath.Base(configDir))
	if err := cmd.Run(); err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
	fmt.Printf("Backup created successfully: %s\n", backupFile)
}

func maintenanceRestore(backupFile, configDir string) {
	fmt.Printf("Restoring from backup: %s\n", backupFile)
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		log.Fatalf("Backup file not found: %s", backupFile)
	}
	cmd := exec.Command("tar", "-xzf", backupFile, "-C", "/")
	if err := cmd.Run(); err != nil {
		log.Fatalf("Restore failed: %v", err)
	}
	fmt.Println("Restore completed successfully")
}

func maintenanceUpdate() {
	fmt.Println("Checking for updates...")
	fmt.Printf("Current version: %s\n", Version)
	fmt.Println("Update feature not yet implemented")
	fmt.Println("Visit https://github.com/apimgr/gitmessages/releases for the latest version")
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
		fmt.Println("Checking for updates...")
		fmt.Printf("Current version: %s\n", Version)
		fmt.Printf("Update branch: %s\n", currentCfg.Server.UpdateBranch)
		fmt.Println("Update feature not yet implemented")
	case "yes":
		fmt.Println("Performing update...")
		fmt.Println("Update feature not yet implemented")
	default:
		if len(cmd) > 7 && cmd[:7] == "branch " {
			branch := cmd[7:]
			validBranches := map[string]bool{
				"stable": true,
				"beta":   true,
				"daily":  true,
			}
			if !validBranches[branch] {
				fmt.Printf("Invalid branch: %s\n", branch)
				fmt.Println("Valid branches: stable, beta, daily")
				os.Exit(1)
			}
			currentCfg.Server.UpdateBranch = branch
			if err := config.Save(); err != nil {
				log.Fatalf("Failed to save configuration: %v", err)
			}
			fmt.Printf("Update branch set to: %s\n", branch)
		} else {
			fmt.Printf("Unknown update command: %s\n", cmd)
			fmt.Println("Usage: --update check|yes|branch {stable|beta|daily}")
			os.Exit(1)
		}
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
