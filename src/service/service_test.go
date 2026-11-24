package service

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func managerDetector(paths, commands map[string]bool) (func(string) (os.FileInfo, error), func(string) (string, error)) {
	stat := func(path string) (os.FileInfo, error) {
		if paths[path] {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	lookPath := func(name string) (string, error) {
		if commands[name] {
			return filepath.Join("/mock/bin", name), nil
		}
		return "", errors.New("not found")
	}
	return stat, lookPath
}

func TestSystemdUnitSeparatesUserAndSystemServices(t *testing.T) {
	userUnit := systemdUnit("/tmp/home/.local/bin/gitmessages", true)
	if !strings.Contains(userUnit, "WantedBy=default.target") || !strings.Contains(userUnit, "ExecStart=/tmp/home/.local/bin/gitmessages") {
		t.Fatalf("user unit is missing its user service target or executable: %s", userUnit)
	}
	for _, forbidden := range []string{"ExecStartPre=", "/etc/apimgr", "/var/lib/apimgr", "/usr/local/bin"} {
		if strings.Contains(userUnit, forbidden) {
			t.Errorf("user unit contains system-only path or setup %q", forbidden)
		}
	}
	systemUnit := systemdUnit("/usr/local/bin/gitmessages", false)
	for _, required := range []string{"ExecStartPre=+", "ProtectSystem=true", "ProtectHome=yes", "ReadWritePaths=-/var/run/apimgr/gitmessages", "WantedBy=multi-user.target"} {
		if !strings.Contains(systemUnit, required) {
			t.Errorf("system unit missing required directive %q", required)
		}
	}
}

func TestDetectServiceManagerLinuxPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		paths    map[string]bool
		commands map[string]bool
		want     ServiceType
	}{
		{"systemd wins", map[string]bool{"/run/systemd/system": true, "/sbin/openrc-run": true, "/run/openrc": true}, map[string]bool{"sv": true}, ServiceSystemd},
		{"openrc wins over sysv", map[string]bool{"/sbin/openrc-run": true, "/run/openrc": true, "/etc/init.d": true}, map[string]bool{"update-rc.d": true}, ServiceOpenRC},
		{"runit by path", map[string]bool{"/etc/runit": true}, map[string]bool{}, ServiceRunit},
		{"runit by command before s6", map[string]bool{}, map[string]bool{"sv": true, "s6-svc": true}, ServiceRunit},
		{"sysv only with enable tool", map[string]bool{"/etc/init.d": true}, map[string]bool{"chkconfig": true}, ServiceSysV},
		{"no manager", map[string]bool{"/etc/systemd": true}, map[string]bool{}, ServiceUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stat, lookPath := managerDetector(tt.paths, tt.commands)
			if got := detectServiceManager("linux", stat, lookPath); got != tt.want {
				t.Fatalf("detector returned %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenRCServiceUsesSeparateSupervisorPIDFile(t *testing.T) {
	content := openRCServiceContent("/usr/local/bin/gitmessages")
	if !strings.Contains(content, `pidfile="/var/run/apimgr/gitmessages/gitmessages.service.pid"`) {
		t.Fatalf("OpenRC service file does not use a separate supervisor PID file: %s", content)
	}
	if !strings.Contains(content, "command_background=true") {
		t.Fatalf("OpenRC service file no longer backgrounds the supervised process: %s", content)
	}
}

func TestDetectS6RequiresScanner(t *testing.T) {
	stat, lookPath := managerDetector(nil, map[string]bool{"s6-svc": true})
	if got := detectServiceManagerWith("linux", stat, lookPath, false); got != ServiceUnknown {
		t.Fatalf("without s6-svscan, detector returned %v", got)
	}
	if got := detectServiceManagerWith("linux", stat, lookPath, true); got != ServiceS6 {
		t.Fatalf("with s6-svscan, detector returned %v", got)
	}
}

func TestFindS6ScanDirUsesProcAndFallback(t *testing.T) {
	root := projectTestDir(t)
	proc := filepath.Join(root, "proc")
	pidDir := filepath.Join(proc, "42")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("/usr/bin/s6-svscan\x00/run/s6\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, found := findS6ScanDir(proc, filepath.Join(root, "service"), os.ReadDir, os.ReadFile)
	if !found || dir != "/run/s6" {
		t.Fatalf("scan dir = %q, found=%t", dir, found)
	}
	fallback := filepath.Join(root, "fallback")
	dir, found = findS6ScanDir(filepath.Join(root, "missing-proc"), fallback, func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }, os.ReadFile)
	if !found || dir != fallback {
		t.Fatalf("fallback scan dir = %q, found=%t", dir, found)
	}
}

func TestS6InstallDisableUninstallHelpersUseInjectedOps(t *testing.T) {
	root := projectTestDir(t)
	serviceRoot := filepath.Join(root, "etc", "s6", "sv")
	scanDir := filepath.Join(root, "run", "service")
	if err := os.MkdirAll(scanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	command := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	ops := defaultS6Ops()
	if err := installS6With(serviceRoot, scanDir, filepath.Join(root, "bin", appName), ops, command); err != nil {
		t.Fatal(err)
	}
	serviceDir := filepath.Join(serviceRoot, appName)
	runScript, err := os.ReadFile(filepath.Join(serviceDir, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if string(runScript) != "#!/bin/sh\nexec "+filepath.Join(root, "bin", appName)+" 2>&1\n" {
		t.Fatalf("unexpected s6 run script: %s", runScript)
	}
	logScript, err := os.ReadFile(filepath.Join(serviceDir, "log", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if string(logScript) != "#!/bin/sh\nexec s6-log n10 s1000000 /var/log/apimgr/gitmessages\n" {
		t.Fatalf("unexpected s6 log script: %s", logScript)
	}
	linkPath := filepath.Join(scanDir, appName)
	if target, err := os.Readlink(linkPath); err != nil || target != serviceDir {
		t.Fatalf("s6 link target = %q, err=%v", target, err)
	}
	wantInstallCalls := [][]string{{"s6-svscanctl", "-a", scanDir}, {"s6-svc", "-u", linkPath}}
	if !reflect.DeepEqual(calls, wantInstallCalls) {
		t.Fatalf("install commands = %#v, want %#v", calls, wantInstallCalls)
	}
	calls = nil
	if err := disableS6With(serviceRoot, scanDir, ops, command); err != nil {
		t.Fatal(err)
	}
	wantDisableCalls := [][]string{{"s6-svc", "-d", linkPath}, {"s6-svscanctl", "-a", scanDir}}
	if !reflect.DeepEqual(calls, wantDisableCalls) {
		t.Fatalf("disable commands = %#v, want %#v", calls, wantDisableCalls)
	}
	if err := ops.removeAll(serviceDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(serviceDir); !os.IsNotExist(err) {
		t.Fatalf("service definition remains after uninstall helper: %v", err)
	}
}

func TestDetectServiceManagerPlatforms(t *testing.T) {
	stat, lookPath := managerDetector(nil, nil)
	for _, tt := range []struct {
		goos string
		want ServiceType
	}{
		{"darwin", ServiceLaunchd}, {"freebsd", ServiceBSDRC}, {"openbsd", ServiceBSDRC}, {"netbsd", ServiceBSDRC}, {"windows", ServiceWindows}, {"plan9", ServiceUnknown},
	} {
		if got := detectServiceManager(tt.goos, stat, lookPath); got != tt.want {
			t.Errorf("detectServiceManager(%q) = %v, want %v", tt.goos, got, tt.want)
		}
	}
}

func TestGetBinaryPath(t *testing.T) {
	path := GetBinaryPath()
	if runtime.GOOS == "windows" {
		if filepath.Ext(path) != ".exe" {
			t.Fatalf("Windows binary path %q must end in .exe", path)
		}
	} else if filepath.Base(path) != appName {
		t.Fatalf("binary path %q must end in %q", path, appName)
	}
}

func TestCopyBinaryUsesTemporaryPaths(t *testing.T) {
	dir := projectTestDir(t)
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "nested", "installed")
	if err := os.WriteFile(source, []byte("test binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyBinary(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "test binary" {
		t.Fatalf("copy contents %q", got)
	}
}

func TestS6InstallCommandFailuresStopLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name      string
		failOn    string
		wantCalls [][]string
	}{
		{"rescan", "s6-svscanctl", [][]string{{"s6-svscanctl", "-a", "scan"}}},
		{"start", "s6-svc", [][]string{{"s6-svscanctl", "-a", "scan"}, {"s6-svc", "-u", filepath.Join("scan", appName)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := projectTestDir(t)
			scanDir := filepath.Join(root, "scan")
			if err := os.MkdirAll(scanDir, 0o755); err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			commandErr := errors.New("command failed")
			command := func(name string, args ...string) error {
				calls = append(calls, append([]string{name}, args...))
				if name == tt.failOn {
					return commandErr
				}
				return nil
			}
			err := installS6With(filepath.Join(root, "services"), scanDir, filepath.Join(root, "bin", appName), defaultS6Ops(), command)
			if !errors.Is(err, commandErr) {
				t.Fatalf("install error = %v, want wrapped command error", err)
			}
			wantCalls := make([][]string, len(tt.wantCalls))
			for index, expected := range tt.wantCalls {
				wantCalls[index] = append([]string(nil), expected...)
				for argumentIndex, argument := range wantCalls[index] {
					if argument == "scan" {
						wantCalls[index][argumentIndex] = scanDir
					} else if argument == filepath.Join("scan", appName) {
						wantCalls[index][argumentIndex] = filepath.Join(scanDir, appName)
					}
				}
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("commands after %s failure = %#v, want %#v", tt.failOn, calls, wantCalls)
			}
		})
	}
}

func TestS6DisableFailurePreservesEnabledLink(t *testing.T) {
	root := projectTestDir(t)
	serviceRoot := filepath.Join(root, "services")
	scanDir := filepath.Join(root, "scan")
	if err := os.MkdirAll(scanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installS6With(serviceRoot, scanDir, filepath.Join(root, "bin", appName), defaultS6Ops(), func(string, ...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	commandErr := errors.New("stop failed")
	var calls [][]string
	err := disableS6With(serviceRoot, scanDir, defaultS6Ops(), func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return commandErr
	})
	if !errors.Is(err, commandErr) {
		t.Fatalf("disable error = %v, want wrapped stop error", err)
	}
	linkPath := filepath.Join(scanDir, appName)
	if target, err := os.Readlink(linkPath); err != nil || target != filepath.Join(serviceRoot, appName) {
		t.Fatalf("service link after failed stop = %q, err=%v", target, err)
	}
	wantCalls := [][]string{{"s6-svc", "-d", linkPath}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("commands after failed stop = %#v, want %#v", calls, wantCalls)
	}
}

func TestS6DisableIsIdempotentWhenAlreadyDisabled(t *testing.T) {
	root := projectTestDir(t)
	serviceRoot := filepath.Join(root, "services")
	scanDir := filepath.Join(root, "scan")
	if err := os.MkdirAll(scanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	command := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	if err := disableS6With(serviceRoot, scanDir, defaultS6Ops(), command); err != nil {
		t.Fatalf("disable missing service link: %v", err)
	}
	if err := disableS6With(serviceRoot, scanDir, defaultS6Ops(), command); err != nil {
		t.Fatalf("repeat disable: %v", err)
	}
	wantCalls := [][]string{
		{"s6-svc", "-d", filepath.Join(scanDir, appName)},
		{"s6-svscanctl", "-a", scanDir},
		{"s6-svc", "-d", filepath.Join(scanDir, appName)},
		{"s6-svscanctl", "-a", scanDir},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("disable commands = %#v, want %#v", calls, wantCalls)
	}
}
