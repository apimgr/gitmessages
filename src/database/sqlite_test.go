package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenSQLiteSchemaPermissionsAndTransactions(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "nested", "server.db")
	store, err := OpenSQLite(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("repeat schema initialization: %v", err)
	}
	for name, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("permissions for %s = %o, want %o", name, got, want)
		}
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES('test', 'value', 'now')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES('rollback', 'value', 'now')")
		if err != nil {
			return err
		}
		return errors.New("rollback transaction")
	}); err == nil {
		t.Fatal("write callback error was ignored")
	}
	var count int
	if err := store.Read().QueryRow("SELECT count(*) FROM app_secrets").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("stored row count = %d, want the three generated secrets and one test row", count)
	}
	if _, err := store.Read().Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES('readonly', 'value', 'now')"); err == nil {
		t.Fatal("read handle accepted a write")
	}
}

func TestEnsureSchemaMigratesAppSecretVersionAndCreatesRevocations(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `INSERT INTO app_secrets(name,value,updated_at) VALUES('legacy','preserved','2026-01-01T00:00:00Z')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `ALTER TABLE app_secrets DROP COLUMN version`)
		return err
	}); err != nil {
		t.Fatalf("prepare legacy app_secrets schema: %v", err)
	}
	for range 2 {
		if err := store.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("migrate app secret schema: %v", err)
		}
	}
	var value string
	var version int
	if err := store.Read().QueryRow(`SELECT value,version FROM app_secrets WHERE name='legacy'`).Scan(&value, &version); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" || version != 1 {
		t.Fatalf("migrated secret = %q at version %d, want preserved at version 1", value, version)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO app_secret_revocations(name,version,node_id,revoked_at) VALUES('legacy',1,'node-a','2026-01-01T00:00:00Z')`)
		return err
	}); err != nil {
		t.Fatalf("insert app secret revocation: %v", err)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO app_secret_revocations(name,version,node_id,revoked_at) VALUES('legacy',1,'node-a','2026-01-02T00:00:00Z')`)
		return err
	}); err == nil {
		t.Fatal("app secret revocation composite key accepted a duplicate")
	}
}

func TestSchedulerSchemaUsesCanonicalColumnsAndMigratesLegacyRows(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		statements := []string{
			`ALTER TABLE scheduler_tasks RENAME TO scheduler_tasks_current`,
			`ALTER TABLE scheduler_history RENAME TO scheduler_history_current`,
			`DROP TABLE scheduler_tasks_current`,
			`DROP TABLE scheduler_history_current`,
			`CREATE TABLE scheduler_tasks (
				task_id TEXT PRIMARY KEY, task_name TEXT NOT NULL, schedule TEXT NOT NULL,
				last_run INTEGER, last_status TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
				next_run INTEGER, run_count INTEGER NOT NULL DEFAULT 0, fail_count INTEGER NOT NULL DEFAULT 0,
				enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)))`,
			`CREATE TABLE scheduler_history (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, ran_at INTEGER NOT NULL, status TEXT NOT NULL, error TEXT)`,
			`INSERT INTO scheduler_tasks(task_id,task_name,schedule,last_status,run_count,fail_count,enabled)
				VALUES('backup_daily','Daily Backup','0 2 * * *','success',4,1,1)`,
			`INSERT INTO scheduler_history(task_id,ran_at,status,error) VALUES('backup_daily',12345,'failed','disk full')`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(context.Background(), statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("migrate legacy scheduler schema: %v", err)
	}
	var name, schedule, status string
	var runs, failures, enabled int
	if err := store.Read().QueryRow(`SELECT name,schedule,last_status,run_count,fail_count,enabled FROM scheduler_tasks WHERE id='backup_daily'`).Scan(&name, &schedule, &status, &runs, &failures, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "Daily Backup" || schedule != "0 2 * * *" || status != "success" || runs != 4 || failures != 1 || enabled != 1 {
		t.Fatalf("migrated task = %q, %q, %q, %d, %d, %d", name, schedule, status, runs, failures, enabled)
	}
	var oldTaskColumns, oldHistoryColumns int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('scheduler_tasks') WHERE name IN ('task_id','task_name')`).Scan(&oldTaskColumns); err != nil {
		t.Fatal(err)
	}
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('scheduler_history') WHERE name='ran_at'`).Scan(&oldHistoryColumns); err != nil {
		t.Fatal(err)
	}
	if oldTaskColumns != 0 || oldHistoryColumns != 0 {
		t.Fatalf("legacy columns remain: task=%d history=%d", oldTaskColumns, oldHistoryColumns)
	}
	var started int64
	var historyStatus, historyError string
	var finished sql.NullInt64
	if err := store.Read().QueryRow(`SELECT started_at,status,error,finished_at FROM scheduler_history WHERE task_id='backup_daily'`).Scan(&started, &historyStatus, &historyError, &finished); err != nil {
		t.Fatal(err)
	}
	if started != 12345 || historyStatus != "failed" || historyError != "disk full" || finished.Valid {
		t.Fatalf("migrated history = %d %q %q finished=%v", started, historyStatus, historyError, finished)
	}
	var historyTableCount int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='scheduler_history'`).Scan(&historyTableCount); err != nil {
		t.Fatal(err)
	}
	if historyTableCount != 1 {
		t.Fatal("scheduler_history table was not created")
	}
}

func TestEnsureSchemaMigratesTokenLastFourColumnIdempotently(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `ALTER TABLE api_tokens DROP COLUMN token_last4`)
		return err
	}); err != nil {
		t.Fatalf("prepare legacy API token schema: %v", err)
	}
	for range 2 {
		if err := store.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("ensure migrated schema: %v", err)
		}
	}
	var suffixColumns int
	if err := store.Read().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('api_tokens') WHERE name='token_last4'`).Scan(&suffixColumns); err != nil {
		t.Fatal(err)
	}
	if suffixColumns != 1 {
		t.Fatalf("migrated token_last4 column count = %d, want 1", suffixColumns)
	}
}

func TestEnsureSchemaMigratesLegacyTokenIDsAndScopes(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DROP TABLE api_tokens`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE TABLE api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
			kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')),
			name TEXT,
			token_hash TEXT NOT NULL UNIQUE,
			token_prefix TEXT NOT NULL,
			token_enc TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			rotated_at INTEGER,
			expires_at INTEGER,
			last_used_at INTEGER,
			revoked_at INTEGER,
			revoked_reason TEXT
		)`)
		return err
	}); err != nil {
		t.Fatalf("prepare legacy token schema: %v", err)
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id,kind,name,token_hash,token_prefix,token_enc) VALUES ('system','primary',NULL,'primary-hash','own_primary','cipher')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id,kind,name,token_hash,token_prefix,token_enc) VALUES ('system','linked','automation','linked-hash','own_linked','cipher')`)
		return err
	}); err != nil {
		t.Fatalf("insert legacy token: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("migrate legacy token schema: %v", err)
	}
	var publicID, primaryScope, linkedScope string
	if err := store.Read().QueryRowContext(ctx, `SELECT public_id,scope FROM api_tokens WHERE kind='primary'`).Scan(&publicID, &primaryScope); err != nil {
		t.Fatal(err)
	}
	if len(publicID) != 36 || publicID[14] != '7' || primaryScope != "global" {
		t.Fatalf("migrated primary = id %q, scope %q; want UUID v7 and global", publicID, primaryScope)
	}
	if err := store.Read().QueryRowContext(ctx, `SELECT scope FROM api_tokens WHERE kind='linked'`).Scan(&linkedScope); err != nil {
		t.Fatal(err)
	}
	if linkedScope != "read-write" {
		t.Fatalf("migrated linked scope = %q, want read-write", linkedScope)
	}
	if err := store.Read().QueryRowContext(ctx, `SELECT type FROM pragma_table_info('api_tokens') WHERE name='id'`).Scan(&primaryScope); err != nil {
		t.Fatal(err)
	}
	if primaryScope != "INTEGER" {
		t.Fatalf("legacy internal id type = %q, want INTEGER preserved", primaryScope)
	}
}

func TestOpenSQLiteRejectsInvalidDatabasePath(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "directory.db")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenSQLite(path, 1); err == nil {
		_ = store.Close()
		t.Fatal("database directory was accepted as a SQLite file")
	}
}

func TestSnapshotIncludesCommittedDatabaseState(t *testing.T) {
	root := projectTestDir(t)
	store, err := OpenSQLite(filepath.Join(root, "server.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('snapshot-test','present','now')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(root, "snapshot.db")
	if err := store.Snapshot(context.Background(), snapshotPath); err != nil {
		t.Fatal(err)
	}
	snapshot, err := OpenSQLite(snapshotPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var value string
	if err := snapshot.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='snapshot-test'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "present" {
		t.Fatalf("snapshot value = %q, want present", value)
	}
}

func TestWriteRejectsNilCallbackAndCloseNil(t *testing.T) {
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatalf("closing nil store: %v", err)
	}
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Write(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("nil callback error = %v", err)
	}
}

func TestSQLiteConcurrentReadersAndWriters(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "concurrent.db"), 20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var failures atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				err := store.Write(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('concurrent','value','now') ON CONFLICT(name) DO UPDATE SET updated_at='now'`)
					return err
				})
				if err != nil && ctx.Err() == nil {
					failures.Add(1)
				}
			}
		}()
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				var value string
				if err := store.Read().QueryRowContext(ctx, "SELECT value FROM app_secrets WHERE name='concurrent'").Scan(&value); err != nil && ctx.Err() == nil && !errors.Is(err, sql.ErrNoRows) {
					failures.Add(1)
				}
			}
		}()
	}
	workers.Wait()
	if count := failures.Load(); count != 0 {
		t.Fatalf("concurrent readers and writers saw %d database errors", count)
	}
}

func TestIsBusyClassifiesSQLiteLockErrors(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
	}{
		{"SQLITE_BUSY", true},
		{"sqlite_locked", true},
		{"database is locked", true},
		{"constraint failed", false},
	} {
		if got := isBusy(errors.New(test.input)); got != test.want {
			t.Errorf("isBusy(%q) = %t, want %t", test.input, got, test.want)
		}
	}
	if isBusy(nil) {
		t.Fatal("isBusy(nil) = true")
	}
}
