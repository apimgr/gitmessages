package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteReportsQueryMetricsWithoutSQLParameters(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "metrics.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var observations []QueryObservation
	store.SetQueryObserver(func(observation QueryObservation) {
		observations = append(observations, observation)
	})
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO audit_log(level,category,action) VALUES('info','test','metric')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit row count = %d, want 1", count)
	}
	seenInsert, seenSelect := false, false
	for _, observation := range observations {
		if observation.Operation == "insert" && observation.Table == "audit_log" && observation.ErrorType == "" && observation.Duration >= 0 {
			seenInsert = true
		}
		if observation.Operation == "select" && observation.Table == "audit_log" && observation.ErrorType == "" && observation.Duration >= 0 {
			seenSelect = true
		}
	}
	if !seenInsert || !seenSelect {
		t.Fatalf("query observations missing expected insert/select: %+v", observations)
	}
}

func TestQueryLabelsAndDatabaseErrorClassification(t *testing.T) {
	for _, test := range []struct{ query, operation, table string }{
		{`SELECT value FROM app_secrets WHERE name=?`, "select", "app_secrets"},
		{`INSERT INTO audit_log(level) VALUES(?)`, "insert", "audit_log"},
		{`UPDATE owners SET last_seen_at=?`, "update", "owners"},
		{`DELETE FROM captcha_challenges WHERE id=?`, "delete", "captcha_challenges"},
	} {
		operation, table := queryLabels(test.query)
		if operation != test.operation || table != test.table {
			t.Errorf("queryLabels(%q) = (%q, %q), want (%q, %q)", test.query, operation, table, test.operation, test.table)
		}
	}
	for message, want := range map[string]string{"connection reset": "connection", "SQLITE_BUSY": "timeout", "UNIQUE constraint failed": "duplicate", "CHECK constraint failed": "constraint", "other": "other"} {
		if got := classifyDatabaseError(assertedError(message)); got != want {
			t.Errorf("classifyDatabaseError(%q) = %q, want %q", message, got, want)
		}
	}
}

type assertedError string

func (e assertedError) Error() string { return string(e) }
