package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

// OpenConfigured selects the configured SQLite or remote libSQL database.
func OpenConfigured(settings config.DatabaseConfig, fallbackPath string, readers int) (*Store, error) {
	maxOpen, maxIdle, maxLifetime, maxIdleTime := resolvedDatabasePool(settings.Pool)
	driverName := strings.ToLower(strings.TrimSpace(settings.Driver))
	if driverName == "sqlite2" || driverName == "sqlite3" || driverName == "" {
		driverName = "sqlite"
	}
	if driverName == "sqlite" {
		path := settings.URL
		if path == "" {
			path = fallbackPath
		} else if strings.HasPrefix(path, "file:") {
			parsed, err := url.Parse(path)
			if err != nil {
				return nil, errors.New("invalid server.database.url; use a SQLite file path")
			}
			path = parsed.Path
			if path == "" {
				path = parsed.Opaque
			}
			path = filepath.FromSlash(path)
		}
		if path == "" {
			return nil, errors.New("server.database.url is required for SQLite")
		}
		return OpenSQLitePool(path, maxOpen, maxIdle, maxLifetime, maxIdleTime)
	}
	if driverName != "libsql" && driverName != "turso" {
		return nil, errors.New("server.database.driver must be sqlite or libsql")
	}
	if settings.URL == "" {
		return nil, errors.New("server.database.url is required for libSQL")
	}
	dsn, err := config.ConnectionURLForDriver(settings.URL)
	if err != nil {
		return nil, errors.New("invalid server.database.url; use the URL form")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return nil, errors.New("invalid server.database.url; use the URL form")
	}
	if settings.Token != "" && parsed.Query().Get("authToken") == "" {
		query := parsed.Query()
		query.Set("authToken", settings.Token)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	}
	db, err := sql.Open(libSQLDriverName, dsn)
	if err != nil {
		return nil, errors.New("could not open configured libSQL connection")
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)
	store := &Store{read: db, write: db, remote: true}
	connectContext, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConnect()
	if err := db.PingContext(connectContext); err != nil {
		if settings.Cache.Enabled {
			cache, cacheErr := openConfiguredLocalCache(settings.Cache, fallbackPath, maxOpen, maxIdle, maxLifetime, maxIdleTime)
			if cacheErr == nil {
				maxAge, _ := time.ParseDuration(settings.Cache.MaxAge)
				interval, _ := time.ParseDuration(settings.Cache.SyncInterval)
				if cacheErr = store.attachLocalCache(cache, maxAge, interval, settings.Cache.OfflineMode); cacheErr == nil {
					store.cacheMu.Lock()
					store.cacheRemoteUnavailable = true
					fresh := !store.cacheLastSync.IsZero() && time.Since(store.cacheLastSync) <= maxAge
					store.cacheMu.Unlock()
					if settings.Cache.OfflineMode && fresh {
						store.startCacheSync()
						return store, nil
					}
				}
				_ = cache.Close()
			}
		}
		_ = store.Close()
		return nil, errors.New("could not connect to configured libSQL database")
	}
	schemaContext, cancelSchema := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelSchema()
	if err := store.EnsureSchema(schemaContext); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("initialize configured libSQL schema: %w", err)
	}
	if settings.Cache.Enabled {
		cache, cacheErr := openConfiguredLocalCache(settings.Cache, fallbackPath, maxOpen, maxIdle, maxLifetime, maxIdleTime)
		if cacheErr != nil {
			_ = store.Close()
			return nil, cacheErr
		}
		interval, _ := time.ParseDuration(settings.Cache.SyncInterval)
		maxAge, _ := time.ParseDuration(settings.Cache.MaxAge)
		if cacheErr = store.attachLocalCache(cache, maxAge, interval, settings.Cache.OfflineMode); cacheErr != nil {
			_ = cache.Close()
			_ = store.Close()
			return nil, cacheErr
		}
		cacheContext, cancelCache := context.WithTimeout(context.Background(), 5*time.Minute)
		cacheErr = store.syncLocalCache(cacheContext)
		cancelCache()
		if cacheErr != nil {
			var failure cacheSyncFailure
			if errors.As(cacheErr, &failure) && failure.remote {
				store.cacheMu.RLock()
				fresh := !store.cacheLastSync.IsZero() && time.Since(store.cacheLastSync) <= store.cacheMaxAge
				store.cacheMu.RUnlock()
				if settings.Cache.OfflineMode && fresh {
					store.startCacheSync()
					return store, nil
				}
				_ = store.Close()
				return nil, fmt.Errorf("initialize configured remote libSQL cache: %w", cacheErr)
			}
			_ = cache.Close()
			store.cache = nil
			return store, nil
		}
		store.cacheMu.Lock()
		store.cacheLastSync = time.Now()
		store.cacheRemoteUnavailable = false
		store.cacheMu.Unlock()
		store.startCacheSync()
	}
	return store, nil
}

func openConfiguredLocalCache(settings config.LocalDatabaseCacheConfig, fallbackPath string, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) (*Store, error) {
	path := strings.TrimSpace(settings.Path)
	if path == "" {
		return nil, errors.New("server.database.cache.path is required when local cache is enabled")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(fallbackPath), path)
	}
	return OpenSQLitePool(filepath.Clean(path), maxOpen, maxIdle, maxLifetime, maxIdleTime)
}

func resolvedDatabasePool(settings config.DatabasePoolConfig) (int, int, time.Duration, time.Duration) {
	defaults := config.DefaultConfig().Server.Database.Pool
	if settings == (config.DatabasePoolConfig{}) {
		settings = defaults
	}
	if settings.MaxOpen < 1 {
		settings.MaxOpen = defaults.MaxOpen
	}
	if settings.MaxIdle < 0 {
		settings.MaxIdle = defaults.MaxIdle
	}
	if settings.MaxIdle > settings.MaxOpen {
		settings.MaxIdle = settings.MaxOpen
	}
	maxLifetime, err := time.ParseDuration(settings.MaxLifetime)
	if err != nil || maxLifetime <= 0 {
		maxLifetime, _ = time.ParseDuration(defaults.MaxLifetime)
	}
	maxIdleTime, err := time.ParseDuration(settings.MaxIdleTime)
	if err != nil || maxIdleTime <= 0 {
		maxIdleTime, _ = time.ParseDuration(defaults.MaxIdleTime)
	}
	return settings.MaxOpen, settings.MaxIdle, maxLifetime, maxIdleTime
}
