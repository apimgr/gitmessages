package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/paths"
)

type ownerDataExport struct {
	Format    int              `json:"format"`
	Owner     ownerData        `json:"owner"`
	Tokens    []ownerTokenData `json:"tokens"`
	Resources []any            `json:"resources"`
}

type ownerTokenData struct {
	Prefix     string     `json:"prefix"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Status     string     `json:"status"`
}

type ownerData struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

func runDataMaintenance(ctx context.Context, args []string, store *database.Store, authStore *auth.Store) error {
	if len(args) < 2 {
		return errors.New("usage: gitmessages --maintenance data export|delete --owner <prefix>")
	}
	operation := args[0]
	var prefix, output string
	yes := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--owner":
			i++
			if i >= len(args) || prefix != "" {
				return errors.New("--owner requires one token prefix")
			}
			prefix = args[i]
		case "--output":
			i++
			if i >= len(args) || output != "" {
				return errors.New("--output requires one path")
			}
			output = args[i]
		case "--yes":
			yes = true
		default:
			return fmt.Errorf("unknown data command option %q", args[i])
		}
	}
	if prefix == "" {
		return errors.New("--owner token prefix is required")
	}
	if operation == "export" && yes || operation == "delete" && output != "" || operation != "export" && operation != "delete" {
		return errors.New("invalid data command options")
	}
	rows, err := store.Read().QueryContext(ctx, `SELECT DISTINCT owner_id FROM api_tokens WHERE token_prefix = ?`, prefix)
	if err != nil {
		return fmt.Errorf("find owner token prefix: %w", err)
	}
	var ownerID string
	for rows.Next() {
		if ownerID != "" {
			_ = rows.Close()
			return errors.New("owner token prefix is ambiguous")
		}
		if err := rows.Scan(&ownerID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read owner token prefix: %w", err)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read owner token prefix: %w", err)
	}
	if ownerID == "" {
		return errors.New("owner token prefix not found")
	}
	if operation == "export" {
		return exportOwnerData(ctx, store, authStore, ownerID, output)
	}
	return deleteOwnerData(ctx, store, ownerID, yes)
}

func exportOwnerData(ctx context.Context, store *database.Store, authStore *auth.Store, ownerID, output string) error {
	var created, lastSeen int64
	if err := store.Read().QueryRowContext(ctx, `SELECT created_at, last_seen_at FROM owners WHERE id = ?`, ownerID).Scan(&created, &lastSeen); err != nil {
		return fmt.Errorf("read owner data: %w", err)
	}
	tokens, err := authStore.List(ctx, ownerID)
	if err != nil {
		return fmt.Errorf("read owner token metadata: %w", err)
	}
	metadata := make([]ownerTokenData, 0, len(tokens))
	for _, token := range tokens {
		metadata = append(metadata, ownerTokenData{Prefix: token.Prefix, Kind: token.Kind, Name: token.Name, CreatedAt: token.CreatedAt, LastUsedAt: token.LastUsedAt, ExpiresAt: token.ExpiresAt, Status: token.Status})
	}
	data := ownerDataExport{Format: 1, Owner: ownerData{ID: ownerID, CreatedAt: time.Unix(created, 0).UTC(), LastSeenAt: time.Unix(lastSeen, 0).UTC()}, Tokens: metadata, Resources: []any{}}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode owner export: %w", err)
	}
	encoded = append(encoded, '\n')
	requestID, err := newComplianceRequestID()
	if err != nil {
		return err
	}
	if err := writeComplianceAudit(ctx, store, "compliance.data_export_requested", ownerID, map[string]any{"request_id": requestID}); err != nil {
		return fmt.Errorf("record export request: %w", err)
	}
	destination := output
	if destination == "" {
		destination = fmt.Sprintf("%s-export-%s-%s.json", paths.ProjectName, ownerID, time.Now().UTC().Format("20060102"))
	}
	if destination == "-" {
		if _, err := os.Stdout.Write(encoded); err != nil {
			return fmt.Errorf("write owner export to stdout: %w", err)
		}
	} else {
		file, err := os.OpenFile(filepath.Clean(destination), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create private owner export: %w", err)
		}
		_, writeErr := file.Write(encoded)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(destination)
			return fmt.Errorf("write owner export: %w", err)
		}
	}
	if err := writeComplianceAudit(ctx, store, "compliance.data_export_completed", ownerID, map[string]any{"request_id": requestID, "file_size": len(encoded)}); err != nil {
		return fmt.Errorf("record export completion: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Owner export written (%d bytes)\n", len(encoded))
	return nil
}

func deleteOwnerData(ctx context.Context, store *database.Store, ownerID string, yes bool) error {
	if !yes {
		fmt.Fprintf(os.Stderr, "Type owner id %s to confirm deletion: ", ownerID)
		var confirmation string
		if _, err := fmt.Fscan(os.Stdin, &confirmation); err != nil {
			return fmt.Errorf("read deletion confirmation: %w", err)
		}
		if confirmation != ownerID {
			return errors.New("owner id confirmation did not match")
		}
	}
	requestID, err := newComplianceRequestID()
	if err != nil {
		return err
	}
	anonymousID, err := newComplianceRequestID()
	if err != nil {
		return err
	}
	var deleted, anonymized int64
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		var tokenCount int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, ownerID).Scan(&tokenCount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level, category, action, target_type, target_id, details, success) VALUES ('info','compliance','compliance.data_deletion_requested','owner',?,?,1)`, ownerID, fmt.Sprintf(`{"request_id":%q}`, requestID)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE audit_log SET target_id = ? WHERE target_type = 'owner' AND target_id = ?`, anonymousID, ownerID)
		if err != nil {
			return err
		}
		anonymized, err = result.RowsAffected()
		if err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `DELETE FROM owners WHERE id = ?`, ownerID)
		if err != nil {
			return err
		}
		ownersDeleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if ownersDeleted != 1 {
			return errors.New("owner no longer exists")
		}
		deleted = ownersDeleted + tokenCount
		details, err := json.Marshal(map[string]int64{"deleted_count": deleted, "anonymized_count": anonymized})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(level, category, action, target_type, target_id, details, success) VALUES ('info','compliance','compliance.data_deletion_completed','owner',?,?,1)`, anonymousID, string(details))
		return err
	}); err != nil {
		return fmt.Errorf("delete owner data: %w", err)
	}
	fmt.Printf("Deleted %d items; anonymized %d audit records\n", deleted, anonymized)
	return nil
}

func runComplianceReport(ctx context.Context, args []string, store *database.Store, configuration *config.Config) error {
	if len(args) != 1 || args[0] != "report" {
		return errors.New("usage: gitmessages --maintenance compliance report")
	}
	var owners, tokens, resources, reports int64
	for _, query := range []struct {
		destination *int64
		sql         string
	}{{&owners, `SELECT COUNT(*) FROM owners`}, {&tokens, `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > strftime('%s','now'))`}, {&reports, `SELECT COUNT(*) FROM security_reports WHERE closed_at IS NULL`}} {
		if err := store.Read().QueryRowContext(ctx, query.sql).Scan(query.destination); err != nil {
			return fmt.Errorf("query compliance counts: %w", err)
		}
	}
	var lastBackup sql.NullInt64
	if err := store.Read().QueryRowContext(ctx, `SELECT MAX(timestamp) FROM audit_log WHERE category = 'backup' AND action IN ('backup.created','backup.restored')`).Scan(&lastBackup); err != nil {
		return fmt.Errorf("query latest backup time: %w", err)
	}
	events, err := store.Read().QueryContext(ctx, `SELECT action, COUNT(*) FROM audit_log WHERE category = 'compliance' AND timestamp >= ? GROUP BY action ORDER BY action`, time.Now().Add(-30*24*time.Hour).Unix())
	if err != nil {
		return fmt.Errorf("query recent compliance events: %w", err)
	}
	defer func() { _ = events.Close() }()
	standards := []struct {
		name string
		on   bool
	}{{"gdpr", configuration.Server.Compliance.GDPR}, {"ccpa", configuration.Server.Compliance.CCPA}, {"hipaa", configuration.Server.Compliance.HIPAA}, {"soc2", configuration.Server.Compliance.SOC2}, {"pci_dss", configuration.Server.Compliance.PCIDSS}, {"iso27001", configuration.Server.Compliance.ISO27001}, {"fedramp", configuration.Server.Compliance.FedRAMP}, {"lgpd", configuration.Server.Compliance.LGPD}, {"pipeda", configuration.Server.Compliance.PIPEDA}, {"appi", configuration.Server.Compliance.APPI}, {"pdpa", configuration.Server.Compliance.PDPA}}
	fmt.Println("Compliance report")
	fmt.Println("Standards:")
	for _, standard := range standards {
		fmt.Printf("  %s: %t\n", standard.name, standard.on)
	}
	fmt.Printf("Retention: privacy=%s; security reports=%d days\n", configuration.Server.Privacy.Retention.PeriodForLanguage("en"), configuration.Server.Security.Reports.RetentionDays)
	fmt.Printf("Counts: owners=%d resources=%d active_tokens=%d open_security_reports=%d\n", owners, resources, tokens, reports)
	fmt.Println("Compliance events in the last 30 days:")
	for events.Next() {
		var action string
		var count int64
		if err := events.Scan(&action, &count); err != nil {
			return fmt.Errorf("read recent compliance events: %w", err)
		}
		fmt.Printf("  %s: %d\n", action, count)
	}
	if err := events.Err(); err != nil {
		return fmt.Errorf("read recent compliance events: %w", err)
	}
	if lastBackup.Valid {
		fmt.Printf("Last backup: %s\n", time.Unix(lastBackup.Int64, 0).UTC().Format(time.RFC3339))
	} else {
		fmt.Println("Last backup: never")
	}
	if err := writeComplianceAudit(ctx, store, "compliance.report_generated", "", map[string]any{"report_type": "summary"}); err != nil {
		return fmt.Errorf("record compliance report: %w", err)
	}
	return nil
}

func writeComplianceAudit(ctx context.Context, store *database.Store, action, ownerID string, details any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level, category, action, target_type, target_id, details, success) VALUES ('info','compliance',?,'owner',?,?,1)`, action, nullableOwnerID(ownerID), string(encoded))
		return err
	})
}

func nullableOwnerID(ownerID string) any {
	if ownerID == "" {
		return nil
	}
	return ownerID
}

func newComplianceRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
