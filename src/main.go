package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/server"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// CLI flags
	showVersion := flag.Bool("version", false, "Show version information")
	showStatus := flag.Bool("status", false, "Show server status")
	showHelp := flag.Bool("help", false, "Show help message")
	portFlag := flag.String("port", "", "Set port(s) (e.g., 8080 or \"8080,8443\")")
	dataDir := flag.String("data", "", "Set data directory")
	_ = flag.String("config", "", "Set config directory")
	address := flag.String("address", "0.0.0.0", "Set listen address")
	_ = flag.Bool("dev", false, "Run in development mode")

	flag.Parse()

	// Handle flags
	if *showHelp {
		printHelp()
		return
	}

	if *showVersion {
		printVersion()
		return
	}

	if *showStatus {
		checkStatus()
		return
	}

	// Determine data directory
	var dir string
	if *dataDir != "" {
		dir = *dataDir
	} else {
		// Use current directory
		dir = "./data"
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalf("Failed to get absolute path: %v", err)
	}

	// Determine port
	port := *portFlag
	if port == "" {
		// Generate random port between 64000-64999
		rand.Seed(time.Now().UnixNano())
		port = fmt.Sprintf("%d", 64000+rand.Intn(1000))
		log.Printf("No port specified, using random port: %s", port)
	}

	// Initialize database
	log.Printf("Initializing database in: %s", absDir)
	db, err := database.New(absDir)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create server
	srv, err := server.New(db, *address, port)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Start server
	log.Printf("Starting gitmessages v%s", Version)
	log.Printf("Server listening on http://%s:%s", *address, port)
	log.Printf("API endpoints:")
	log.Printf("  - GET  /api/v1/random       - Get random message (JSON)")
	log.Printf("  - GET  /api/v1/random.txt   - Get random message (text)")
	log.Printf("  - GET  /api/v1/stats        - Get usage statistics")
	log.Printf("  - POST /api/v1/reset        - Reset cycle")
	log.Printf("  - GET  /healthz             - Health check")

	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func printHelp() {
	fmt.Println(`Usage: gitmessages [OPTIONS]

Options:
  --help            Show this help message
  --version         Show version information
  --status          Show server status and exit with code
  --port PORT       Set port(s) (e.g., 8080 or "8080,8443")
  --data DIR        Set data directory (must be directory)
  --config DIR      Set config directory (must be directory)
  --address ADDR    Set listen address (e.g., 0.0.0.0)
  --dev             Run in development mode`)
}

func printVersion() {
	fmt.Printf(`gitmessages version %s
Built: %s
Commit: %s
`, Version, BuildDate, Commit)
}

func checkStatus() {
	// TODO: Implement actual status check
	fmt.Println("❌ Server: Not running")
	os.Exit(1)
}
