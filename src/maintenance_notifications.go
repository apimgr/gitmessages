package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/email"
	"github.com/apimgr/gitmessages/src/paths"
)

// sendOperatorEmail sends an event only when SMTP is healthy and that event is enabled.
func sendOperatorEmail(ctx context.Context, configuration *config.Config, event, recipient string, values map[string]string) error {
	if configuration == nil || event == "" {
		return errors.New("notification configuration and event are required")
	}
	settings := configuration.Server.Notifications.Email
	if !settings.Enabled || strings.TrimSpace(settings.SMTP.Host) == "" || settings.SMTP.Port < 1 {
		return nil
	}
	if event != "disk_low" && event != "test" && !settings.Events[event] {
		return nil
	}
	if strings.TrimSpace(recipient) == "" {
		if event == "security_alert" && strings.TrimSpace(configuration.Server.Contact.Security.Email) != "" {
			recipient = configuration.Server.Contact.Security.Email
		} else {
			recipient = configuration.Server.Contact.Admin.Email
		}
	}
	if strings.TrimSpace(recipient) == "" {
		return nil
	}
	appName := strings.TrimSpace(configuration.Server.Branding.Title)
	if appName == "" {
		appName = paths.ProjectName
	}
	fqdn := strings.TrimSpace(configuration.Server.FQDN)
	if fqdn == "" {
		fqdn = "localhost"
	}
	fromName := strings.TrimSpace(settings.From.Name)
	if fromName == "" {
		fromName = appName
	}
	fromAddress := strings.TrimSpace(settings.From.Email)
	if fromAddress == "" {
		fromAddress = "no-reply@" + fqdn
	}
	variables := make(map[string]string, len(values)+6)
	for key, value := range values {
		variables[key] = value
	}
	variables["app_name"] = appName
	variables["fqdn"] = fqdn
	variables["app_url"] = "https://" + fqdn
	variables["notification_reply_to"] = settings.ReplyTo
	manager := email.NewTemplateManager(filepath.Join(config.ConfigDirectory(), "template", "email"))
	message, err := manager.Render(event, configuration.Server.I18N.DefaultLanguage, variables)
	if err != nil {
		return err
	}
	message.To = strings.TrimSpace(recipient)
	message.ReplyTo = strings.TrimSpace(settings.ReplyTo)
	mailer := email.Mailer{
		SMTP: email.SMTPConfig{
			Host: settings.SMTP.Host, Port: settings.SMTP.Port, Username: settings.SMTP.Username,
			Password: settings.SMTP.Password, TLS: settings.SMTP.TLS,
		},
		Sender:  email.Sender{Name: fromName, Address: fromAddress, FQDN: fqdn},
		Timeout: 15 * time.Second,
	}
	return mailer.Send(ctx, message)
}

func runEmailCommand(args []string, configuration *config.Config, configDir string, store *database.Store, output io.Writer) error {
	if len(args) == 0 || args[0] != "test" {
		return errors.New("usage: gitmessages email test [--to ADDRESS] [--dry-run]")
	}
	flags := flag.NewFlagSet("email test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	recipientFlag := flags.String("to", "", "Test email recipient")
	dryRun := flags.Bool("dry-run", false, "Render and validate every email template without sending")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("email test does not accept positional arguments")
	}
	if configuration == nil {
		return errors.New("email configuration is required")
	}
	settings := configuration.Server.Notifications.Email
	manager := email.NewTemplateManager(filepath.Join(configDir, "template", "email"))
	values := map[string]string{
		"app_name":              configuration.Server.Branding.Title,
		"app_url":               "https://" + configuration.Server.FQDN,
		"fqdn":                  configuration.Server.FQDN,
		"ip":                    "192.168.1.100",
		"notification_reply_to": settings.ReplyTo,
		"event":                 "Test email",
		"reason":                "A test email was requested.",
		"task_name":             "sample_task",
		"error":                 "sample error",
		"filename":              "sample-backup.tar.gz",
		"size":                  "1 MiB",
		"domain":                configuration.Server.FQDN,
		"expires_in":            "7 days",
		"expiry_date":           "2030-01-01",
		"valid_until":           "2031-01-01",
		"next_retry":            "2030-01-02T00:00:00Z",
		"hostname":              "server.example.test",
		"version":               "1.0.0",
		"mode":                  "production",
		"path":                  "/var/lib/gitmessages",
		"used_percent":          "90",
		"free":                  "1 GiB",
		"threshold_percent":     "10",
		"since":                 time.Now().Add(-time.Minute).Format(time.RFC3339),
		"current_version":       "1.0.0",
		"new_version":           "1.0.1",
		"previous_version":      "1.0.0",
		"channel":               "stable",
		"next_run":              "2030-01-02T00:00:00Z",
		"details":               "Sample notification details",
	}
	values["timestamp"] = time.Now().Format(time.RFC3339)
	if *dryRun {
		for _, name := range manager.Names() {
			message, err := manager.Render(name, configuration.Server.I18N.DefaultLanguage, values)
			if err != nil {
				return fmt.Errorf("render %s: %w", name, err)
			}
			if _, err := fmt.Fprintf(output, "Template: %s\nSubject: %s\n%s\n\n", name, message.Subject, message.Text); err != nil {
				return err
			}
		}
		return nil
	}
	if !settings.Enabled || strings.TrimSpace(settings.SMTP.Host) == "" || settings.SMTP.Port < 1 {
		return errors.New("email features require SMTP configuration")
	}
	recipient := strings.TrimSpace(*recipientFlag)
	if recipient == "" {
		recipient = strings.TrimSpace(settings.ReplyTo)
	}
	if recipient == "" {
		return errors.New("set server.notifications.email.reply_to or pass --to ADDRESS")
	}
	message, err := manager.Render("test", configuration.Server.I18N.DefaultLanguage, values)
	if err != nil {
		return err
	}
	message.Subject = "[TEST] " + message.Subject
	message.To = recipient
	message.ReplyTo = strings.TrimSpace(settings.ReplyTo)
	appName := strings.TrimSpace(configuration.Server.Branding.Title)
	if appName == "" {
		appName = paths.ProjectName
	}
	fqdn := strings.TrimSpace(configuration.Server.FQDN)
	if fqdn == "" {
		fqdn = "localhost"
	}
	fromName, fromAddress := strings.TrimSpace(settings.From.Name), strings.TrimSpace(settings.From.Email)
	if fromName == "" {
		fromName = appName
	}
	if fromAddress == "" {
		fromAddress = "no-reply@" + fqdn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mailer := email.Mailer{SMTP: email.SMTPConfig{Host: settings.SMTP.Host, Port: settings.SMTP.Port, Username: settings.SMTP.Username, Password: settings.SMTP.Password, TLS: settings.SMTP.TLS}, Sender: email.Sender{Name: fromName, Address: fromAddress, FQDN: fqdn}, Timeout: 15 * time.Second}
	if err := mailer.Send(ctx, message); err != nil {
		return err
	}
	details := fmt.Sprintf(`{"recipient":%q,"template":"test","subject":%q}`, recipient, message.Subject)
	if store != nil {
		if err := store.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,target_type,target_id,details,success) VALUES('info','email','email.test_sent','recipient',?,?,1)`, recipient, details)
			return err
		}); err != nil {
			return fmt.Errorf("record test email audit event: %w", err)
		}
	}
	_, err = fmt.Fprintf(output, "Sent test email to %s using template test. Subject: %s\n", recipient, message.Subject)
	return err
}
