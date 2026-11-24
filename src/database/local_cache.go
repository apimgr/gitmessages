package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var localCacheTables = []string{"app_secrets", "rate_limits", "audit_log", "owners", "api_tokens", "captcha_challenges", "security_reports", "scheduler_tasks", "scheduler_history", "security_thanks", "backups"}

var localCachePrimaryKeys = map[string]string{"app_secrets": "name", "rate_limits": "key", "audit_log": "id", "owners": "id", "api_tokens": "id", "captcha_challenges": "id", "security_reports": "tracking_id", "scheduler_tasks": "id", "scheduler_history": "id", "security_thanks": "tracking_id", "backups": "id"}

type cacheSyncFailure struct {
	remote bool
	err    error
}

func (failure cacheSyncFailure) Error() string { return failure.err.Error() }
func (failure cacheSyncFailure) Unwrap() error { return failure.err }

func (s *Store) attachLocalCache(cache *Store, maxAge, interval time.Duration, offlineMode bool) error {
	if s == nil || cache == nil || maxAge <= 0 || interval <= 0 {
		return errors.New("invalid local database cache configuration")
	}
	s.cache = cache
	s.cacheMaxAge = maxAge
	s.cacheSyncInterval = interval
	s.cacheOfflineMode = offlineMode
	if err := cache.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS cache_metadata (name TEXT PRIMARY KEY, value INTEGER NOT NULL)`)
		return err
	}); err != nil {
		return err
	}
	var synced int64
	if err := cache.Read().QueryRow(`SELECT value FROM cache_metadata WHERE name='last_sync'`).Scan(&synced); err == nil {
		s.cacheLastSync = time.Unix(synced, 0)
	}
	return nil
}

func (s *Store) startCacheSync() {
	if s == nil || s.cache == nil || s.cacheSyncInterval <= 0 {
		return
	}
	s.cacheStop = make(chan struct{})
	s.cacheDone = make(chan struct{})
	go func() {
		defer close(s.cacheDone)
		ticker := time.NewTicker(s.cacheSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), s.cacheSyncInterval)
				_ = s.syncLocalCache(ctx)
				cancel()
			case <-s.cacheStop:
				return
			}
		}
	}()
}

func (s *Store) syncLocalCache(ctx context.Context) error {
	if s == nil || s.cache == nil || s.read == nil || s.cache.write == nil {
		return errors.New("local database cache is unavailable")
	}
	s.cacheSyncMu.Lock()
	defer s.cacheSyncMu.Unlock()
	stageTables := make(map[string][]string, len(localCacheTables))
	stageTx, err := s.cache.write.BeginTx(ctx, nil)
	if err != nil {
		return cacheSyncFailure{err: fmt.Errorf("begin local cache staging: %w", err)}
	}
	for _, table := range localCacheTables {
		if _, err := stageTx.ExecContext(ctx, `DROP TABLE IF EXISTS cache_stage_`+table); err != nil {
			_ = stageTx.Rollback()
			return cacheSyncFailure{err: fmt.Errorf("reset staging table for %s: %w", table, err)}
		}
	}
	if err := stageTx.Commit(); err != nil {
		return cacheSyncFailure{err: fmt.Errorf("commit local cache staging reset: %w", err)}
	}
	for _, table := range localCacheTables {
		columns, err := orderedTableColumns(ctx, s.read, table)
		if err != nil {
			return s.remoteCacheFailure(ctx, fmt.Errorf("inspect remote cache table %s: %w", table, err))
		}
		s.cacheMu.Lock()
		s.cacheRemoteUnavailable = false
		s.cacheMu.Unlock()
		if len(columns) == 0 {
			return s.remoteCacheFailure(ctx, fmt.Errorf("remote cache table %s has no columns", table))
		}
		stageTables[table] = columns
		columnDefinitions := make([]string, len(columns))
		for index, column := range columns {
			columnDefinitions[index] = `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
		}
		createTx, err := s.cache.write.BeginTx(ctx, nil)
		if err != nil {
			return cacheSyncFailure{err: fmt.Errorf("begin staging table %s: %w", table, err)}
		}
		if _, err := createTx.ExecContext(ctx, `CREATE TABLE cache_stage_`+table+` (`+strings.Join(columnDefinitions, ",")+`)`); err != nil {
			_ = createTx.Rollback()
			return cacheSyncFailure{err: fmt.Errorf("create staging table for %s: %w", table, err)}
		}
		if err := createTx.Commit(); err != nil {
			return cacheSyncFailure{err: fmt.Errorf("commit staging table %s: %w", table, err)}
		}
		primaryKey, ok := localCachePrimaryKeys[table]
		if !ok {
			return cacheSyncFailure{err: fmt.Errorf("cache table %s has no configured primary key", table)}
		}
		primaryIndex := -1
		for index, column := range columns {
			if column == primaryKey {
				primaryIndex = index
				break
			}
		}
		if primaryIndex < 0 {
			return s.remoteCacheFailure(ctx, fmt.Errorf("remote cache table %s is missing primary key %s", table, primaryKey))
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		statement := `INSERT INTO cache_stage_` + table + ` (` + quoteColumns(columns) + `) VALUES (` + placeholders + `)`
		var lastKey any
		hasLastKey := false
		for {
			query := `SELECT ` + quoteColumns(columns) + ` FROM ` + table
			arguments := []any{}
			if hasLastKey {
				query += ` WHERE ` + quoteColumns([]string{primaryKey}) + ` > ?`
				arguments = append(arguments, lastKey)
			}
			query += ` ORDER BY ` + quoteColumns([]string{primaryKey}) + ` LIMIT 500`
			rows, err := s.read.QueryContext(ctx, query, arguments...)
			if err != nil {
				return s.remoteCacheFailure(ctx, fmt.Errorf("read remote cache table %s: %w", table, err))
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
					return s.remoteCacheFailure(ctx, fmt.Errorf("scan remote cache table %s: %w", table, err))
				}
				for index, value := range values {
					if bytes, ok := value.([]byte); ok {
						values[index] = append([]byte(nil), bytes...)
					}
				}
				batch = append(batch, values)
			}
			rowErr := rows.Err()
			closeErr := rows.Close()
			if rowErr != nil {
				return s.remoteCacheFailure(ctx, fmt.Errorf("read remote cache table %s: %w", table, rowErr))
			}
			if closeErr != nil {
				return s.remoteCacheFailure(ctx, fmt.Errorf("close remote cache table %s: %w", table, closeErr))
			}
			s.cacheMu.Lock()
			s.cacheRemoteUnavailable = false
			s.cacheMu.Unlock()
			if len(batch) == 0 {
				break
			}
			lastKey = batch[len(batch)-1][primaryIndex]
			hasLastKey = true
			batchTx, err := s.cache.write.BeginTx(ctx, nil)
			if err != nil {
				return cacheSyncFailure{err: fmt.Errorf("begin cache batch for %s: %w", table, err)}
			}
			prepared, err := batchTx.PrepareContext(ctx, statement)
			if err != nil {
				_ = batchTx.Rollback()
				return cacheSyncFailure{err: fmt.Errorf("prepare cache batch for %s: %w", table, err)}
			}
			var batchErr error
			for _, values := range batch {
				if _, batchErr = prepared.ExecContext(ctx, values...); batchErr != nil {
					break
				}
			}
			prepareErr := prepared.Close()
			if batchErr == nil {
				batchErr = prepareErr
			}
			if batchErr != nil {
				_ = batchTx.Rollback()
				return cacheSyncFailure{err: fmt.Errorf("write cache batch for %s: %w", table, batchErr)}
			}
			if err := batchTx.Commit(); err != nil {
				return cacheSyncFailure{err: fmt.Errorf("commit cache batch for %s: %w", table, err)}
			}
			if len(batch) < 500 {
				break
			}
		}
	}
	publishTx, err := s.cache.write.BeginTx(ctx, nil)
	if err != nil {
		return cacheSyncFailure{err: fmt.Errorf("begin local cache publish: %w", err)}
	}
	defer func() { _ = publishTx.Rollback() }()
	for index := len(localCacheTables) - 1; index >= 0; index-- {
		table := localCacheTables[index]
		if _, err := publishTx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return cacheSyncFailure{err: fmt.Errorf("clear published cache table %s: %w", table, err)}
		}
	}
	for _, table := range localCacheTables {
		columns := stageTables[table]
		columnList := quoteColumns(columns)
		if _, err := publishTx.ExecContext(ctx, `INSERT INTO `+table+` (`+columnList+`) SELECT `+columnList+` FROM cache_stage_`+table); err != nil {
			return cacheSyncFailure{err: fmt.Errorf("publish cached table %s: %w", table, err)}
		}
	}
	for _, table := range localCacheTables {
		if _, err := publishTx.ExecContext(ctx, `DROP TABLE cache_stage_`+table); err != nil {
			return cacheSyncFailure{err: fmt.Errorf("drop published staging table for %s: %w", table, err)}
		}
	}
	if _, err := publishTx.ExecContext(ctx, `INSERT INTO cache_metadata(name,value) VALUES('last_sync',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, time.Now().Unix()); err != nil {
		return cacheSyncFailure{err: fmt.Errorf("record local cache sync time: %w", err)}
	}
	if err := publishTx.Commit(); err != nil {
		return cacheSyncFailure{err: fmt.Errorf("commit local cache publish: %w", err)}
	}
	s.cacheMu.Lock()
	s.cacheLastSync = time.Now()
	s.cacheRemoteUnavailable = false
	s.cacheMu.Unlock()
	return nil
}

func (s *Store) remoteCacheFailure(ctx context.Context, err error) error {
	pingContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	pingErr := s.read.PingContext(pingContext)
	cancel()
	unavailable := pingErr != nil
	s.cacheMu.Lock()
	s.cacheRemoteUnavailable = unavailable
	s.cacheMu.Unlock()
	return cacheSyncFailure{remote: unavailable, err: err}
}

func orderedTableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	var columns []string
	for rows.Next() {
		var position, notNull, primary int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	return columns, errors.Join(rowErr, closeErr)
}

func quoteColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
	}
	return strings.Join(quoted, ",")
}
