package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
)

func TestSendOperatorEmailSkipsWhenSMTPUnavailableOrEventDisabled(t *testing.T) {
	configuration := config.DefaultConfig()
	if err := sendOperatorEmail(context.Background(), configuration, "scheduler_error", "operator@example.test", map[string]string{"task_name": "cleanup"}); err != nil {
		t.Fatalf("disabled SMTP attempted delivery: %v", err)
	}
	configuration.Server.Notifications.Email.Enabled = true
	configuration.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
	configuration.Server.Notifications.Email.SMTP.Port = 1
	configuration.Server.Notifications.Email.Events["scheduler_error"] = false
	if err := sendOperatorEmail(context.Background(), configuration, "scheduler_error", "operator@example.test", map[string]string{"task_name": "cleanup"}); err != nil {
		t.Fatalf("disabled event attempted delivery: %v", err)
	}
}

func TestEmailCommandDryRunRendersEveryTemplateWithoutSMTP(t *testing.T) {
	configuration := config.DefaultConfig()
	var output bytes.Buffer
	if err := runEmailCommand([]string{"test", "--dry-run"}, configuration, emailTestDirectory(t), nil, &output); err != nil {
		t.Fatalf("dry-run template preview failed: %v", err)
	}
	for _, name := range []string{"security_alert", "backup_failed", "ssl_renewal_failed", "scheduler_error", "disk_low", "test"} {
		if !strings.Contains(output.String(), "Template: "+name+"\n") {
			t.Errorf("dry-run output omitted template %q", name)
		}
	}
}

func TestEmailCommandRequiresWorkingSMTPForRealSend(t *testing.T) {
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.Email.ReplyTo = "operator@example.test"
	if err := runEmailCommand([]string{"test"}, configuration, emailTestDirectory(t), nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "require SMTP") {
		t.Fatalf("real test email without SMTP error = %v", err)
	}
}

func TestEmailCommandValidatesArgumentsAndConfiguration(t *testing.T) {
	directory := emailTestDirectory(t)
	for _, test := range []struct {
		name          string
		args          []string
		configuration *config.Config
		want          string
	}{
		{name: "missing command", want: "usage:"},
		{name: "wrong command", args: []string{"send"}, configuration: config.DefaultConfig(), want: "usage:"},
		{name: "invalid flag", args: []string{"test", "--invalid"}, configuration: config.DefaultConfig(), want: "flag provided but not defined"},
		{name: "positional argument", args: []string{"test", "extra"}, configuration: config.DefaultConfig(), want: "does not accept positional"},
		{name: "missing configuration", args: []string{"test", "--dry-run"}, want: "configuration is required"},
		{name: "missing recipient", args: []string{"test"}, configuration: func() *config.Config {
			configuration := config.DefaultConfig()
			configuration.Server.Notifications.Email.Enabled = true
			configuration.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
			configuration.Server.Notifications.Email.SMTP.Port = 25
			return configuration
		}(), want: "set server.notifications.email.reply_to"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runEmailCommand(test.args, test.configuration, directory, nil, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runEmailCommand() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func emailTestDirectory(t *testing.T) string {
	return maintenanceTestDir(t)
}
