package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestOpenConfiguredSQLiteUsesSavedFileURL(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "configured.db")
	store, err := OpenConfigured(config.DatabaseConfig{Driver: "sqlite3", URL: "file:" + filepath.ToSlash(path)}, filepath.Join(projectTestDir(t), "fallback.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var count int
	if err := store.Read().QueryRow("SELECT COUNT(*) FROM app_secrets").Scan(&count); err != nil {
		t.Fatalf("read configured database schema: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !store.CanServeReads(ctx) {
		t.Fatal("available SQLite database was reported unable to serve reads")
	}
}

func TestOpenConfiguredSQLiteUsesFallbackPath(t *testing.T) {
	path := filepath.Join(projectTestDir(t), "fallback.db")
	store, err := OpenConfigured(config.DatabaseConfig{Driver: "sqlite", Pool: config.DatabasePoolConfig{MaxOpen: 7, MaxIdle: 3, MaxLifetime: "10m", MaxIdleTime: "2m"}}, path, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if store.remote {
		t.Fatal("SQLite configuration created a remote store")
	}
	if got := store.read.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("SQLite read pool max open = %d, want 7", got)
	}
	if got := store.write.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("SQLite writer max open = %d, want 1", got)
	}
}

func TestOpenConfiguredRejectsUnknownDriver(t *testing.T) {
	if _, err := OpenConfigured(config.DatabaseConfig{Driver: "postgres"}, filepath.Join(projectTestDir(t), "server.db"), 1); err == nil {
		t.Fatal("unsupported database driver was accepted")
	}
}

func TestLocalDatabaseCacheRefreshesAndTracksAge(t *testing.T) {
	root := projectTestDir(t)
	remote, err := OpenSQLite(filepath.Join(root, "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	cache, err := OpenSQLite(filepath.Join(root, "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{read: remote.read, write: remote.write, remote: true}
	if err := store.attachLocalCache(cache, time.Hour, time.Minute, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('cache-test','remote-value','now')`)
		if err != nil {
			return err
		}
		for index := 0; index < 500; index++ {
			if _, err := tx.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?)`, "cache-test-"+strconv.Itoa(index), "remote-value", "now"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := cache.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='cache-test'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "remote-value" {
		t.Fatalf("cached value = %q", value)
	}
	var cachedRows int
	if err := cache.Read().QueryRow(`SELECT COUNT(*) FROM app_secrets`).Scan(&cachedRows); err != nil {
		t.Fatal(err)
	}
	if cachedRows != 504 {
		t.Fatalf("cached row count = %d, want 504 including the three generated secrets", cachedRows)
	}
	if store.cacheLastSync.IsZero() || time.Since(store.cacheLastSync) > time.Minute {
		t.Fatalf("cache sync timestamp is stale: %v", store.cacheLastSync)
	}
	store.cacheLastSync = time.Now().Add(-2 * time.Hour)
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE app_secrets SET value='fresh-value' WHERE name='cache-test'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='cache-test'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "fresh-value" {
		t.Fatalf("refreshed cache value = %q, want fresh-value", value)
	}
}

func TestLocalCacheFailureDoesNotFailCommittedRemoteWrite(t *testing.T) {
	root := projectTestDir(t)
	remote, err := OpenSQLite(filepath.Join(root, "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	cache, err := OpenSQLite(filepath.Join(root, "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{read: remote.read, write: remote.write, remote: true}
	if err := store.attachLocalCache(cache, time.Hour, time.Minute, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := cache.Write(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(`DROP TABLE backups`); return err }); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('durable-write','committed','now')`)
		return err
	}); err != nil {
		t.Fatalf("committed remote write reported a cache failure: %v", err)
	}
	var value string
	if err := remote.Read().QueryRow(`SELECT value FROM app_secrets WHERE name='durable-write'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "committed" {
		t.Fatalf("remote value = %q, want committed", value)
	}
	store.cacheMu.RLock()
	unavailable := store.cacheRemoteUnavailable
	synced := store.cacheLastSync
	store.cacheMu.RUnlock()
	if unavailable {
		t.Fatal("local cache failure marked the remote database unavailable")
	}
	if !synced.IsZero() {
		t.Fatal("local cache failure did not mark its contents stale")
	}
}
