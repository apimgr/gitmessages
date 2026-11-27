package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

var restoreTables = []string{
	"app_secrets",
	"app_secret_revocations",
	"rate_limits",
	"audit_log",
	"owners",
	"api_tokens",
	"captcha_challenges",
	"security_reports",
	"scheduler_tasks",
	"scheduler_history",
	"security_thanks",
	"pgp_key_metadata",
	"backups",
	"health_request_stats",
	"health_request_buckets",
}

var restorePrimaryKeys = map[string][]string{
	"app_secrets":            {"name"},
	"app_secret_revocations": {"name", "version", "node_id"},
	"rate_limits":            {"key"},
	"audit_log":              {"id"},
	"owners":                 {"id"},
	"api_tokens":             {"id"},
	"captcha_challenges":     {"id"},
	"security_reports":       {"tracking_id"},
	"scheduler_tasks":        {"id"},
	"scheduler_history":      {"id"},
	"security_thanks":        {"tracking_id"},
	"pgp_key_metadata":       {"id"},
	"backups":                {"id"},
	"health_request_stats":   {"id"},
	"health_request_buckets": {"hour_start"},
}

// RestoreDataEmpty ignores only the default system owner and zeroed health counter.
func (s *Store) RestoreDataEmpty(ctx context.Context) (bool, error) {
	if s == nil || s.read == nil {
		return false, errors.New("database store is unavailable")
	}
	var count int64
	err := s.read.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM app_secrets) +
		(SELECT COUNT(*) FROM app_secret_revocations) +
		(SELECT COUNT(*) FROM rate_limits) +
		(SELECT COUNT(*) FROM audit_log) +
		(SELECT COUNT(*) FROM owners WHERE id <> 'system') +
		(SELECT COUNT(*) FROM api_tokens) +
		(SELECT COUNT(*) FROM captcha_challenges) +
		(SELECT COUNT(*) FROM security_reports) +
		(SELECT COUNT(*) FROM scheduler_tasks) +
		(SELECT COUNT(*) FROM scheduler_history) +
		(SELECT COUNT(*) FROM security_thanks) +
		(SELECT COUNT(*) FROM pgp_key_metadata) +
		(SELECT COUNT(*) FROM backups) +
		(SELECT COUNT(*) FROM health_request_buckets) +
		(SELECT COUNT(*) FROM health_request_stats WHERE total_count > 0)`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("inspect database before restore: %w", err)
	}
	return count == 0, nil
}

// SeedFromSQLite copies a SQLite archive into an already initialized empty database.
func (s *Store) SeedFromSQLite(ctx context.Context, sourcePath string) error {
	if s == nil || s.write == nil || sourcePath == "" {
		return errors.New("database store and SQLite snapshot are required")
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(sourcePath)}).String() + "?mode=ro&_pragma=query_only(1)"
	source, err := sql.Open("sqlite", uri)
	if err != nil {
		return fmt.Errorf("open SQLite restore source: %w", err)
	}
	defer func() { _ = source.Close() }()
	if err := source.PingContext(ctx); err != nil {
		return fmt.Errorf("connect SQLite restore source: %w", err)
	}
	for _, table := range restoreTables {
		if err := s.restoreTable(ctx, source, table); err != nil {
			return fmt.Errorf("restore database table %s: %w", table, err)
		}
	}
	if s.cache != nil {
		if err := s.syncLocalCache(ctx); err != nil {
			return fmt.Errorf("refresh local database cache after restore: %w", err)
		}
	}
	return nil
}

func (s *Store) restoreTable(ctx context.Context, source *sql.DB, table string) error {
	sourceColumns, err := orderedTableColumns(ctx, source, table)
	if err != nil {
		return fmt.Errorf("inspect SQLite columns: %w", err)
	}
	targetColumns, err := orderedTableColumns(ctx, s.write, table)
	if err != nil {
		return fmt.Errorf("inspect target columns: %w", err)
	}
	targetColumnSet := make(map[string]struct{}, len(targetColumns))
	for _, column := range targetColumns {
		targetColumnSet[column] = struct{}{}
	}
	columns := make([]string, 0, len(sourceColumns))
	for _, column := range sourceColumns {
		if _, ok := targetColumnSet[column]; ok {
			columns = append(columns, column)
		}
	}
	if len(columns) == 0 {
		return errors.New("no compatible columns")
	}
	primaryKeys, ok := restorePrimaryKeys[table]
	if !ok {
		return errors.New("table has no configured primary key")
	}
	for _, primaryKey := range primaryKeys {
		if !containsColumn(columns, primaryKey) {
			return fmt.Errorf("SQLite snapshot is missing primary key %s", primaryKey)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	updates := make([]string, 0, len(columns)-len(primaryKeys))
	primarySet := make(map[string]struct{}, len(primaryKeys))
	for _, key := range primaryKeys {
		primarySet[key] = struct{}{}
	}
	for _, column := range columns {
		if _, ok := primarySet[column]; !ok {
			updates = append(updates, `"`+strings.ReplaceAll(column, `"`, `""`)+`"=excluded."`+strings.ReplaceAll(column, `"`, `""`)+`"`)
		}
	}
	statement := `INSERT INTO "` + table + `" (` + quoteColumns(columns) + `) VALUES (` + placeholders + `) ON CONFLICT (` + quoteColumns(primaryKeys) + `) `
	if len(updates) == 0 {
		statement += "DO NOTHING"
	} else {
		statement += "DO UPDATE SET " + strings.Join(updates, ",")
	}
	lastKey := make([]any, len(primaryKeys))
	hasLastKey := false
	for {
		query := `SELECT ` + quoteColumns(columns) + ` FROM "` + table + `"`
		arguments := make([]any, 0, len(primaryKeys))
		if hasLastKey {
			query += ` WHERE (` + quoteColumns(primaryKeys) + `) > (` + strings.TrimSuffix(strings.Repeat("?,", len(primaryKeys)), ",") + `)`
			arguments = append(arguments, lastKey...)
		}
		query += ` ORDER BY ` + quoteColumns(primaryKeys) + ` LIMIT 500`
		rows, err := source.QueryContext(ctx, query, arguments...)
		if err != nil {
			return fmt.Errorf("read SQLite batch: %w", err)
		}
		batch := make([][]any, 0, 500)
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(values))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan SQLite batch: %w", err)
			}
			for index, value := range values {
				if bytes, ok := value.([]byte); ok {
					values[index] = append([]byte(nil), bytes...)
				}
			}
			batch = append(batch, values)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("finish SQLite batch: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := s.writeOnce(ctx, func(tx *sql.Tx) error {
			for _, values := range batch {
				if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("write database batch: %w", err)
		}
		last := batch[len(batch)-1]
		for keyIndex, primaryKey := range primaryKeys {
			for columnIndex, column := range columns {
				if column == primaryKey {
					lastKey[keyIndex] = last[columnIndex]
					break
				}
			}
		}
		hasLastKey = true
	}
}

func containsColumn(columns []string, target string) bool {
	for _, column := range columns {
		if column == target {
			return true
		}
	}
	return false
}
