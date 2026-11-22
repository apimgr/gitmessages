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
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store exposes separate SQLite read and write handles so request handlers do
// not accidentally upgrade a read transaction while holding a cursor.
type Store struct {
	read  *sql.DB
	write *sql.DB
}

// OpenSQLite creates or opens a local server database and applies its schema.
func OpenSQLite(path string, readers int) (*Store, error) {
	if readers < 1 {
		readers = 5
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
	write, err := sql.Open("sqlite", base+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite writer: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)
	read, err := sql.Open("sqlite", base+"&_pragma=query_only(1)")
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	read.SetMaxOpenConns(readers)
	read.SetMaxIdleConns(min(readers, 2))
	store := &Store{read: read, write: write}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := write.PingContext(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("connect SQLite writer: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	if err := read.PingContext(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("connect SQLite reader: %w", err)
	}
	var journal string
	if err := write.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || !strings.EqualFold(journal, "wal") {
		_ = store.Close()
		if err != nil {
			return nil, fmt.Errorf("check SQLite journal mode: %w", err)
		}
		return nil, fmt.Errorf("SQLite database must support WAL mode; got %q", journal)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// Read returns the query-only handle. Callers must close result rows promptly.
func (s *Store) Read() *sql.DB { return s.read }

// Write runs a short write transaction, retrying SQLite busy errors within a
// ten-second deadline.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if s == nil || fn == nil {
		return errors.New("database store and write callback are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = s.writeOnce(ctx, fn)
		if !isBusy(err) {
			return err
		}
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
	return nil
}

// Close checkpoints the WAL and closes the read handle before the writer.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var checkpointErr error
	if s.write != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, checkpointErr = s.write.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		cancel()
	}
	var readErr, writeErr error
	if s.read != nil {
		readErr = s.read.Close()
	}
	if s.write != nil {
		writeErr = s.write.Close()
	}
	return errors.Join(checkpointErr, readErr, writeErr)
}
