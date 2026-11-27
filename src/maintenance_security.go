package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/email"
	"github.com/apimgr/gitmessages/src/web"
)

type securityReportRecord struct {
	TrackingID, Status, Severity, Component, BodyAlg, ResearcherComment string
	BodyEnc, ContactEnc                                                 []byte
	CreditPref                                                          string
	CreditName                                                          sql.NullString
	DisclosureDate                                                      sql.NullString
	CreatedAt, UpdatedAt                                                string
}

func handleSecurityMaintenance(args []string, databaseDir, logsDir string, configuration *config.Config) error {
	if len(args) == 0 {
		return errors.New("usage: --maintenance security list|show|export|update")
	}
	store, err := database.OpenConfigured(configuration.Server.Database, filepath.Join(databaseDir, "server.db"), 5)
	if err != nil {
		return fmt.Errorf("open security maintenance database: %w", err)
	}
	defer func() { _ = store.Close() }()
	identity, err := auth.NewStore(store, configuration.Server.Token, configuration.Server.Security.EncryptionKey, configuration.Server.Tokens.MaxLinked, configuration.Server.Tokens.IdlePurgeDays, configuredTokenCookieAge(configuration.Server.Tokens.CookieMaxAge))
	if err != nil {
		return err
	}
	if err := authorizeTokenMaintenance(identity); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case "list":
		return listSecurityReports(ctx, store, args[1:])
	case "show":
		if len(args) != 2 {
			return errors.New("usage: --maintenance security show TRACKING_ID")
		}
		report, err := loadSecurityReport(ctx, store, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("tracking_id: %s\nstatus: %s\nseverity: %s\ncomponent: %s\ncreated_at: %s\nupdated_at: %s\nresearcher_comment: %s\ndisclosure_date: %s\n", report.TrackingID, report.Status, report.Severity, report.Component, report.CreatedAt, report.UpdatedAt, report.ResearcherComment, report.DisclosureDate.String)
		return nil
	case "export":
		return exportSecurityReport(ctx, store, args[1:])
	case "update":
		return updateSecurityReport(ctx, store, args[1:], logsDir, configuration)
	default:
		return fmt.Errorf("unknown security maintenance action %q", args[0])
	}
}

func listSecurityReports(ctx context.Context, store *database.Store, args []string) error {
	flags := flag.NewFlagSet("security list", flag.ContinueOnError)
	status := flags.String("status", "", "filter by report status")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: --maintenance security list [--status STATUS]")
	}
	query := `SELECT tracking_id,status,severity,component,created_at,updated_at FROM security_reports`
	values := []any{}
	if *status != "" {
		if !validSecurityReportStatus(*status) {
			return fmt.Errorf("invalid report status %q", *status)
		}
		query += ` WHERE status=?`
		values = append(values, *status)
	}
	query += ` ORDER BY created_at DESC,tracking_id`
	rows, err := store.Read().QueryContext(ctx, query, values...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	fmt.Println("tracking_id\tstatus\tseverity\tcomponent\tcreated_at\tupdated_at")
	for rows.Next() {
		var id, state, severity, component, created, updated string
		if err := rows.Scan(&id, &state, &severity, &component, &created, &updated); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", id, state, severity, component, created, updated)
	}
	return rows.Err()
}

func loadSecurityReport(ctx context.Context, store *database.Store, trackingID string) (securityReportRecord, error) {
	var report securityReportRecord
	err := store.Read().QueryRowContext(ctx, `SELECT tracking_id,status,severity,component,body_alg,body_enc,contact_enc,credit_pref,credit_name,researcher_comment,disclosure_date,created_at,updated_at FROM security_reports WHERE tracking_id=?`, trackingID).Scan(&report.TrackingID, &report.Status, &report.Severity, &report.Component, &report.BodyAlg, &report.BodyEnc, &report.ContactEnc, &report.CreditPref, &report.CreditName, &report.ResearcherComment, &report.DisclosureDate, &report.CreatedAt, &report.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return securityReportRecord{}, errors.New("security report not found")
	}
	if err != nil {
		return securityReportRecord{}, fmt.Errorf("load security report: %w", err)
	}
	return report, nil
}

func exportSecurityReport(ctx context.Context, store *database.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: --maintenance security export TRACKING_ID [--output FILE]")
	}
	trackingID := args[0]
	flags := flag.NewFlagSet("security export", flag.ContinueOnError)
	output := flags.String("output", "", "encrypted report output file or - for stdout")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("usage: --maintenance security export TRACKING_ID [--output FILE]")
	}
	report, err := loadSecurityReport(ctx, store, trackingID)
	if err != nil {
		return err
	}
	path := *output
	if path == "" {
		extension := ".asc"
		if report.BodyAlg == "aes-256-gcm" {
			extension = ".enc"
		}
		path = trackingID + extension
	}
	if path == "-" {
		if _, err := os.Stdout.Write(report.BodyEnc); err != nil {
			return err
		}
	} else if err := writePGPExport(path, report.BodyEnc, 0o600); err != nil {
		return fmt.Errorf("write encrypted report: %w", err)
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','security','security.report_exported','local','report',?,'{}',1)`, trackingID)
		return err
	})
}

func updateSecurityReport(ctx context.Context, store *database.Store, args []string, logsDir string, configuration *config.Config) error {
	if len(args) == 0 {
		return errors.New("usage: --maintenance security update TRACKING_ID [--status STATUS] [--comment TEXT] [--disclosure-date YYYY-MM-DD] [--severity LEVEL]")
	}
	trackingID := args[0]
	flags := flag.NewFlagSet("security update", flag.ContinueOnError)
	status := flags.String("status", "", "new report status")
	comment := flags.String("comment", "", "researcher status-page comment")
	date := flags.String("disclosure-date", "", "expected disclosure date (YYYY-MM-DD)")
	severity := flags.String("severity", "", "re-triaged severity")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid security update arguments")
	}
	report, err := loadSecurityReport(ctx, store, trackingID)
	if err != nil {
		return err
	}
	if *status != "" && (!validSecurityReportStatus(*status) || !forwardSecurityStatus(report.Status, *status)) {
		return fmt.Errorf("invalid security status transition %q to %q", report.Status, *status)
	}
	if len([]rune(*comment)) > 2000 {
		return errors.New("researcher comment exceeds 2000 characters")
	}
	if *severity != "" && !validSecuritySeverity(*severity) {
		return fmt.Errorf("invalid severity %q", *severity)
	}
	if *date != "" {
		if _, err := time.Parse("2006-01-02", *date); err != nil {
			return errors.New("disclosure date must use YYYY-MM-DD")
		}
	}
	oldStatus := report.Status
	now := time.Now().UTC().Format(time.RFC3339)
	newStatus := oldStatus
	if *status != "" {
		newStatus = *status
	}
	err = store.Write(ctx, func(tx *sql.Tx) error {
		var closedAt any
		if newStatus == "disclosed" || newStatus == "wont_fix" {
			closedAt = now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET status=?,researcher_comment=?,disclosure_date=?,severity=?,updated_at=?,closed_at=COALESCE(closed_at,?) WHERE tracking_id=?`, newStatus, chooseString(*comment, report.ResearcherComment), chooseString(*date, report.DisclosureDate.String), chooseString(*severity, report.Severity), now, closedAt, trackingID); err != nil {
			return err
		}
		if newStatus == "disclosed" && oldStatus != "disclosed" && report.CreditPref != "no" {
			credit := report.CreditName.String
			if report.CreditPref == "anon" {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_thanks WHERE credit LIKE 'Anonymous Researcher #%'`).Scan(&count); err != nil {
					return err
				}
				credit = fmt.Sprintf("Anonymous Researcher #%d", count+1)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO security_thanks(tracking_id,credit,severity,disclosed_on) VALUES(?,?,?,?) ON CONFLICT(tracking_id) DO NOTHING`, trackingID, credit, chooseString(*severity, report.Severity), time.Now().UTC().Format("2006-01-02")); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','security','security.report_updated','local','report',?,?,1)`, trackingID, fmt.Sprintf(`{"old_status":%q,"new_status":%q}`, oldStatus, newStatus))
		return err
	})
	if err != nil {
		return fmt.Errorf("update security report: %w", err)
	}
	if err := appendSecurityLog(logsDir, map[string]any{"timestamp": now, "action": "security.report_updated", "tracking_id": trackingID, "old_status": oldStatus, "new_status": newStatus}); err != nil {
		return err
	}
	if oldStatus != newStatus && len(report.ContactEnc) > 0 {
		if err := notifySecurityReportStatus(ctx, report.ContactEnc, configuration, trackingID, newStatus); err != nil {
			log.Printf("WARN security status notice failed tracking_id=%s", trackingID)
		}
	}
	return nil
}

func validSecurityReportStatus(status string) bool {
	switch status {
	case "received", "triaged", "confirmed", "patching", "disclosed", "wont_fix":
		return true
	default:
		return false
	}
}

func forwardSecurityStatus(oldStatus, newStatus string) bool {
	rank := map[string]int{"received": 0, "triaged": 1, "confirmed": 2, "patching": 3, "disclosed": 4, "wont_fix": 5}
	if !validSecurityReportStatus(oldStatus) || !validSecurityReportStatus(newStatus) {
		return false
	}
	if oldStatus == "disclosed" || oldStatus == "wont_fix" {
		return oldStatus == newStatus
	}
	return newStatus == "wont_fix" || rank[newStatus] >= rank[oldStatus]
}

func validSecuritySeverity(severity string) bool {
	switch severity {
	case "critical", "high", "medium", "low", "informational":
		return true
	default:
		return false
	}
}

func chooseString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func appendSecurityLog(directory string, record map[string]any) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "security.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = fmt.Fprintln(file, string(encoded))
	return err
}

func notifySecurityReportStatus(ctx context.Context, encrypted []byte, configuration *config.Config, trackingID, status string) error {
	key, err := decodeConfiguredEncryptionKey(configuration.Server.Security.EncryptionKey)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < gcm.NonceSize() {
		return errors.New("invalid encrypted researcher contact")
	}
	plain, err := gcm.Open(nil, encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():], nil)
	if err != nil {
		return err
	}
	var contactInfo struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(plain, &contactInfo); err != nil {
		return err
	}
	settings := configuration.Server.Notifications.Email
	fromName := settings.From.Name
	fromAddress := settings.From.Email
	if fromName == "" {
		fromName = configuration.Server.Branding.Title
	}
	mailHost := web.ResolveStartupFQDN()
	if mailHost == "" {
		mailHost = "gitmessages.invalid"
	}
	mailer := email.Mailer{SMTP: email.SMTPConfig{Host: settings.SMTP.Host, Port: settings.SMTP.Port, Username: settings.SMTP.Username, Password: settings.SMTP.Password, TLS: settings.SMTP.TLS}, Sender: email.Sender{Name: fromName, Address: fromAddress, FQDN: mailHost}}
	if !mailer.Configured() || contactInfo.Email == "" {
		return nil
	}
	return mailer.Send(ctx, email.Message{To: contactInfo.Email, Subject: "Security report status updated", Text: fmt.Sprintf("The status of report %s is now %s.\n", trackingID, status)})
}

func decodeConfiguredEncryptionKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("server.security.encryption_key must contain 32 encoded bytes")
	}
	return key, nil
}
