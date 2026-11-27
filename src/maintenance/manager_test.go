package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerRetriesUntilRecovery(t *testing.T) {
	var attempts int
	recovered := make(chan Info, 1)
	manager, err := New(Config{RetryInterval: "5ms"}, func(context.Context, string) error {
		attempts++
		if attempts < 2 {
			return errors.New("still unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetHandlers(nil, func(info Info) { recovered <- info }, nil)
	manager.Start(context.Background())
	t.Cleanup(manager.Stop)
	manager.Enter("database_connection", "Database connection failed")
	select {
	case previous := <-recovered:
		if previous.Reason != "database_connection" || previous.SelfHealing.Attempts != 2 {
			t.Fatalf("recovery state = %+v", previous)
		}
	case <-time.After(time.Second):
		t.Fatal("maintenance manager did not recover")
	}
	if _, active := manager.Snapshot(); active {
		t.Fatal("maintenance mode remained active after recovery")
	}
}

func TestManagerExhaustsConfiguredAttempts(t *testing.T) {
	exhausted := make(chan Info, 1)
	manager, err := New(Config{RetryInterval: "5ms", MaxAttempts: 2}, func(context.Context, string) error {
		return errors.New("still unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetHandlers(nil, nil, func(info Info) { exhausted <- info })
	manager.Start(context.Background())
	t.Cleanup(manager.Stop)
	manager.Enter("file_write", "Unable to write a file")
	deadline := time.After(time.Second)
	for {
		info, active := manager.Snapshot()
		if active && info.SelfHealing.Exhausted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("maintenance manager did not exhaust retries")
		case <-time.After(time.Millisecond):
		}
	}
	info := <-exhausted
	if info.SelfHealing.Enabled || !info.SelfHealing.Exhausted || info.SelfHealing.Attempts != 2 || info.SelfHealing.NextAttempt != nil {
		t.Fatalf("exhausted maintenance state = %+v", info.SelfHealing)
	}
}

func TestManagerDefaultsRetryIntervalAndValidatesConfiguration(t *testing.T) {
	manager, err := New(Config{}, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if manager.interval != 30*time.Second {
		t.Fatalf("retry interval = %s, want 30s", manager.interval)
	}
	if _, err := New(Config{RetryInterval: "0s"}, func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("zero retry interval was accepted")
	}
	if _, err := New(Config{MaxAttempts: -1}, func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("negative maximum attempts was accepted")
	}
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("missing self-healing callback was accepted")
	}
}
