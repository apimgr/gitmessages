package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/overlay"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/apimgr/gitmessages/src/securitydata"
)

func TestConfigureOutputFallsBackToEnglish(t *testing.T) {
	previous, existed := os.LookupEnv("GITMESSAGES_LANG")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("GITMESSAGES_LANG", previous)
		} else {
			_ = os.Unsetenv("GITMESSAGES_LANG")
		}
	})
	if !i18n.IsSupported("en") {
		t.Fatal("English locale is unavailable")
	}
	if err := configureOutput("auto", "unsupported"); err != nil {
		t.Fatalf("unsupported language should fall back silently: %v", err)
	}
	if got := os.Getenv("GITMESSAGES_LANG"); got != "en" {
		t.Fatalf("unsupported language fallback = %q, want en", got)
	}
}

func TestConfigureOutputRejectsUnknownColor(t *testing.T) {
	if err := configureOutput("invalid", "en"); err == nil {
		t.Fatal("unsupported color mode was accepted")
	}
}

func TestRestartRequiredSettingsAndPreservation(t *testing.T) {
	current := config.DefaultConfig().Server
	incoming := current
	incoming.SSL.LetsEncrypt.Email = "admin@example.test"
	if changed := restartRequiredSettings(current, incoming); len(changed) != 0 {
		t.Fatalf("Let's Encrypt-only change requires restart: %v", changed)
	}
	incoming.Port = "6443"
	incoming.Address = "127.0.0.1"
	incoming.SSL.Enabled = true
	incoming.SSL.Cert = "/new/cert.pem"
	incoming.SSL.Key = "/new/key.pem"
	incoming.SSL.MinVersion = "TLS1.3"
	incoming.Daemonize = true
	incoming.PIDFile = false
	incoming.Database.Driver = "libsql"
	want := []string{"server.port", "server.address", "server.ssl.enabled", "server.ssl.cert", "server.ssl.key", "server.ssl.min_version", "server.daemonize", "server.pidfile", "server.database"}
	changed := restartRequiredSettings(current, incoming)
	if strings.Join(changed, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("restartRequiredSettings() = %v, want %v", changed, want)
	}
	preserveRestartRequiredSettings(current, &incoming)
	if len(restartRequiredSettings(current, incoming)) != 0 || incoming.SSL.LetsEncrypt.Email != "admin@example.test" {
		t.Fatalf("restart-required settings were not preserved while ACME settings remained reloadable: %+v", incoming)
	}
}

func TestStagedUpdateVersionAndPresence(t *testing.T) {
	dataDirectory := t.TempDir()
	if stagedUpdateExists(dataDirectory) {
		t.Fatal("missing update directory reported as staged")
	}
	stageDirectory := filepath.Join(dataDirectory, "update")
	if err := os.Mkdir(stageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if !stagedUpdateExists(dataDirectory) {
		t.Fatal("existing update directory was not detected")
	}
	if err := os.WriteFile(filepath.Join(stageDirectory, paths.ProjectName+".json"), []byte(`{"tag_name":"v1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stagedUpdateVersion(dataDirectory); got != "v1.2.3" {
		t.Fatalf("stagedUpdateVersion() = %q, want v1.2.3", got)
	}
	if err := os.WriteFile(filepath.Join(stageDirectory, paths.ProjectName+".json"), []byte(`{"tag_name":"../../binary"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stagedUpdateVersion(dataDirectory); got != "unknown" {
		t.Fatalf("unsafe stagedUpdateVersion() = %q, want unknown", got)
	}
}

func TestCommandHelpRecognizesBothHelpForms(t *testing.T) {
	for _, next := range []string{"--help", "-h", "help"} {
		command, ok := commandHelp([]string{"--maintenance", next}, "shell", "service", "maintenance", "update")
		if !ok || command != "maintenance" {
			t.Errorf("commandHelp(%q) = %q, %t; want maintenance, true", next, command, ok)
		}
	}
	if command, ok := commandHelp([]string{"--help"}, "shell", "service", "maintenance", "update"); ok {
		t.Fatalf("plain --help was routed to command help %q", command)
	}
}

func TestPrintCommandHelp(t *testing.T) {
	for _, command := range []string{"shell", "service", "maintenance", "update", "unknown"} {
		printCommandHelp(command)
	}
}

func TestHandleServiceCommandHelp(t *testing.T) {
	handleServiceCommand("--help", maintenanceTestDir(t), paths.GetDirectories())
}

func TestCommandHelpersHandleFailureAndUnprivilegedStartup(t *testing.T) {
	previousLogOutput := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })
	runCommand("gitmessages-command-that-does-not-exist")
	if err := dropToServiceAccount(startupIdentity{}, maintenanceTestDir(t)); err != nil {
		t.Fatalf("unprivileged startup attempted a privilege drop: %v", err)
	}
	printVersion(paths.ProjectName)
}

func TestRefreshGeoIPReturnsCanceledUpdateError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := securitydata.New(filepath.Join(maintenanceTestDir(t), "security"))
	if err := refreshGeoIP(ctx, manager); err == nil {
		t.Fatal("refreshGeoIP() succeeded with a canceled context")
	}
}

func TestMaintenanceModeDisplaysConfiguredAndDefaultModes(t *testing.T) {
	previousFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = previousFlags })

	for _, test := range []struct {
		name string
		mode string
	}{
		{name: "missing config uses production default"},
		{name: "configured mode", mode: "development"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configDir := maintenanceTestDir(t)
			if test.mode != "" {
				if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  mode: "+test.mode+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			flag.CommandLine = flag.NewFlagSet("gitmessages-test", flag.ContinueOnError)
			if err := flag.CommandLine.Parse(nil); err != nil {
				t.Fatal(err)
			}
			handleMaintenanceCommand("mode", configDir, "", "", "", "", "", config.DefaultConfig(), false, false)
		})
	}
}

func TestMaintenanceComplianceCommandUsesTemporaryOperationalDatabase(t *testing.T) {
	root := maintenanceTestDir(t)
	configuration := config.DefaultConfig()
	configuration.Server.Security.EncryptionKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	previousFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = previousFlags })
	flag.CommandLine = flag.NewFlagSet("gitmessages-test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{"report"}); err != nil {
		t.Fatal(err)
	}
	handleMaintenanceCommand(
		"compliance",
		filepath.Join(root, "config"),
		filepath.Join(root, "data"),
		filepath.Join(root, "database"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "backups"),
		filepath.Join(root, "run", "server.pid"),
		configuration,
		false,
		false,
	)
}

func TestMaintenanceUpdateCommandDispatchRejectsUnknownSubcommand(t *testing.T) {
	previousFlags := flag.CommandLine
	previousConfig := cfg
	t.Cleanup(func() {
		flag.CommandLine = previousFlags
		cfg = previousConfig
	})
	cfg = config.DefaultConfig()
	flag.CommandLine = flag.NewFlagSet("gitmessages-test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{"update", "unknown"}); err != nil {
		t.Fatal(err)
	}
	handleMaintenanceCommand("update", maintenanceTestDir(t), "", "", "", "", "", cfg, false, false)
}

func TestNewApplicationSchedulerRegistersConfiguredTasks(t *testing.T) {
	root := maintenanceTestDir(t)
	databaseDir := filepath.Join(root, "database")
	backupDir := filepath.Join(root, "backups")
	logsDir := filepath.Join(root, "logs")
	for _, directory := range []string{backupDir, logsDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configuration := config.DefaultConfig()
	configuration.Server.GeoIP.Enabled = false
	configuration.Server.Security.Blocklists.Enabled = false
	scheduled, err := newApplicationScheduler(store, nil, nil, configuration, paths.Directories{Backup: backupDir, Logs: logsDir}, log.New(io.Discard, "", 0), nil, nil, nil, func() (*overlay.TorManager, *overlay.I2PManager) { return nil, nil }, func() []string { return nil })
	if err != nil {
		t.Fatalf("newApplicationScheduler() error: %v", err)
	}
	if got := len(scheduled.GetTasks()); got < 10 {
		t.Fatalf("registered scheduler tasks = %d, want at least 10", got)
	}
	for _, taskID := range []string{"healthcheck_self", "geoip_update", "blocklist_update", "ssl_renewal", "log_rotation"} {
		for _, task := range scheduled.GetTasks() {
			if task.ID == taskID {
				if err := task.Func(); err != nil {
					t.Errorf("scheduled task %q: %v", taskID, err)
				}
				break
			}
		}
	}
}

func TestParseServerListeners(t *testing.T) {
	for _, test := range []struct {
		value      string
		sslEnabled bool
		ports      []int
		tls        []bool
		valid      bool
	}{
		{value: "8080", ports: []int{8080}, tls: []bool{false}, valid: true},
		{value: "443", ports: []int{443}, tls: []bool{true}, valid: true},
		{value: "8090", sslEnabled: true, ports: []int{8090}, tls: []bool{true}, valid: true},
		{value: "80,443", ports: []int{80, 443}, tls: []bool{false, true}, valid: true},
		{value: "8090,8443", ports: []int{8090, 8443}, tls: []bool{false, true}, valid: true},
		{value: "80,443,8443", valid: false},
		{value: "abc", valid: false},
	} {
		got, err := parseServerListeners(test.value, test.sslEnabled)
		if (err == nil) != test.valid {
			t.Errorf("parseServerListeners(%q) error = %v, want valid=%t", test.value, err, test.valid)
			continue
		}
		if !test.valid {
			continue
		}
		if len(got) != len(test.ports) {
			t.Fatalf("parseServerListeners(%q) returned %d listeners, want %d", test.value, len(got), len(test.ports))
		}
		for index, listener := range got {
			if listener.port != test.ports[index] || listener.tls != test.tls[index] {
				t.Errorf("listener %d for %q = %+v, want port %d tls %t", index, test.value, listener, test.ports[index], test.tls[index])
			}
		}
	}
}

func TestValidateTLSChallenge(t *testing.T) {
	for _, test := range []struct {
		name      string
		challenge string
		listeners []serverListener
		enabled   bool
		wantError bool
	}{
		{name: "disabled", challenge: "unsupported", enabled: false},
		{name: "default HTTP-01", listeners: []serverListener{{port: 80}}, enabled: true},
		{name: "HTTP-01 requires plain port 80", challenge: "http-01", listeners: []serverListener{{port: 80, tls: true}}, enabled: true, wantError: true},
		{name: "TLS-ALPN-01", challenge: "tls-alpn-01", listeners: []serverListener{{port: 443, tls: true}}, enabled: true},
		{name: "TLS-ALPN-01 requires TLS port 443", challenge: "tls-alpn-01", listeners: []serverListener{{port: 443}}, enabled: true, wantError: true},
		{name: "DNS-01", challenge: "dns-01", enabled: true},
		{name: "unsupported", challenge: "other", enabled: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := config.LetsEncryptConfig{Enabled: test.enabled, Challenge: test.challenge}
			err := validateTLSChallenge(test.listeners, configuration)
			if (err != nil) != test.wantError {
				t.Fatalf("validateTLSChallenge() error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestSecurityDataDirectoryAndBlocklistInputs(t *testing.T) {
	dataDirectory := maintenanceTestDir(t)
	resolved, err := securityDataDirectory("{data_dir}/security", dataDirectory)
	if err != nil {
		t.Fatalf("securityDataDirectory() error = %v", err)
	}
	if resolved != filepath.Join(dataDirectory, "security") {
		t.Fatalf("securityDataDirectory() = %q", resolved)
	}
	if _, err := securityDataDirectory("../outside", dataDirectory); err == nil {
		t.Fatal("securityDataDirectory() accepted traversal")
	}

	gotSources := blocklistSources([]config.BlocklistSourceConfig{
		{ID: "source-a", Type: "ip", URL: "https://example.test/list", Enabled: true},
		{ID: "source-b", Type: "domain", URL: "https://example.test/domains", Enabled: false},
	})
	if len(gotSources) != 2 || gotSources[0].ID != "source-a" || gotSources[0].Type != "ip" || !gotSources[0].Enabled || gotSources[1].Enabled {
		t.Fatalf("blocklistSources() = %+v", gotSources)
	}

	configuration := config.DefaultConfig()
	configuration.Server.TrustedProxies.Additional = []string{"192.0.2.9", "192.0.2.128/25", "not-a-prefix"}
	configuration.Server.Security.Allowlist = []config.AllowlistEntry{{CIDR: "2001:db8::1/64"}, {CIDR: "bad"}}
	prefixes := trustedBlocklistPrefixes(configuration)
	want := []string{"192.0.2.9/32", "192.0.2.128/25", "2001:db8::/64"}
	if len(prefixes) != len(want) {
		t.Fatalf("trustedBlocklistPrefixes() = %v, want %v", prefixes, want)
	}
	for index, prefix := range prefixes {
		if prefix != netip.MustParsePrefix(want[index]) {
			t.Errorf("trusted prefix %d = %s, want %s", index, prefix, want[index])
		}
	}
}

func TestResolveDirectoryOverridePrecedenceAndValidation(t *testing.T) {
	fallback := filepath.Join(maintenanceTestDir(t), "fallback")
	environment := filepath.Join(maintenanceTestDir(t), "environment")
	flagValue := filepath.Join(maintenanceTestDir(t), "flag")
	for _, test := range []struct {
		name      string
		flagValue string
		envValue  string
		fallback  string
		want      string
	}{
		{name: "flag overrides environment", flagValue: flagValue, envValue: environment, fallback: fallback, want: flagValue},
		{name: "environment overrides fallback", envValue: environment, fallback: fallback, want: environment},
		{name: "fallback", fallback: fallback, want: fallback},
	} {
		got, err := resolveDirectoryOverride("data", test.flagValue, test.envValue, test.fallback)
		if err != nil {
			t.Fatalf("%s: resolveDirectoryOverride() error = %v", test.name, err)
		}
		want, err := filepath.Abs(test.want)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: resolveDirectoryOverride() = %q, want %q", test.name, got, want)
		}
	}
	if _, err := resolveDirectoryOverride("data", "../outside", "", fallback); err == nil {
		t.Fatal("directory traversal was accepted")
	}
}

func TestResolvePIDFileOverridePrecedence(t *testing.T) {
	fallback := filepath.Join(maintenanceTestDir(t), "default.pid")
	environment := filepath.Join(maintenanceTestDir(t), "environment.pid")
	flagValue := filepath.Join(maintenanceTestDir(t), "flag.pid")
	for _, test := range []struct {
		name      string
		flagValue string
		envValue  string
		fallback  string
		want      string
	}{
		{name: "flag overrides environment", flagValue: flagValue, envValue: environment, fallback: fallback, want: flagValue},
		{name: "environment overrides fallback", envValue: environment, fallback: fallback, want: environment},
		{name: "fallback", fallback: fallback, want: fallback},
	} {
		got, err := resolvePIDFileOverride(test.flagValue, test.envValue, test.fallback)
		if err != nil {
			t.Fatalf("%s: resolvePIDFileOverride() error = %v", test.name, err)
		}
		want, err := filepath.Abs(test.want)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: resolvePIDFileOverride() = %q, want %q", test.name, got, want)
		}
	}
	if _, err := resolvePIDFileOverride("../outside.pid", "", fallback); err == nil {
		t.Fatal("PID file path traversal was accepted")
	}
}

func TestBuildDate(t *testing.T) {
	previous := BuildEpoch
	t.Cleanup(func() { BuildEpoch = previous })
	BuildEpoch = "1"
	if got := buildDate(); got != "1970-01-01T00:00:01Z" {
		t.Fatalf("buildDate() = %q, want the Unix epoch timestamp", got)
	}
	for _, value := range []string{"", "invalid", "0", "-1"} {
		BuildEpoch = value
		if got := buildDate(); got != "N/A" {
			t.Errorf("buildDate() for %q = %q, want N/A", value, got)
		}
	}
}

func TestConfigureSMTPDisablesEmailWithoutConfiguredRelay(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.Enabled = true
	configureSMTP(configuration)
	if configuration.Server.Notifications.Email.Enabled {
		t.Fatal("email remained enabled when SMTP_HOST was explicitly empty")
	}
}

func TestConfigureSMTPDisablesUnavailableConfiguredRelay(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.Enabled = true
	configuration.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
	configuration.Server.Notifications.Email.SMTP.Port = 1
	configuration.Server.Notifications.Email.SMTP.TLS = "none"
	configureSMTP(configuration)
	if configuration.Server.Notifications.Email.Enabled {
		t.Fatal("email remained enabled after configured SMTP probe failed")
	}
}

func TestConfigureSMTPEnablesSuccessfulRelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = io.WriteString(connection, "220 localhost ESMTP\r\n")
		_, _ = bufio.NewReader(connection).ReadString('\n')
		_, _ = io.WriteString(connection, "250 localhost\r\n")
	}()
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
	configuration.Server.Notifications.Email.SMTP.Port = listener.Addr().(*net.TCPAddr).Port
	configuration.Server.Notifications.Email.SMTP.TLS = "none"
	configureSMTP(configuration)
	if !configuration.Server.Notifications.Email.Enabled {
		t.Fatal("email remained disabled after a successful SMTP greeting")
	}
}

func TestInitializeSecurityDataDegradesWhenInitialDownloadsFail(t *testing.T) {
	configuration := config.DefaultConfig()
	configuration.Server.GeoIP.Enabled = true
	configuration.Server.Security.Blocklists.Enabled = true
	configuration.Data.Trivy.Enabled = true
	manager := &securitydata.Manager{Dir: filepath.Join(maintenanceTestDir(t), "security")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var logs bytes.Buffer
	initializeSecurityData(ctx, manager, configuration, log.New(&logs, "", 0))
	for _, dataset := range []string{"geoip", "blocklist", "NVD", "Trivy"} {
		if !strings.Contains(logs.String(), dataset) {
			t.Errorf("initialization failure logs omitted %s dataset warning: %s", dataset, logs.String())
		}
	}
}

func TestAuthorizeModeChange(t *testing.T) {
	for _, test := range []struct {
		name           string
		serverToken    string
		supplied       string
		root           bool
		serviceUser    bool
		wantAuthorized bool
	}{
		{name: "root", root: true, wantAuthorized: true},
		{name: "service user with token", serverToken: "operator-token", supplied: "operator-token", serviceUser: true, wantAuthorized: true},
		{name: "service user with wrong token", serverToken: "operator-token", supplied: "wrong-token", serviceUser: true},
		{name: "service user without token", serviceUser: true},
		{name: "unrelated user with matching token", serverToken: "operator-token", supplied: "operator-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := authorizeModeChange(test.serverToken, test.supplied, test.root, test.serviceUser)
			if (err == nil) != test.wantAuthorized {
				t.Fatalf("authorizeModeChange() error = %v, authorized = %t; want %t", err, err == nil, test.wantAuthorized)
			}
		})
	}
}

func TestTokenMaintenanceListsAndRevokesWithYes(t *testing.T) {
	previousConfig := cfg
	configuration := config.DefaultConfig()
	configuration.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	configuration.Server.Security.EncryptionKey = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg = configuration
	t.Cleanup(func() { cfg = previousConfig })
	databaseDir := filepath.Join(maintenanceTestDir(t), "database")
	store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ownerStore, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, 25, 0, time.Hour)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	primary, identity, err := ownerStore.IssueOwner(t.Context())
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, _, err := ownerStore.CreateLinked(t.Context(), identity.OwnerID, "maintenance-test", nil); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	listed, err := ownerStore.List(t.Context(), identity.OwnerID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Kind != "primary" {
		_ = store.Close()
		t.Fatalf("owner token list = %#v", listed)
	}
	primaryPrefix := listed[0].Prefix
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	handleTokenMaintenance([]string{"list"}, databaseDir, configuration)
	handleTokenMaintenance([]string{"revoke", primaryPrefix, "--yes"}, databaseDir, configuration)
	checkStore, err := auth.Open(filepath.Join(databaseDir, "server.db"), configuration.Server.Token, configuration.Server.Security.EncryptionKey, 25, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = checkStore.Close() }()
	if _, err := checkStore.Authenticate(t.Context(), primary); err == nil {
		t.Fatal("maintenance CLI left the revoked primary token valid")
	}
}

func TestMaintenanceBackupTestVerifiesExplicitAndNewestBackup(t *testing.T) {
	root := maintenanceTestDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	databaseDir := filepath.Join(root, "database")
	backupDir := filepath.Join(root, "backups")
	for _, directory := range []string{configDir, dataDir, databaseDir, backupDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: 64580\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "catalog.txt"), []byte("curated data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenSQLite(filepath.Join(databaseDir, "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, "gitmessages_backup_2026-11-23.tar.gz")
	if _, err := createApplicationBackup(t.Context(), store, config.DefaultConfig(), configDir, dataDir, backupDir, backupPath, false, true); err != nil {
		_ = store.Close()
		t.Fatalf("create source backup: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	previousFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = previousFlags })
	for _, arguments := range [][]string{{"test", backupPath}, {"test"}} {
		flag.CommandLine = flag.NewFlagSet("gitmessages-test", flag.ContinueOnError)
		if err := flag.CommandLine.Parse(arguments); err != nil {
			t.Fatalf("parse maintenance arguments %q: %v", arguments, err)
		}
		previousStdout := os.Stdout
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = writer
		handleMaintenanceCommand("backup", configDir, dataDir, databaseDir, filepath.Join(root, "logs"), backupDir, "", config.DefaultConfig(), false, true)
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		os.Stdout = previousStdout
		output, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(output), "Backup verified:") || !strings.Contains(string(output), filepath.Base(backupPath)) {
			t.Fatalf("backup test output for arguments %q = %q", arguments, output)
		}
	}
}

func TestConfirmTokenRevocationRequiresTheSpecifiedValue(t *testing.T) {
	linked := auth.Token{Kind: "linked", Prefix: "lnk_1234"}
	var prompt bytes.Buffer
	if err := confirmTokenRevocation(strings.NewReader("wrong\n"), &prompt, linked, 2, false); err == nil {
		t.Fatal("linked-token revocation accepted a mismatched prefix")
	}
	if !strings.Contains(prompt.String(), "linked token prefix") || !strings.Contains(prompt.String(), linked.Prefix) {
		t.Fatalf("linked-token prompt omitted its required confirmation: %q", prompt.String())
	}
	prompt.Reset()
	if err := confirmTokenRevocation(strings.NewReader(linked.Prefix+"\n"), &prompt, linked, 2, false); err != nil {
		t.Fatalf("linked-token revocation rejected matching prefix: %v", err)
	}
	primary := auth.Token{Kind: "primary", Prefix: "own_abcd", OwnerID: "owner-123"}
	prompt.Reset()
	if err := confirmTokenRevocation(strings.NewReader("owner-123\n"), &prompt, primary, 2, false); err != nil {
		t.Fatalf("primary-token revocation rejected matching owner id: %v", err)
	}
	if !strings.Contains(prompt.String(), "2 tokens, 0 resources") {
		t.Fatalf("primary-token prompt omitted impact counts: %q", prompt.String())
	}
	if err := confirmTokenRevocation(strings.NewReader(""), &prompt, primary, 2, true); err != nil {
		t.Fatalf("--yes did not skip the revocation prompt: %v", err)
	}
}

func TestServerShellIntegrationOutputsAndDetection(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"gitmessages-test"}
	t.Cleanup(func() { os.Args = oldArgs })
	capture := func(call func() error) (string, error) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		previous := os.Stdout
		os.Stdout = writer
		callErr := call()
		_ = writer.Close()
		os.Stdout = previous
		output, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(output), callErr
	}
	if output, err := capture(func() error { return printShellIntegration("help", "") }); err != nil || !strings.Contains(output, "completions") {
		t.Fatalf("shell help = %q, %v", output, err)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "sh", "powershell"} {
		output, err := capture(func() error { return printShellIntegration("completions", shell) })
		if err != nil || !strings.Contains(output, "gitmessages-test") {
			t.Fatalf("%s completions = %q, %v", shell, output, err)
		}
		output, err = capture(func() error { return printShellIntegration("init", shell) })
		if err != nil || !strings.Contains(output, "gitmessages-test") {
			t.Fatalf("%s init = %q, %v", shell, output, err)
		}
	}
	if _, err := capture(func() error { return printShellIntegration("completions", "unknown") }); err == nil {
		t.Fatal("unsupported shell was accepted")
	}
	if _, err := capture(func() error { return printShellIntegration("unknown", "bash") }); err == nil {
		t.Fatal("unsupported shell integration action was accepted")
	}
	t.Setenv("SHELL", "/bin/zsh")
	if got := detectShell(); got != "zsh" {
		t.Fatalf("detectShell() = %q, want zsh", got)
	}
	t.Setenv("SHELL", "")
	t.Setenv("ComSpec", "powershell.exe")
	if got := detectShell(); got != "powershell" {
		t.Fatalf("detectShell() = %q, want powershell", got)
	}
	t.Setenv("ComSpec", "")
	if got := detectShell(); got != "bash" {
		t.Fatalf("detectShell() = %q, want bash", got)
	}
}

func TestConfiguredTokenCookieAge(t *testing.T) {
	for _, input := range []struct {
		value string
		want  time.Duration
	}{
		{"", 365 * 24 * time.Hour},
		{" 1y ", 365 * 24 * time.Hour},
		{"2y", 2 * 365 * 24 * time.Hour},
		{"720h", 30 * 24 * time.Hour},
		{"0", 365 * 24 * time.Hour},
		{"invalid", 365 * 24 * time.Hour},
		{"0y", 365 * 24 * time.Hour},
	} {
		if got := configuredTokenCookieAge(input.value); got != input.want {
			t.Errorf("configuredTokenCookieAge(%q) = %s, want %s", input.value, got, input.want)
		}
	}
}

// TestSetupRoutes is a mux smoke test verifying routes dispatch instead of
// falling through to a 404.
func TestSetupRoutes(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	paths := []string{
		"/", "/types", "/templates", "/builder", "/server/about",
		"/server/help", "/server/healthz", "/manifest.json", "/sw.js",
		"/api/v1", "/api/v1/types", "/api/v1/conventions",
		"/api/v1/templates", "/api/v1/messages/random", "/api/v1/server/healthz",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("route %s not registered on mux", p)
		}
	}
}

// TestCheckHealth covers the success path against a live httptest server and
// the failure path against a port nothing listens on.
func TestCheckHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/healthz" && r.URL.Path != "/messages/api/v1/server/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("failed to split test server host/port: %v", err)
	}

	if err := checkHealth(port); err != nil {
		t.Errorf("checkHealth(%q) error = %v, want nil", port, err)
	}
	if err := checkHealth(port, "/messages"); err != nil {
		t.Errorf("checkHealth(%q, /messages) error = %v, want nil", port, err)
	}

	// Grab a port and immediately close the listener so nothing answers it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate a free port: %v", err)
	}
	_, freePort, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()

	if err := checkHealth(freePort); err == nil {
		t.Error("checkHealth against a closed port: expected error, got nil")
	}
}

func TestNormalizeUpdateBranch(t *testing.T) {
	for input, want := range map[string]string{
		"stable":      "stable",
		"beta":        "beta",
		"devel":       "devel",
		"daily":       "devel",
		"rolling":     "devel",
		"development": "devel",
		"invalid":     "",
	} {
		if got := normalizeUpdateBranch(input); got != want {
			t.Errorf("normalizeUpdateBranch(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestApplyI2POverrides(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"gitmessages"}
	current := config.DefaultConfig()
	current.Server.I2P.Enabled = false
	t.Setenv("I2P_ENABLED", "oui")
	if err := applyI2POverrides(current, false); err != nil || !current.Server.I2P.Enabled {
		t.Fatalf("truthy I2P_ENABLED result = %v, %v", current.Server.I2P.Enabled, err)
	}
	t.Setenv("I2P_ENABLED", "not-a-boolean")
	if err := applyI2POverrides(current, false); err == nil {
		t.Fatal("invalid I2P_ENABLED was accepted")
	}
	t.Setenv("I2P_ENABLED", "true")
	os.Args = []string{"gitmessages", "--i2p=false"}
	if err := applyI2POverrides(current, false); err != nil || current.Server.I2P.Enabled {
		t.Fatalf("explicit --i2p=false did not override env: enabled=%v err=%v", current.Server.I2P.Enabled, err)
	}
	if err := applyI2POverrides(nil, false); err == nil {
		t.Fatal("nil configuration was accepted")
	}
}

func TestConfigurationFingerprintAndWatcher(t *testing.T) {
	directory := maintenanceTestDir(t)
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  mode: production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := configurationFingerprint(directory)
	if first == "" {
		t.Fatal("configuration fingerprint is empty")
	}
	if err := os.WriteFile(configPath, []byte("server:\n  mode: development\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if configurationFingerprint(directory) == first {
		t.Fatal("configuration fingerprint did not change after an edit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	changed := make(chan struct{}, 1)
	go watchConfiguration(ctx, directory, changed)
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte("server:\n  mode: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("configuration watcher did not observe the change")
	}
	cancel()
}

func TestPrintHelp(t *testing.T) {
	printHelp("gitmessages")
}

// TestRunSetupWizard verifies the wizard creates a config file under an
// isolated temp directory without touching any real system path.
func TestRunSetupWizard(t *testing.T) {
	dir := maintenanceTestDir(t)

	runSetupWizard(dir)

	configFile := filepath.Join(dir, "server.yml")
	if _, err := os.Stat(configFile); err != nil {
		t.Errorf("expected setup wizard to create %s: %v", configFile, err)
	}
}

// TestSetApplicationMode_ValidMode covers the success branch: a valid mode
// is persisted to an isolated temp config file. The invalid-mode branch
// calls os.Exit and cannot be safely unit tested in-process.
func TestSetApplicationMode_ValidMode(t *testing.T) {
	configFile := filepath.Join(maintenanceTestDir(t), "server.yml")

	setApplicationMode("development", configFile)

	loaded, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if loaded.Server.Mode != "development" {
		t.Errorf("Server.Mode = %q, want development", loaded.Server.Mode)
	}
}

// TestHandleUpdateCommand covers the "check" informational branch and the
// "branch <valid>" success branch. The invalid-branch/unknown-command
// branches call os.Exit and cannot be safely unit tested in-process.
func TestHandleUpdateCommand(t *testing.T) {
	configFile := filepath.Join(maintenanceTestDir(t), "server.yml")
	currentCfg, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	handleUpdateCommand("check", currentCfg)

	handleUpdateCommand("branch beta", currentCfg)
	handleUpdateCommand("invalid", currentCfg)

	reloaded, err := config.Load(configFile)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if reloaded.Server.Update.Branch != "beta" {
		t.Errorf("Server.Update.Branch = %q, want beta", reloaded.Server.Update.Branch)
	}
}

// TestServiceDispatch_Status exercises the safe read-only service commands.
// These only shell out via runCommand, which logs and returns on failure
// rather than exiting, so they are safe to call even when the underlying
// service manager (systemctl/launchctl) is unavailable in this environment.
func TestServiceDispatch_Status(t *testing.T) {
	handleServiceCommand("status", maintenanceTestDir(t), paths.GetDirectories())
	serviceStart()
	serviceStop()
	serviceRestart()
	serviceReload()
	serviceStatus()
}

// TestSystemdServiceInstallAndUninstallCancellation exercises service file
// creation and verifies that a declined uninstall preserves the service data.
func TestSystemdServiceInstallAndUninstallCancellation(t *testing.T) {
	configDir := maintenanceTestDir(t)
	serviceFile := filepath.Join(maintenanceTestDir(t), "systemd", "gitmessages.service")
	var commands []string
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0o700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	var installOutput bytes.Buffer
	serviceInstall(configDir, "linux", serviceFile, "", command, &installOutput)

	data, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("service unit file missing after serviceInstall: %v", err)
	}
	if !strings.Contains(string(data), configDir) {
		t.Errorf("service unit file does not reference configDir %q", configDir)
	}
	if len(commands) != 3 || commands[0] != "systemctl daemon-reload" || commands[1] != "systemctl enable gitmessages" || commands[2] != "systemctl start gitmessages" {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(installOutput.String(), "Service installed and started successfully") {
		t.Errorf("installation status missing: %q", installOutput.String())
	}

	var output bytes.Buffer
	serviceUninstallWithInput(configDir, strings.NewReader("n\n"), &output)

	if _, err := os.Stat(serviceFile); err != nil {
		t.Errorf("service unit file was removed despite uninstall cancellation, err=%v", err)
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Errorf("configuration directory was removed despite uninstall cancellation, err=%v", err)
	}
	if !strings.Contains(output.String(), "This will delete ALL data, configs, and the system user. Continue? [y/N]") {
		t.Errorf("uninstall confirmation prompt missing: %q", output.String())
	}

	var unsupportedOutput bytes.Buffer
	if err := installServiceFor("openbsd", configDir, "", "", command, &unsupportedOutput); err != nil {
		t.Fatalf("unsupported service platform should report status without failing: %v", err)
	}
	if !strings.Contains(unsupportedOutput.String(), "Service installation not supported on openbsd") {
		t.Errorf("unsupported platform status missing: %q", unsupportedOutput.String())
	}
	serviceInstall(configDir, "linux", filepath.Join(maintenanceTestDir(t), "missing", "gitmessages.service"), "", command, &installOutput)
}

func TestLaunchdServiceInstall(t *testing.T) {
	configDir := maintenanceTestDir(t)
	serviceFile := filepath.Join(maintenanceTestDir(t), "LaunchDaemons", "gitmessages.plist")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0o700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	var commands []string
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	var output bytes.Buffer
	if err := installServiceFor("darwin", configDir, "", serviceFile, command, &output); err != nil {
		t.Fatalf("installServiceFor: %v", err)
	}
	data, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("service plist missing after installServiceFor: %v", err)
	}
	if !strings.Contains(string(data), configDir) {
		t.Errorf("service plist does not reference configDir %q", configDir)
	}
	if len(commands) != 1 || commands[0] != "launchctl load "+serviceFile {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(output.String(), "Service installed and started successfully") {
		t.Errorf("installation status missing: %q", output.String())
	}
}

func TestRemoveServiceData(t *testing.T) {
	root := maintenanceTestDir(t)
	dirs := paths.Directories{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
		Logs:   filepath.Join(root, "logs"),
		Backup: filepath.Join(root, "backup"),
		PID:    filepath.Join(root, "run", "gitmessages.pid"),
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, filepath.Dir(dirs.PID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	if err := os.WriteFile(dirs.PID, []byte("123"), 0o600); err != nil {
		t.Fatalf("WriteFile(PID): %v", err)
	}
	if err := removeServiceData(dirs); err != nil {
		t.Fatalf("removeServiceData: %v", err)
	}
	for _, path := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, dirs.PID} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("path remains after service uninstall: %s (err=%v)", path, err)
		}
	}
}

func TestConfirmServiceUninstall(t *testing.T) {
	var output bytes.Buffer
	if !confirmServiceUninstall(strings.NewReader("yes\n"), &output) {
		t.Fatal("affirmative response did not confirm service uninstall")
	}
	if !strings.Contains(output.String(), "This will delete ALL data, configs, and the system user. Continue? [y/N]") {
		t.Errorf("uninstall confirmation prompt missing: %q", output.String())
	}
}

func TestUninstallService(t *testing.T) {
	root := maintenanceTestDir(t)
	serviceFile := filepath.Join(root, "systemd", "gitmessages.service")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0o700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	if err := os.WriteFile(serviceFile, []byte("unit"), 0o600); err != nil {
		t.Fatalf("WriteFile(service file): %v", err)
	}
	dirs := paths.Directories{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
		Logs:   filepath.Join(root, "logs"),
		Backup: filepath.Join(root, "backup"),
		PID:    filepath.Join(root, "run", "gitmessages.pid"),
	}
	for _, dir := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, filepath.Dir(dirs.PID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	if err := os.WriteFile(dirs.PID, []byte("123"), 0o600); err != nil {
		t.Fatalf("WriteFile(PID): %v", err)
	}
	var commands []string
	var output bytes.Buffer
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := uninstallService("linux", dirs, serviceFile, "/usr/local/bin/gitmessages", &output, command); err != nil {
		t.Fatalf("uninstallService: %v", err)
	}
	if len(commands) != 3 || commands[0] != "systemctl stop gitmessages" || commands[1] != "systemctl disable gitmessages" || commands[2] != "systemctl daemon-reload" {
		t.Errorf("service commands = %v", commands)
	}
	if !strings.Contains(output.String(), "Service uninstalled. Delete binary manually: rm /usr/local/bin/gitmessages") {
		t.Errorf("binary removal guidance missing: %q", output.String())
	}
	for _, path := range []string{serviceFile, dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, dirs.PID} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("path remains after uninstall: %s (err=%v)", path, err)
		}
	}
}

func TestUninstallServiceDarwin(t *testing.T) {
	serviceFile := filepath.Join(maintenanceTestDir(t), "launchd", "gitmessages.plist")
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0o700); err != nil {
		t.Fatalf("MkdirAll(service file): %v", err)
	}
	if err := os.WriteFile(serviceFile, []byte("plist"), 0o600); err != nil {
		t.Fatalf("WriteFile(service file): %v", err)
	}
	var commands []string
	var output bytes.Buffer
	command := func(name string, args ...string) {
		commands = append(commands, name+" "+strings.Join(args, " "))
	}
	if err := uninstallService("darwin", paths.Directories{}, serviceFile, "/usr/local/bin/gitmessages", &output, command); err != nil {
		t.Fatalf("uninstallService: %v", err)
	}
	if len(commands) != 1 || commands[0] != "launchctl unload "+serviceFile {
		t.Errorf("service commands = %v", commands)
	}
	if _, err := os.Stat(serviceFile); !os.IsNotExist(err) {
		t.Errorf("launchd plist remains after uninstall (err=%v)", err)
	}
}

func TestCleanupSecurityDataHonorsClosedReportRetention(t *testing.T) {
	ctx := context.Background()
	store, err := database.OpenSQLite(filepath.Join(maintenanceTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -366).Format(time.RFC3339)
	recent := now.AddDate(0, 0, -100).Format(time.RFC3339)
	for _, report := range []struct {
		id, status string
		closedAt   any
	}{
		{id: "old-closed", status: "disclosed", closedAt: old},
		{id: "old-open", status: "received", closedAt: nil},
		{id: "recent-closed", status: "disclosed", closedAt: recent},
	} {
		if err := store.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,token_hash,closed_at) VALUES(?,?,?,?,?,?,?,?,?)`, report.id, time.Now().Unix()/172800, "low", "test", report.status, []byte("encrypted"), "aes-256-gcm", "hash", report.closedAt)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	configDir := maintenanceTestDir(t)
	retiredKey := filepath.Join(configDir, "security", "pgp.priv.old.asc.enc")
	if err := os.MkdirAll(filepath.Dir(retiredKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retiredKey, []byte("encrypted-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(retiredKey, now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSecurityData(ctx, store, configDir, 365, now); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"old-closed": false, "old-open": true, "recent-closed": true} {
		var count int
		if err := store.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM security_reports WHERE tracking_id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if got := count == 1; got != want {
			t.Errorf("report %q remains = %t, want %t", id, got, want)
		}
	}
	if _, err := os.Stat(retiredKey); !os.IsNotExist(err) {
		t.Errorf("retired PGP key remains after 30 days (err=%v)", err)
	}
}

func TestApplicationSchedulerRegistersAndRunsLocalBuiltIns(t *testing.T) {
	root := maintenanceTestDir(t)
	directories := paths.Directories{
		Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"),
		Logs: filepath.Join(root, "logs"), Backup: filepath.Join(root, "backups"),
	}
	for _, directory := range []string{directories.Config, directories.Data, directories.Logs, directories.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := database.OpenSQLite(filepath.Join(root, "db", "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.DiskLow.Enabled = false
	configuration.Server.Security.Blocklists.Enabled = false
	configuration.Server.GeoIP.Enabled = false
	configuration.Data.Trivy.Enabled = false
	for _, id := range []string{"backup_daily", "backup_hourly", "cve_update", "update_check"} {
		task := configuration.Server.Scheduler.Tasks[id]
		task.Enabled = false
		configuration.Server.Scheduler.Tasks[id] = task
	}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	authStore, err := auth.NewStore(store, "", key, 25, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	manager := securitydata.New(filepath.Join(directories.Data, "security"))
	scheduled, err := newApplicationScheduler(store, manager, authStore, configuration, directories, log.New(io.Discard, "", 0), nil, nil, nil, func() (*overlay.TorManager, *overlay.I2PManager) { return nil, nil }, func() []string { return nil })
	if err != nil {
		t.Fatalf("newApplicationScheduler() error: %v", err)
	}
	tasks := make(map[string]func() error)
	for _, task := range scheduled.GetTasks() {
		tasks[task.ID] = task.Func
	}
	for _, id := range []string{"healthcheck_self", "security_cleanup", "token_cleanup", "geoip_update", "blocklist_update", "log_rotation", "backup_daily", "backup_hourly", "disk_check", "ssl_renewal"} {
		if tasks[id] == nil {
			t.Errorf("required built-in task %q was not registered", id)
		}
	}
	for _, id := range []string{"healthcheck_self", "security_cleanup", "token_cleanup", "geoip_update", "blocklist_update", "log_rotation", "disk_check", "ssl_renewal"} {
		if err := tasks[id](); err != nil {
			t.Errorf("built-in task %q failed: %v", id, err)
		}
	}
	if err := scheduled.RunNow("healthcheck_self"); err != nil {
		t.Fatalf("run healthcheck task through scheduler: %v", err)
	}
	var audited int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category='scheduler' AND action='scheduler.task_succeeded' AND target_id='healthcheck_self'`).Scan(&audited); err != nil {
		t.Fatalf("read scheduler audit event: %v", err)
	}
	if audited != 1 {
		t.Fatalf("scheduler success audit entries = %d, want 1", audited)
	}
}

func TestOverlayManagersTrackReplacements(t *testing.T) {
	managers := &overlayManagers{}
	if tor, i2p := managers.current(); tor != nil || i2p != nil {
		t.Fatal("empty overlay manager state returned a manager")
	}
	torManager := &overlay.TorManager{}
	i2pManager := &overlay.I2PManager{}
	managers.replaceTor(torManager)
	managers.replaceI2P(i2pManager)
	if tor, i2p := managers.current(); tor != torManager || i2p != i2pManager {
		t.Fatal("overlay manager replacement did not retain the new state")
	}
}

func TestConfiguredTLSHostsPrefersConfiguredFQDN(t *testing.T) {
	t.Setenv("DOMAIN", "fallback.example.test,ignored.example.test")
	if got := configuredTLSHosts(nil); got != nil {
		t.Fatalf("configuredTLSHosts(nil) = %v, want nil", got)
	}
	configuration := config.DefaultConfig()
	if got := configuredTLSHosts(configuration); len(got) != 1 || got[0] != "fallback.example.test" {
		t.Fatalf("configuredTLSHosts() env fallback = %v", got)
	}
	configuration.Server.FQDN = " configured.example.test "
	if got := configuredTLSHosts(configuration); len(got) != 1 || got[0] != "configured.example.test" {
		t.Fatalf("configuredTLSHosts() configured host = %v", got)
	}
	configuration.Server.FQDN = ""
	t.Setenv("DOMAIN", ",ignored.example.test")
	if got := configuredTLSHosts(configuration); got != nil {
		t.Fatalf("configuredTLSHosts() accepted an empty first DOMAIN: %v", got)
	}
}
