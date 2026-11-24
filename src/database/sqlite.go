package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

// Store exposes separate SQLite read and write handles so request handlers do
// not accidentally upgrade a read transaction while holding a cursor.
type Store struct {
	read                   *sql.DB
	write                  *sql.DB
	remote                 bool
	cache                  *Store
	cacheMu                sync.RWMutex
	cacheLastSync          time.Time
	cacheMaxAge            time.Duration
	cacheSyncInterval      time.Duration
	cacheOfflineMode       bool
	cacheRemoteUnavailable bool
	cacheSyncMu            sync.Mutex
	cacheStop              chan struct{}
	cacheDone              chan struct{}
	cacheStopOnce          sync.Once
	observer               *queryObserver
	busyMu                 sync.RWMutex
	busyFn                 func(time.Duration, string)
}

// OpenSQLite creates or opens a local server database and applies its schema.
func OpenSQLite(path string, readers int) (*Store, error) {
	if readers < 1 {
		readers = 5
	}
	return OpenSQLitePool(path, readers, min(readers, 2), 5*time.Minute, time.Minute)
}

// OpenSQLitePool applies the configured pool to the read handle while preserving SQLite's single writer.
func OpenSQLitePool(path string, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) (*Store, error) {
	if maxOpen < 1 {
		maxOpen = 5
	}
	if maxIdle < 0 {
		maxIdle = 0
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	if maxLifetime <= 0 {
		maxLifetime = 5 * time.Minute
	}
	if maxIdleTime <= 0 {
		maxIdleTime = time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secure database directory: %w", err)
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure database file: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect database file: %w", err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	base := fileURL + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	observer := &queryObserver{}
	driverName := registerInstrumentedSQLite(observer)
	write, err := sql.Open(driverName, base+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite writer: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)
	read, err := sql.Open(driverName, base+"&_pragma=query_only(1)")
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	read.SetMaxOpenConns(maxOpen)
	read.SetMaxIdleConns(maxIdle)
	read.SetConnMaxLifetime(maxLifetime)
	read.SetConnMaxIdleTime(maxIdleTime)
	store := &Store{read: read, write: write, observer: observer}
	connectContext, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
	if err := write.PingContext(connectContext); err != nil {
		cancelConnect()
		_ = store.Close()
		return nil, fmt.Errorf("connect SQLite writer: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		cancelConnect()
		_ = store.Close()
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	if err := read.PingContext(connectContext); err != nil {
		cancelConnect()
		_ = store.Close()
		return nil, fmt.Errorf("connect SQLite reader: %w", err)
	}
	var journal string
	if err := write.QueryRowContext(connectContext, "PRAGMA journal_mode").Scan(&journal); err != nil || !strings.EqualFold(journal, "wal") {
		cancelConnect()
		_ = store.Close()
		if err != nil {
			return nil, fmt.Errorf("check SQLite journal mode: %w", err)
		}
		return nil, fmt.Errorf("SQLite database must support WAL mode; got %q", journal)
	}
	cancelConnect()
	schemaContext, cancelSchema := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelSchema()
	if err := store.EnsureSchema(schemaContext); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// SetQueryObserver receives bounded database labels and timings without SQL parameters.
func (s *Store) SetQueryObserver(callback func(QueryObservation)) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.mu.Lock()
	s.observer.callback = callback
	s.observer.mu.Unlock()
}

func (s *Store) SetBusyObserver(callback func(time.Duration, string)) {
	if s == nil {
		return
	}
	s.busyMu.Lock()
	s.busyFn = callback
	s.busyMu.Unlock()
}

// Read returns the query-only handle. Callers must close result rows promptly.
func (s *Store) Read() *sql.DB {
	if s == nil {
		return nil
	}
	if s.cache == nil {
		return s.read
	}
	s.cacheMu.RLock()
	lastSync := s.cacheLastSync
	remoteUnavailable := s.cacheRemoteUnavailable
	s.cacheMu.RUnlock()
	fresh := !lastSync.IsZero() && time.Since(lastSync) <= s.cacheMaxAge
	if remoteUnavailable {
		if fresh && s.cacheOfflineMode {
			return s.cache.Read()
		}
		return s.read
	}
	if fresh {
		return s.cache.Read()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := s.syncLocalCache(ctx)
	cancel()
	if err == nil {
		return s.cache.Read()
	}
	s.cacheMu.RLock()
	lastSync = s.cacheLastSync
	offlineAllowed := s.cacheOfflineMode && !lastSync.IsZero() && time.Since(lastSync) <= s.cacheMaxAge
	s.cacheMu.RUnlock()
	var syncFailure cacheSyncFailure
	if offlineAllowed && errors.As(err, &syncFailure) && syncFailure.remote {
		return s.cache.Read()
	}
	return s.read
}

// Snapshot copies the live database to a new SQLite file through SQLite's online backup API.
func (s *Store) Snapshot(ctx context.Context, destination string) (result error) {
	if s == nil || s.write == nil || destination == "" {
		return errors.New("database store and snapshot destination are required")
	}
	if s.remote {
		return errors.New("remote libSQL snapshots are unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("close snapshot file: %w", err)
	}
	defer func() {
		if result != nil {
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, fmt.Errorf("remove failed snapshot: %w", err))
			}
		}
	}()
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(destination)}).String()
	connection, err := s.write.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite connection for snapshot: %w", err)
	}
	defer func() { result = errors.Join(result, connection.Close()) }()
	err = connection.Raw(func(raw any) (backupErr error) {
		backupper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite connection does not support online backups")
		}
		backup, err := backupper.NewBackup(uri)
		if err != nil {
			return fmt.Errorf("start SQLite online backup: %w", err)
		}
		defer func() { backupErr = errors.Join(backupErr, backup.Finish()) }()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				return fmt.Errorf("copy SQLite backup pages: %w", err)
			}
			if !more {
				return nil
			}
		}
	})
	if err != nil {
		return fmt.Errorf("snapshot SQLite database: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("secure snapshot file: %w", err)
	}
	return nil
}

// Write runs a short write transaction, retrying SQLite busy errors within a
// ten-second deadline.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if s == nil || fn == nil {
		return errors.New("database store and write callback are required")
	}
	s.cacheMu.RLock()
	remoteUnavailable := s.cache != nil && s.cacheRemoteUnavailable
	s.cacheMu.RUnlock()
	if remoteUnavailable || s.write == nil {
		return errors.New("database is read-only while remote libSQL is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	caller := "unknown"
	if programCounter, _, _, ok := runtime.Caller(1); ok {
		if function := runtime.FuncForPC(programCounter); function != nil {
			caller = function.Name()
		}
	}
	hadBusy := false
	defer func() {
		waited := time.Since(started)
		if !hadBusy || waited <= time.Second {
			return
		}
		s.busyMu.RLock()
		callback := s.busyFn
		s.busyMu.RUnlock()
		if callback != nil {
			defer func() { _ = recover() }()
			callback(waited, caller)
		}
	}()
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = s.writeOnce(ctx, fn)
		if !isBusy(err) {
			if err == nil && s.cache != nil {
				if syncErr := s.syncLocalCache(ctx); syncErr != nil {
					s.cacheMu.Lock()
					s.cacheLastSync = time.Time{}
					s.cacheMu.Unlock()
				}
			}
			return err
		}
		hadBusy = true
		wait := time.Duration(25<<attempt)*time.Millisecond + time.Duration(rand.IntN(25))*time.Millisecond
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (s *Store) writeOnce(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "SQLITE_BUSY") || strings.Contains(message, "SQLITE_LOCKED") || strings.Contains(message, "DATABASE IS LOCKED")
}

// EnsureSchema creates the shared server tables idempotently.
func (s *Store) EnsureSchema(ctx context.Context) error {
	for _, statement := range schemaStatements {
		if err := s.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement)
			return err
		}); err != nil {
			return fmt.Errorf("ensure server schema: %w", err)
		}
	}
	if err := s.migrateSchedulerTasks(ctx); err != nil {
		return err
	}
	if err := s.migrateAPITokens(ctx); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_scheduler_history_task ON scheduler_history(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduler_history_started ON scheduler_history(started_at)`,
	} {
		if err := s.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement)
			return err
		}); err != nil {
			return fmt.Errorf("ensure scheduler history indexes: %w", err)
		}
	}
	return nil
}

func (s *Store) migrateAPITokens(ctx context.Context) error {
	columns, err := tableColumns(ctx, s.read, "api_tokens")
	if err != nil {
		return fmt.Errorf("inspect API token schema: %w", err)
	}
	if columns["token_last4"] {
		return nil
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN token_last4 TEXT NOT NULL DEFAULT ''`)
		return err
	}); err != nil {
		return fmt.Errorf("add api_tokens.token_last4: %w", err)
	}
	return nil
}

func (s *Store) migrateSchedulerTasks(ctx context.Context) error {
	taskColumns, err := tableColumns(ctx, s.read, "scheduler_tasks")
	if err != nil {
		return fmt.Errorf("inspect scheduler task schema: %w", err)
	}
	historyColumns, err := tableColumns(ctx, s.read, "scheduler_history")
	if err != nil {
		return fmt.Errorf("inspect scheduler history schema: %w", err)
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, rename := range [][2]string{{"task_id", "id"}, {"task_name", "name"}} {
			if taskColumns[rename[0]] && !taskColumns[rename[1]] {
				if _, err := tx.ExecContext(ctx, `ALTER TABLE scheduler_tasks RENAME COLUMN `+rename[0]+` TO `+rename[1]); err != nil {
					return fmt.Errorf("rename scheduler_tasks.%s: %w", rename[0], err)
				}
				taskColumns[rename[1]] = true
				delete(taskColumns, rename[0])
			}
		}
		if !taskColumns["id"] || !taskColumns["name"] {
			return fmt.Errorf("scheduler_tasks has an unsupported schema")
		}
		for _, column := range []struct{ name, definition string }{
			{"schedule", "TEXT NOT NULL DEFAULT ''"},
			{"last_run", "INTEGER"},
			{"last_status", "TEXT NOT NULL DEFAULT ''"},
			{"last_error", "TEXT NOT NULL DEFAULT ''"},
			{"next_run", "INTEGER"},
			{"run_count", "INTEGER NOT NULL DEFAULT 0"},
			{"fail_count", "INTEGER NOT NULL DEFAULT 0"},
			{"enabled", "INTEGER NOT NULL DEFAULT 1"},
		} {
			if taskColumns[column.name] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `ALTER TABLE scheduler_tasks ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("add scheduler_tasks.%s: %w", column.name, err)
			}
		}
		for _, rename := range [][2]string{{"ran_at", "started_at"}} {
			if historyColumns[rename[0]] && !historyColumns[rename[1]] {
				if _, err := tx.ExecContext(ctx, `ALTER TABLE scheduler_history RENAME COLUMN `+rename[0]+` TO `+rename[1]); err != nil {
					return fmt.Errorf("rename scheduler_history.%s: %w", rename[0], err)
				}
				historyColumns[rename[1]] = true
				delete(historyColumns, rename[0])
			}
		}
		if !historyColumns["task_id"] || !historyColumns["started_at"] {
			return fmt.Errorf("scheduler_history has an unsupported schema")
		}
		for _, column := range []struct{ name, definition string }{
			{"finished_at", "INTEGER"},
			{"status", "TEXT NOT NULL DEFAULT 'running'"},
			{"error", "TEXT"},
			{"duration_ms", "INTEGER"},
		} {
			if historyColumns[column.name] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `ALTER TABLE scheduler_history ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("add scheduler_history.%s: %w", column.name, err)
			}
		}
		return nil
	})
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	if table != "scheduler_tasks" && table != "scheduler_history" && table != "api_tokens" {
		return nil, fmt.Errorf("unsupported schema table %q", table)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns := make(map[string]bool)
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("table %s does not exist", table)
	}
	return columns, nil
}

// Close checkpoints the WAL and closes the read handle before the writer.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.cacheStop != nil {
		s.cacheStopOnce.Do(func() { close(s.cacheStop) })
		<-s.cacheDone
	}
	var checkpointErr error
	if s.write != nil && !s.remote {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, checkpointErr = s.write.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		cancel()
	}
	var readErr, writeErr error
	if s.cache != nil {
		readErr = s.cache.Close()
	} else if s.read != nil && s.read != s.write {
		readErr = s.read.Close()
	}
	if s.write != nil && s.cache == nil {
		writeErr = s.write.Close()
	} else if s.write != nil {
		writeErr = s.write.Close()
	}
	return errors.Join(checkpointErr, readErr, writeErr)
}
