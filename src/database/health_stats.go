package database

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type HealthRequestHourBucket struct {
	HourStart time.Time
	Count     int64
}

type HealthRequestCounterBatch struct {
	Total int64
	Hours []HealthRequestHourBucket
}

type HealthRequestStats struct {
	Total      int64
	Last24Hour int64
	Hours      []HealthRequestHourBucket
}

func (store *Store) FlushHealthRequestCounters(ctx context.Context, batch HealthRequestCounterBatch, now time.Time) error {
	if batch.Total < 0 {
		return errors.New("health request count cannot be negative")
	}
	for _, bucket := range batch.Hours {
		if bucket.Count < 0 {
			return errors.New("health request bucket count cannot be negative")
		}
	}
	if batch.Total == 0 && len(batch.Hours) == 0 {
		return nil
	}
	currentHour := now.UTC().Truncate(time.Hour).Unix()
	oldestHour := currentHour - 23*int64(time.Hour/time.Second)
	return store.Write(ctx, func(tx *sql.Tx) error {
		if batch.Total > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE health_request_stats SET total_count=total_count+? WHERE id=1`, batch.Total); err != nil {
				return err
			}
		}
		for _, bucket := range batch.Hours {
			if bucket.Count == 0 {
				continue
			}
			hourStart := bucket.HourStart.UTC().Truncate(time.Hour).Unix()
			if _, err := tx.ExecContext(ctx, `INSERT INTO health_request_buckets(hour_start,count) VALUES(?,?) ON CONFLICT(hour_start) DO UPDATE SET count=count+excluded.count`, hourStart, bucket.Count); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM health_request_buckets WHERE hour_start<?`, oldestHour)
		return err
	})
}

func (store *Store) ReadHealthRequestStats(ctx context.Context, now time.Time) (HealthRequestStats, error) {
	currentHour := now.UTC().Truncate(time.Hour).Unix()
	oldestHour := currentHour - 23*int64(time.Hour/time.Second)
	var stats HealthRequestStats
	if err := store.Read().QueryRowContext(ctx, `SELECT total_count FROM health_request_stats WHERE id=1`).Scan(&stats.Total); err != nil {
		return HealthRequestStats{}, err
	}
	rows, err := store.Read().QueryContext(ctx, `SELECT hour_start,count FROM health_request_buckets WHERE hour_start>=? ORDER BY hour_start`, oldestHour)
	if err != nil {
		return HealthRequestStats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var hourStart, count int64
		if err := rows.Scan(&hourStart, &count); err != nil {
			return HealthRequestStats{}, err
		}
		stats.Hours = append(stats.Hours, HealthRequestHourBucket{HourStart: time.Unix(hourStart, 0).UTC(), Count: count})
		stats.Last24Hour += count
	}
	if err := rows.Err(); err != nil {
		return HealthRequestStats{}, err
	}
	return stats, nil
}
