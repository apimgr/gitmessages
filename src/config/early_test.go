package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEarlyServerSettingsUsesOrderedDropIns(t *testing.T) {
	directory := projectTestDir(t)
	if err := os.Mkdir(filepath.Join(directory, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.yml"), []byte("server:\n  port: \"80\"\n  address: \"127.0.0.1\"\n  daemonize: false\n  token: ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"10-port.yml":   "server:\n  port: \"8080,443\"\n",
		"20-daemon.yml": "server:\n  daemonize: true\n  pidfile: false\n  token: ignored-too\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, "yaml.d", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	port, address, daemonize, pidfile := EarlyServerSettings(filepath.Join(directory, "server.yml"))
	if port != "8080,443" || address != "127.0.0.1" || !daemonize || pidfile {
		t.Fatalf("EarlyServerSettings() = (%q, %q, %v, %v)", port, address, daemonize, pidfile)
	}
}

func TestEarlyServerSettingsSkipsMalformedFiles(t *testing.T) {
	directory := projectTestDir(t)
	if err := os.Mkdir(filepath.Join(directory, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.yml"), []byte("server:\n  port: \"80\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "yaml.d", "10-invalid.yml"), []byte("server: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, address, daemonize, pidfile := EarlyServerSettings(filepath.Join(directory, "server.yml"))
	if port != "80" || address != "" || daemonize || !pidfile {
		t.Fatalf("EarlyServerSettings() = (%q, %q, %v, %v)", port, address, daemonize, pidfile)
	}
}

func TestEarlyBaseURLUsesOrderedDropIns(t *testing.T) {
	directory := projectTestDir(t)
	if err := os.Mkdir(filepath.Join(directory, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.yml"), []byte("server:\n  baseurl: /root\n  token: ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "yaml.d", "20-prefix.yml"), []byte("server:\n  baseurl: /messages\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := EarlyBaseURL(filepath.Join(directory, "server.yml")); got != "/messages" {
		t.Fatalf("EarlyBaseURL() = %q, want /messages", got)
	}
}

func TestIsSystemManagerHost(t *testing.T) {
	tests := []struct {
		name          string
		invocationID  string
		rcServiceName string
		pid1          string
		openRCRunning bool
		want          bool
	}{
		{name: "systemd service", invocationID: "service-id", want: true},
		{name: "OpenRC service", rcServiceName: "gitmessages", want: true},
		{name: "systemd system container", pid1: "systemd", want: true},
		{name: "OpenRC init process", pid1: "openrc-init", want: true},
		{name: "OpenRC system container", pid1: "init", openRCRunning: true, want: true},
		{name: "openrc marker without OpenRC init", openRCRunning: true, want: false},
		{name: "application container", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSystemManagerHost(test.invocationID, test.rcServiceName, test.pid1, test.openRCRunning); got != test.want {
				t.Errorf("isSystemManagerHost() = %v, want %v", got, test.want)
			}
		})
	}
}
