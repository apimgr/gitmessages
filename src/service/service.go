package service

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	projectpaths "github.com/apimgr/gitmessages/src/paths"
)

const (
	appName = projectpaths.ProjectName
	orgName = projectpaths.OrgName
)

// capitalize upper-cases the first rune of an ASCII word, leaving the rest unchanged.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ServiceType represents the type of service manager
type ServiceType int

const (
	ServiceUnknown ServiceType = iota
	ServiceSystemd
	ServiceOpenRC
	ServiceRunit
	ServiceLaunchd
	ServiceWindows
	ServiceBSDRC
	ServiceS6
	ServiceSysV
)

// DetectServiceManager detects the system's service manager
func DetectServiceManager() ServiceType {
	_, s6Running := findS6ScanDir("/proc", "/run/service", os.ReadDir, os.ReadFile)
	return detectServiceManagerWith(runtime.GOOS, os.Stat, exec.LookPath, s6Running)
}

func detectServiceManager(goos string, stat func(string) (os.FileInfo, error), lookPath func(string) (string, error)) ServiceType {
	_, s6Running := findS6ScanDir("/proc", "/run/service", os.ReadDir, os.ReadFile)
	return detectServiceManagerWith(goos, stat, lookPath, s6Running)
}

func detectServiceManagerWith(goos string, stat func(string) (os.FileInfo, error), lookPath func(string) (string, error), s6Running bool) ServiceType {
	exists := func(path string) bool { _, err := stat(path); return err == nil }
	available := func(name string) bool { _, err := lookPath(name); return err == nil }
	switch goos {
	case "linux":
		if exists("/run/systemd/system") {
			return ServiceSystemd
		}
		if exists("/sbin/openrc-run") && exists("/run/openrc") {
			return ServiceOpenRC
		}
		if exists("/etc/runit") || available("sv") {
			return ServiceRunit
		}
		if available("s6-svc") && s6Running {
			return ServiceS6
		}
		if exists("/etc/init.d") && (available("update-rc.d") || available("chkconfig")) {
			return ServiceSysV
		}
		return ServiceUnknown

	case "darwin":
		return ServiceLaunchd

	case "windows":
		return ServiceWindows

	case "freebsd", "openbsd", "netbsd":
		return ServiceBSDRC

	default:
		return ServiceUnknown
	}
}

func findS6ScanDir(procRoot, fallback string, readDir func(string) ([]os.DirEntry, error), readFile func(string) ([]byte, error)) (string, bool) {
	entries, err := readDir(procRoot)
	if err != nil {
		return fallback, true
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cmdline, readErr := readFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
		if readErr != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if len(args) == 0 || filepath.Base(args[0]) != "s6-svscan" {
			continue
		}
		if len(args) > 1 && filepath.IsAbs(args[1]) {
			return filepath.Clean(args[1]), true
		}
		return fallback, true
	}
	return "", false
}

// Install installs the service for the detected service manager
func Install() error {
	serviceType := DetectServiceManager()
	if !isSystemInstall() && serviceType != ServiceSystemd && serviceType != ServiceLaunchd {
		return fmt.Errorf("service manager %v does not support a non-root user service", serviceType)
	}
	if serviceType != ServiceUnknown && isSystemInstall() {
		if startupPreparation == nil {
			return fmt.Errorf("service startup preparation is not configured")
		}
		if err := startupPreparation(); err != nil {
			return fmt.Errorf("prepare service account and directories: %w", err)
		}
	}

	switch serviceType {
	case ServiceSystemd:
		return installSystemd()
	case ServiceOpenRC:
		return installOpenRC()
	case ServiceRunit:
		return installRunit()
	case ServiceS6:
		return installS6()
	case ServiceSysV:
		return installSysV()
	case ServiceLaunchd:
		return installLaunchd()
	case ServiceWindows:
		return installWindows()
	case ServiceBSDRC:
		return installBSDRC()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

var startupPreparation func() error

// SetStartupPreparation connects service installation to the same idempotent
// account and directory setup used by normal server startup.
func SetStartupPreparation(prepare func() error) {
	startupPreparation = prepare
}

func isSystemInstall() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	current, err := user.Current()
	return err == nil && current.Uid == "0"
}

func systemdCommand(args ...string) *exec.Cmd {
	if !isSystemInstall() {
		args = append([]string{"--user"}, args...)
	}
	return exec.Command("systemctl", args...)
}

func launchdPlistPath() (string, error) {
	if isSystemInstall() {
		return filepath.Join("/Library/LaunchDaemons", fmt.Sprintf("com.%s.%s.plist", orgName, appName)), nil
	}
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(current.HomeDir, "Library", "LaunchAgents", fmt.Sprintf("com.%s.%s.plist", orgName, appName)), nil
}

// Uninstall removes the service
func Uninstall() error {
	serviceType := DetectServiceManager()

	switch serviceType {
	case ServiceSystemd:
		return uninstallSystemd()
	case ServiceOpenRC:
		return uninstallOpenRC()
	case ServiceRunit:
		return uninstallRunit()
	case ServiceS6:
		return uninstallS6()
	case ServiceSysV:
		return uninstallSysV()
	case ServiceLaunchd:
		return uninstallLaunchd()
	case ServiceWindows:
		return uninstallWindows()
	case ServiceBSDRC:
		return uninstallBSDRC()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

func installOpenRC() error {
	binaryPath := GetBinaryPath()
	servicePath := "/etc/init.d/" + appName
	serviceContent := openRCServiceContent(binaryPath)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		return fmt.Errorf("create OpenRC init directory: %w", err)
	}
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0o755); err != nil {
		return fmt.Errorf("write OpenRC service: %w", err)
	}
	if err := installBinary(binaryPath); err != nil {
		return err
	}
	if err := exec.Command("rc-update", "add", appName, "default").Run(); err != nil {
		return fmt.Errorf("enable OpenRC service: %w", err)
	}
	if err := exec.Command("rc-service", appName, "start").Run(); err != nil {
		return fmt.Errorf("start OpenRC service: %w", err)
	}
	return nil
}

func openRCServiceContent(binaryPath string) string {
	return fmt.Sprintf(`#!/sbin/openrc-run
name="%s"
description="Git Messages server"
command="%s"
command_args=""
pidfile="/var/run/%s/%s/%s.service.pid"
command_background=true
output_log="/var/log/%s/%s/server.log"
error_log="/var/log/%s/%s/error.log"
depend() {
    need net
    after firewall
    use dns logger
}

start_pre() {
    checkpath -d -m 0755 /var/run/%s
    checkpath -d -m 0755 /var/log/%s
    checkpath -d -m 0755 -o %s:%s /var/run/%s/%s
    checkpath -d -m 0755 -o %s:%s /var/log/%s/%s
}
`, appName, binaryPath, orgName, appName, appName, orgName, appName, orgName, appName, orgName, orgName, appName, appName, orgName, appName, appName, appName, orgName, appName)
}

func uninstallOpenRC() error {
	if err := exec.Command("rc-service", appName, "stop").Run(); err != nil {
		return fmt.Errorf("stop OpenRC service: %w", err)
	}
	if err := exec.Command("rc-update", "del", appName, "default").Run(); err != nil {
		return fmt.Errorf("disable OpenRC service: %w", err)
	}
	if err := os.Remove("/etc/init.d/" + appName); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove OpenRC service: %w", err)
	}
	return nil
}

// GetBinaryPath returns the path where the binary should be installed
func GetBinaryPath() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf(`C:\Program Files\%s\%s\%s.exe`, orgName, appName, appName)
	default:
		return fmt.Sprintf("/usr/local/bin/%s", appName)
	}
}

// installSystemd creates systemd service file
func installSystemd() error {
	binaryPath := GetBinaryPath()
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine service install user: %w", err)
	}
	userInstall := current.Uid != "0"
	unitDir := "/etc/systemd/system"
	unitName := appName + ".service"
	command := func(args ...string) *exec.Cmd { return exec.Command("systemctl", args...) }
	if userInstall {
		binaryPath = filepath.Join(current.HomeDir, ".local", "bin", appName)
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(current.HomeDir, ".config")
		}
		unitDir = filepath.Join(configDir, "systemd", "user")
		command = func(args ...string) *exec.Cmd {
			return exec.Command("systemctl", append([]string{"--user"}, args...)...)
		}
	}

	serviceContent := systemdUnit(binaryPath, userInstall)

	servicePath := filepath.Join(unitDir, unitName)

	// Create directories
	dirs := []string{
		fmt.Sprintf("/var/lib/%s/%s", orgName, appName),
		fmt.Sprintf("/var/log/%s/%s", orgName, appName),
		fmt.Sprintf("/etc/%s/%s", orgName, appName),
	}
	if userInstall {
		dirs = []string{unitDir, filepath.Dir(binaryPath)}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write service file
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0o644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Copy binary if not already in place
	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	// Reload systemd
	if err := command("daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Enable service
	if err := command("enable", appName).Run(); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}
	if err := command("start", appName).Run(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	fmt.Printf("✅ Service installed at: %s\n", servicePath)
	fmt.Printf("✅ Binary installed at: %s\n", binaryPath)
	fmt.Println()
	fmt.Println("To start the service:")
	fmt.Printf("  sudo systemctl start %s\n", appName)
	fmt.Println()
	fmt.Println("To check status:")
	fmt.Printf("  sudo systemctl status %s\n", appName)

	return nil
}

func systemdUnit(binaryPath string, userInstall bool) string {
	if userInstall {
		return fmt.Sprintf(`[Unit]
Description=Git Messages service

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, binaryPath)
	}
	return fmt.Sprintf(`[Unit]
Description=Git Messages service
Documentation=https://gitmessages.apimgr.us
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
ProtectSystem=true
ProtectHome=yes
PrivateTmp=yes
ExecStartPre=+/usr/bin/install -d -m 0755 /etc/%s/%s /var/lib/%s/%s /var/cache/%s/%s /var/log/%s/%s /var/run/%s/%s
ReadWritePaths=-/etc/%s/%s
ReadWritePaths=-/var/lib/%s/%s
ReadWritePaths=-/var/cache/%s/%s
ReadWritePaths=-/var/log/%s/%s
ReadWritePaths=-/var/run/%s/%s
ReadWritePaths=-/mnt/Backups/%s/%s
ReadWritePaths=-/usr/local/bin

[Install]
WantedBy=multi-user.target
`, binaryPath, orgName, appName, orgName, appName, orgName, appName, orgName, appName, orgName, appName,
		orgName, appName, orgName, appName, orgName, appName, orgName, appName, orgName, appName, orgName, appName)
}

func installSysV() error {
	servicePath := filepath.Join("/etc/init.d", appName)
	binaryPath := GetBinaryPath()
	content := fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          %s
# Required-Start:    $network $remote_fs $syslog
# Required-Stop:     $network $remote_fs $syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
### END INIT INFO
NAME=%s
DAEMON=%s
DAEMON_USER=%s
PIDFILE=/var/run/%s/%s/%s.pid
LOGFILE=/var/log/%s/%s/server.log
case "$1" in
 start) mkdir -p "$(dirname "$PIDFILE")" "$(dirname "$LOGFILE")" || exit 1; chown -R "$DAEMON_USER:$DAEMON_USER" "$(dirname "$PIDFILE")" "$(dirname "$LOGFILE")" || exit 1; start-stop-daemon --start --quiet --background --make-pidfile --pidfile "$PIDFILE" --exec "$DAEMON" --no-close >>"$LOGFILE" 2>&1 ;;
 stop) start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --retry 30; result=$?; rm -f "$PIDFILE"; exit "$result" ;;
 restart) "$0" stop && sleep 1 && "$0" start ;;
 status) [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null ;;
 *) echo "Usage: $0 {start|stop|restart|status}"; exit 1 ;;
esac
`, appName, appName, binaryPath, appName, orgName, appName, appName, orgName, appName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		return fmt.Errorf("create SysV init directory: %w", err)
	}
	if err := os.WriteFile(servicePath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("write SysV service: %w", err)
	}
	if err := installBinary(binaryPath); err != nil {
		return err
	}
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		if err := exec.Command("update-rc.d", appName, "defaults").Run(); err != nil {
			return fmt.Errorf("enable SysV service: %w", err)
		}
	} else {
		if err := exec.Command("chkconfig", "--add", appName).Run(); err != nil {
			return fmt.Errorf("add SysV service: %w", err)
		}
		if err := exec.Command("chkconfig", appName, "on").Run(); err != nil {
			return fmt.Errorf("enable SysV service: %w", err)
		}
	}
	if err := exec.Command(servicePath, "start").Run(); err != nil {
		return fmt.Errorf("start SysV service: %w", err)
	}
	return nil
}

func uninstallSysV() error {
	if err := exec.Command(filepath.Join("/etc/init.d", appName), "stop").Run(); err != nil {
		return fmt.Errorf("stop SysV service: %w", err)
	}
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		if err := exec.Command("update-rc.d", "-f", appName, "remove").Run(); err != nil {
			return fmt.Errorf("disable SysV service: %w", err)
		}
	} else if err := exec.Command("chkconfig", "--del", appName).Run(); err != nil {
		return fmt.Errorf("disable SysV service: %w", err)
	}
	if err := os.Remove(filepath.Join("/etc/init.d", appName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove SysV service: %w", err)
	}
	return nil
}

func installS6() error {
	scanDir, found := findS6ScanDir("/proc", "/run/service", os.ReadDir, os.ReadFile)
	if !found {
		return fmt.Errorf("s6-svscan process or scan directory was not found")
	}
	return installS6With("/etc/s6/sv", scanDir, GetBinaryPath(), defaultS6Ops(), runServiceCommand)
}

type s6ServiceOps struct {
	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
	symlink   func(string, string) error
	readlink  func(string) (string, error)
	remove    func(string) error
	removeAll func(string) error
}

func defaultS6Ops() s6ServiceOps {
	return s6ServiceOps{os.MkdirAll, os.WriteFile, os.Symlink, os.Readlink, os.Remove, os.RemoveAll}
}

func runServiceCommand(name string, args ...string) error { return exec.Command(name, args...).Run() }

func installS6With(serviceRoot, scanDir, binaryPath string, fs s6ServiceOps, command func(string, ...string) error) error {
	serviceDir := filepath.Join(serviceRoot, appName)
	logDir := filepath.Join(serviceDir, "log")
	if err := fs.mkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create s6 service directory: %w", err)
	}
	runScript := fmt.Sprintf("#!/bin/sh\nexec %s 2>&1\n", binaryPath)
	if err := fs.writeFile(filepath.Join(serviceDir, "run"), []byte(runScript), 0o755); err != nil {
		return fmt.Errorf("write s6 run script: %w", err)
	}
	logScript := fmt.Sprintf("#!/bin/sh\nexec s6-log n10 s1000000 /var/log/%s/%s\n", orgName, appName)
	if err := fs.writeFile(filepath.Join(logDir, "run"), []byte(logScript), 0o755); err != nil {
		return fmt.Errorf("write s6 log script: %w", err)
	}
	linkPath := filepath.Join(scanDir, appName)
	if err := fs.symlink(serviceDir, linkPath); err != nil {
		if target, readErr := fs.readlink(linkPath); readErr != nil || target != serviceDir {
			return fmt.Errorf("enable s6 service: %w", err)
		}
	}
	if err := command("s6-svscanctl", "-a", scanDir); err != nil {
		return fmt.Errorf("rescan s6 services: %w", err)
	}
	if err := command("s6-svc", "-u", linkPath); err != nil {
		return fmt.Errorf("start s6 service: %w", err)
	}
	return nil
}

func s6ScanDir() (string, error) {
	scanDir, found := findS6ScanDir("/proc", "/run/service", os.ReadDir, os.ReadFile)
	if !found {
		return "", fmt.Errorf("s6-svscan process or scan directory was not found")
	}
	return scanDir, nil
}

func disableS6With(serviceRoot, scanDir string, fs s6ServiceOps, command func(string, ...string) error) error {
	linkPath := filepath.Join(scanDir, appName)
	if err := command("s6-svc", "-d", linkPath); err != nil {
		return fmt.Errorf("stop s6 service: %w", err)
	}
	if err := fs.remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disable s6 service: %w", err)
	}
	if err := command("s6-svscanctl", "-a", scanDir); err != nil {
		return fmt.Errorf("rescan s6 services: %w", err)
	}
	return nil
}

func uninstallS6() error {
	scanDir, err := s6ScanDir()
	if err != nil {
		return err
	}
	fs := defaultS6Ops()
	if err := disableS6With("/etc/s6/sv", scanDir, fs, runServiceCommand); err != nil {
		return err
	}
	if err := fs.removeAll(filepath.Join("/etc/s6/sv", appName)); err != nil {
		return fmt.Errorf("remove s6 service definition: %w", err)
	}
	return nil
}

func installBinary(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate server binary: %w", err)
	}
	if source != destination {
		if err := copyBinary(source, destination); err != nil {
			return fmt.Errorf("install server binary: %w", err)
		}
	}
	return nil
}

// uninstallSystemd removes systemd service
func uninstallSystemd() error {
	serviceDir := "/etc/systemd/system"
	if !isSystemInstall() {
		current, err := user.Current()
		if err != nil {
			return fmt.Errorf("determine systemd user: %w", err)
		}
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(current.HomeDir, ".config")
		}
		serviceDir = filepath.Join(configDir, "systemd", "user")
	}
	servicePath := filepath.Join(serviceDir, appName+".service")

	if err := systemdCommand("stop", appName).Run(); err != nil {
		return fmt.Errorf("stop systemd service: %w", err)
	}
	if err := systemdCommand("disable", appName).Run(); err != nil {
		return fmt.Errorf("disable systemd service: %w", err)
	}

	// Remove service file
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file: %w", err)
	}

	// Reload systemd
	if err := systemdCommand("daemon-reload").Run(); err != nil {
		return fmt.Errorf("reload systemd configuration: %w", err)
	}

	fmt.Printf("✅ Service uninstalled: %s\n", servicePath)
	return nil
}

// installRunit creates runit service
func installRunit() error {
	svDir := fmt.Sprintf("/etc/sv/%s", appName)
	binaryPath := GetBinaryPath()

	// Create service directory
	if err := os.MkdirAll(svDir, 0o755); err != nil {
		return fmt.Errorf("failed to create service directory: %w", err)
	}

	runScript := fmt.Sprintf(`#!/bin/sh
exec %s 2>&1
`, binaryPath)

	runPath := filepath.Join(svDir, "run")
	if err := os.WriteFile(runPath, []byte(runScript), 0o755); err != nil {
		return fmt.Errorf("failed to write run script: %w", err)
	}

	// Create log directory
	logDir := filepath.Join(svDir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logRunScript := `#!/bin/sh
exec svlogd -tt /var/log/apimgr/gitmessages
`
	logRunPath := filepath.Join(logDir, "run")
	if err := os.WriteFile(logRunPath, []byte(logRunScript), 0o755); err != nil {
		return fmt.Errorf("failed to write log run script: %w", err)
	}

	// Link to service directory
	linkPath := fmt.Sprintf("/var/service/%s", appName)
	if err := installBinary(binaryPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("create runit service directory: %w", err)
	}
	if err := os.Symlink(svDir, linkPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("enable runit service: %w", err)
	}
	if err := exec.Command("sv", "up", appName).Run(); err != nil {
		return fmt.Errorf("start runit service: %w", err)
	}

	fmt.Printf("✅ Runit service installed at: %s\n", svDir)
	return nil
}

// uninstallRunit removes runit service
func uninstallRunit() error {
	svDir := fmt.Sprintf("/etc/sv/%s", appName)
	linkPath := fmt.Sprintf("/var/service/%s", appName)

	// Stop service
	if err := exec.Command("sv", "down", appName).Run(); err != nil {
		return fmt.Errorf("stop runit service: %w", err)
	}

	// Remove link
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disable runit service: %w", err)
	}

	// Remove service directory
	if err := os.RemoveAll(svDir); err != nil {
		return fmt.Errorf("remove runit service files: %w", err)
	}

	fmt.Printf("✅ Runit service uninstalled\n")
	return nil
}

// installLaunchd creates macOS launchd plist
func installLaunchd() error {
	binaryPath := GetBinaryPath()
	plistPath := fmt.Sprintf("/Library/LaunchDaemons/com.%s.%s.plist", orgName, appName)
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine launchd install user: %w", err)
	}
	userInstall := current.Uid != "0"
	logDir := filepath.Join("/Library/Logs", orgName, appName)
	label := fmt.Sprintf("com.%s.%s", orgName, appName)
	if userInstall {
		binaryPath = filepath.Join(current.HomeDir, ".local", "bin", appName)
		plistPath = filepath.Join(current.HomeDir, "Library", "LaunchAgents", label+".plist")
		logDir = filepath.Join(current.HomeDir, "Library", "Logs", orgName, appName)
	}

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>%s/error.log</string>
    <key>StandardOutPath</key>
    <string>%s/output.log</string>
</dict>
</plist>
`, label, binaryPath, filepath.Join(logDir, "error.log"), filepath.Join(logDir, "output.log"))

	// Create directories
	dirs := []string{
		fmt.Sprintf("/Library/Application Support/%s/%s", orgName, appName),
		logDir,
	}
	if userInstall {
		dirs = []string{filepath.Dir(plistPath), filepath.Dir(binaryPath), logDir}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write plist file
	if err := os.WriteFile(plistPath, []byte(plistContent), 0o644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	if err := installBinary(binaryPath); err != nil {
		return err
	}
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("load launchd service: %w", err)
	}
	if err := exec.Command("launchctl", "start", label).Run(); err != nil {
		return fmt.Errorf("start launchd service: %w", err)
	}

	fmt.Printf("✅ LaunchDaemon installed at: %s\n", plistPath)
	fmt.Println()
	fmt.Println("To load the service:")
	fmt.Printf("  sudo launchctl load %s\n", plistPath)

	return nil
}

// uninstallLaunchd removes macOS launchd plist
func uninstallLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return fmt.Errorf("locate launchd service: %w", err)
	}
	if err := exec.Command("launchctl", "unload", plistPath).Run(); err != nil {
		return fmt.Errorf("unload launchd service: %w", err)
	}

	// Remove plist
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist file: %w", err)
	}

	fmt.Printf("✅ LaunchDaemon uninstalled\n")
	return nil
}

// installWindows creates Windows service
func installWindows() error {
	binaryPath := GetBinaryPath()

	// Copy binary
	binDir := filepath.Dir(binaryPath)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	// Create service using sc.exe
	displayName := capitalize(appName) + " API"
	cmd := exec.Command("sc.exe", "create", appName,
		"binPath=", binaryPath,
		"DisplayName=", displayName,
		"start=", "auto")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create Windows service: %w", err)
	}
	if err := exec.Command("sc.exe", "config", appName, "obj=", "NT SERVICE\\"+appName).Run(); err != nil {
		return fmt.Errorf("configure Windows virtual service account: %w", err)
	}
	if err := exec.Command("sc.exe", "start", appName).Run(); err != nil {
		return fmt.Errorf("start Windows service: %w", err)
	}

	fmt.Printf("✅ Windows service '%s' installed\n", appName)
	fmt.Println()
	fmt.Println("To start the service:")
	fmt.Printf("  sc.exe start %s\n", appName)

	return nil
}

// uninstallWindows removes Windows service
func uninstallWindows() error {
	// Stop service
	if err := exec.Command("sc.exe", "stop", appName).Run(); err != nil {
		return fmt.Errorf("stop Windows service: %w", err)
	}

	// Delete service
	if err := exec.Command("sc.exe", "delete", appName).Run(); err != nil {
		return fmt.Errorf("failed to delete Windows service: %w", err)
	}

	fmt.Printf("✅ Windows service '%s' uninstalled\n", appName)
	return nil
}

// installBSDRC creates BSD rc.d script
func installBSDRC() error {
	binaryPath := GetBinaryPath()
	rcPath := fmt.Sprintf("/usr/local/etc/rc.d/%s", appName)

	rcContent := fmt.Sprintf(`#!/bin/sh

# PROVIDE: %s
# REQUIRE: NETWORKING
# KEYWORD: shutdown

. /etc/rc.subr

name="%s"
rcvar="%s_enable"
pidfile="/var/run/%s/%s/%s.pid"
command="/usr/sbin/daemon"
command_args="-P ${pidfile} -r -f %s"
start_precmd="%s_prestart"

%s_prestart()
{
    install -d -m 0755 /var/run/%s/%s /var/log/%s/%s
}

load_rc_config $name
run_rc_command "$1"
`, appName, appName, appName, orgName, appName, appName, binaryPath, appName, appName, orgName, appName, orgName, appName)
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		return fmt.Errorf("create rc.d directory: %w", err)
	}

	if err := os.WriteFile(rcPath, []byte(rcContent), 0o755); err != nil {
		return fmt.Errorf("failed to write rc.d script: %w", err)
	}

	if err := installBinary(binaryPath); err != nil {
		return err
	}
	if err := exec.Command("sysrc", appName+"_enable=YES").Run(); err != nil {
		return fmt.Errorf("enable BSD service: %w", err)
	}
	if err := exec.Command("service", appName, "start").Run(); err != nil {
		return fmt.Errorf("start BSD service: %w", err)
	}

	fmt.Printf("✅ BSD rc.d script installed at: %s\n", rcPath)
	fmt.Println()
	fmt.Printf("Add '%s_enable=\"YES\"' to /etc/rc.conf\n", appName)
	fmt.Println()
	fmt.Println("To start the service:")
	fmt.Printf("  service %s start\n", appName)

	return nil
}

// uninstallBSDRC removes BSD rc.d script
func uninstallBSDRC() error {
	rcPath := fmt.Sprintf("/usr/local/etc/rc.d/%s", appName)

	// Stop service
	if err := exec.Command("service", appName, "stop").Run(); err != nil {
		return fmt.Errorf("stop BSD service: %w", err)
	}
	if err := exec.Command("sysrc", "-x", appName+"_enable").Run(); err != nil {
		return fmt.Errorf("disable BSD service: %w", err)
	}

	// Remove script
	if err := os.Remove(rcPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove rc.d script: %w", err)
	}

	fmt.Printf("✅ BSD rc.d script uninstalled\n")
	return nil
}

// copyBinary copies the binary to the destination
func copyBinary(src, dst string) error {
	// Create destination directory if needed
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	// Read source
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Write to destination
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return err
	}

	return nil
}

// Start starts the service
func Start() error {
	serviceType := DetectServiceManager()

	switch serviceType {
	case ServiceSystemd:
		return systemdCommand("start", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "start").Run()
	case ServiceRunit:
		return exec.Command("sv", "start", appName).Run()
	case ServiceS6:
		scanDir, err := s6ScanDir()
		if err != nil {
			return err
		}
		return runServiceCommand("s6-svc", "-u", filepath.Join(scanDir, appName))
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "start").Run()
	case ServiceLaunchd:
		plistPath, err := launchdPlistPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "load", plistPath).Run()
	case ServiceWindows:
		return exec.Command("sc.exe", "start", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "start").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Stop stops the service
func Stop() error {
	serviceType := DetectServiceManager()

	switch serviceType {
	case ServiceSystemd:
		return systemdCommand("stop", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "stop").Run()
	case ServiceRunit:
		return exec.Command("sv", "stop", appName).Run()
	case ServiceS6:
		scanDir, err := s6ScanDir()
		if err != nil {
			return err
		}
		return runServiceCommand("s6-svc", "-d", filepath.Join(scanDir, appName))
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "stop").Run()
	case ServiceLaunchd:
		plistPath, err := launchdPlistPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "unload", plistPath).Run()
	case ServiceWindows:
		return exec.Command("sc.exe", "stop", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "stop").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Restart restarts the service
func Restart() error {
	serviceType := DetectServiceManager()

	switch serviceType {
	case ServiceSystemd:
		return systemdCommand("restart", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "restart").Run()
	case ServiceRunit:
		return exec.Command("sv", "restart", appName).Run()
	case ServiceS6:
		scanDir, err := s6ScanDir()
		if err != nil {
			return err
		}
		return runServiceCommand("s6-svc", "-r", filepath.Join(scanDir, appName))
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "restart").Run()
	case ServiceLaunchd:
		if err := Stop(); err != nil {
			return err
		}
		return Start()
	case ServiceWindows:
		_ = exec.Command("sc.exe", "stop", appName).Run()
		return exec.Command("sc.exe", "start", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "restart").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Reload sends reload signal to the service
func Reload() error {
	serviceType := DetectServiceManager()

	switch serviceType {
	case ServiceSystemd:
		return systemdCommand("reload", appName).Run()
	case ServiceRunit:
		return exec.Command("sv", "hup", appName).Run()
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "restart").Run()
	default:
		// For others, restart is the fallback
		return Restart()
	}
}

// Disable stops the service and removes it from automatic startup while preserving its files.
func Disable() error {
	serviceType := DetectServiceManager()
	if serviceType != ServiceS6 {
		if err := Stop(); err != nil {
			return fmt.Errorf("stop service before disabling: %w", err)
		}
	}
	switch serviceType {
	case ServiceSystemd:
		return systemdCommand("disable", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-update", "del", appName, "default").Run()
	case ServiceRunit:
		if err := exec.Command("sv", "down", appName).Run(); err != nil {
			return fmt.Errorf("stop runit service: %w", err)
		}
		if err := os.Remove(fmt.Sprintf("/var/service/%s", appName)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("disable runit service: %w", err)
		}
		return nil
	case ServiceS6:
		scanDir, err := s6ScanDir()
		if err != nil {
			return err
		}
		return disableS6With("/etc/s6/sv", scanDir, defaultS6Ops(), runServiceCommand)
	case ServiceSysV:
		if _, err := exec.LookPath("update-rc.d"); err == nil {
			return exec.Command("update-rc.d", "-f", appName, "remove").Run()
		}
		if err := exec.Command("chkconfig", appName, "off").Run(); err != nil {
			return fmt.Errorf("disable SysV service: %w", err)
		}
		return nil
	case ServiceLaunchd:
		return nil
	case ServiceWindows:
		return exec.Command("sc.exe", "config", appName, "start=", "demand").Run()
	case ServiceBSDRC:
		return exec.Command("sysrc", "-x", appName+"_enable").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Status returns the service manager's current status for the project service.
func Status() error {
	switch DetectServiceManager() {
	case ServiceSystemd:
		return systemdCommand("status", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "status").Run()
	case ServiceRunit:
		return exec.Command("sv", "status", appName).Run()
	case ServiceS6:
		scanDir, err := s6ScanDir()
		if err != nil {
			return err
		}
		return runServiceCommand("s6-svstat", filepath.Join(scanDir, appName))
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "status").Run()
	case ServiceLaunchd:
		return exec.Command("launchctl", "list", fmt.Sprintf("com.%s.%s", orgName, appName)).Run()
	case ServiceWindows:
		return exec.Command("sc.exe", "query", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "status").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}
