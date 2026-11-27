package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestHealthRequestStatsPersistLifetimeAndRollingDay(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTestDir(t), "health-stats.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.November, 26, 16, 30, 0, 0, time.UTC)
	batch := HealthRequestCounterBatch{
		Total: 9,
		Hours: []HealthRequestHourBucket{
			{HourStart: now.Add(-time.Hour), Count: 4},
			{HourStart: now, Count: 3},
			{HourStart: now.Add(-24 * time.Hour), Count: 2},
		},
	}
	if err := store.FlushHealthRequestCounters(context.Background(), batch, now); err != nil {
		t.Fatal(err)
	}
	stats, err := store.ReadHealthRequestStats(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 9 || stats.Last24Hour != 7 {
		t.Fatalf("health stats = %+v, want total 9 and rolling 24-hour count 7", stats)
	}
	if err := store.FlushHealthRequestCounters(context.Background(), HealthRequestCounterBatch{Total: 5, Hours: []HealthRequestHourBucket{{HourStart: now, Count: 5}}}, now); err != nil {
		t.Fatal(err)
	}
	stats, err = store.ReadHealthRequestStats(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 14 || stats.Last24Hour != 12 {
		t.Fatalf("health stats after second flush = %+v, want total 14 and rolling 24-hour count 12", stats)
	}
}
