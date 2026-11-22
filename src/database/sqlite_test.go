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
	path := filepath.Join(t.TempDir(), "nested", "server.db")
	store, err := OpenSQLite(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("repeat schema initialization: %v", err)
	}
	for name, want := range map[string]os.FileMode{filepath.Dir(path): 0700, path: 0600} {
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
	if count != 1 {
		t.Fatalf("stored row count = %d, want 1", count)
	}
	if _, err := store.Read().Exec("INSERT INTO app_secrets(name, value, updated_at) VALUES('readonly', 'value', 'now')"); err == nil {
		t.Fatal("read handle accepted a write")
	}
}

func TestOpenSQLiteRejectsInvalidDatabasePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.db")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenSQLite(path, 1); err == nil {
		_ = store.Close()
		t.Fatal("database directory was accepted as a SQLite file")
	}
}

func TestWriteRejectsNilCallbackAndCloseNil(t *testing.T) {
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatalf("closing nil store: %v", err)
	}
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Write(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("nil callback error = %v", err)
	}
}

func TestSQLiteConcurrentReadersAndWriters(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "concurrent.db"), 20)
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
