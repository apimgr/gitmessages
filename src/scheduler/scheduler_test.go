package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

// TestAddRemoveTask covers task registration and removal via GetTasks.
func TestAddRemoveTask(t *testing.T) {
	s := New()
	s.AddTask("t1", time.Hour, func() error { return nil })

	tasks := s.GetTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Name != "t1" {
		t.Errorf("task name = %q, want t1", tasks[0].Name)
	}
	if !tasks[0].Enabled {
		t.Error("newly added task should be enabled")
	}

	s.RemoveTask("t1")
	if len(s.GetTasks()) != 0 {
		t.Fatalf("expected 0 tasks after removal, got %d", len(s.GetTasks()))
	}

	// Removing an unknown task must not panic.
	s.RemoveTask("does-not-exist")
}

// TestEnableDisableTask covers the state-transition helpers, including the
// no-op branch for an unknown task name.
func TestEnableDisableTask(t *testing.T) {
	s := New()
	s.AddTask("t1", time.Hour, func() error { return nil })

	s.DisableTask("t1")
	tasks := s.GetTasks()
	if tasks[0].Enabled {
		t.Error("DisableTask() should mark task disabled")
	}

	s.EnableTask("t1")
	tasks = s.GetTasks()
	if !tasks[0].Enabled {
		t.Error("EnableTask() should mark task enabled")
	}

	// Unknown task names must be silently ignored, not panic.
	s.EnableTask("unknown")
	s.DisableTask("unknown")
}

// TestRunNow covers a successful run, an error-returning task, and the
// not-found case (which silently returns nil per current behavior).
func TestRunNow(t *testing.T) {
	s := New()

	calls := 0
	s.AddTask("ok-task", time.Hour, func() error {
		calls++
		return nil
	})
	if err := s.RunNow("ok-task"); err != nil {
		t.Fatalf("RunNow() error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected task func called once, got %d", calls)
	}

	boom := errors.New("boom")
	s.AddTask("err-task", time.Hour, func() error { return boom })
	if err := s.RunNow("err-task"); !errors.Is(err, boom) {
		t.Fatalf("RunNow() error = %v, want %v", err, boom)
	}

	// RunNow on an unregistered task name currently returns nil rather
	// than an error - documenting real (if surprising) behavior.
	if err := s.RunNow("missing"); err != nil {
		t.Fatalf("RunNow() for missing task = %v, want nil", err)
	}
}

// TestRunNow_UpdatesLastRunAndNextRun verifies bookkeeping fields advance
// after a synchronous run.
func TestRunNow_UpdatesLastRunAndNextRun(t *testing.T) {
	s := New()
	s.AddTask("t1", time.Minute, func() error { return nil })

	before := time.Now()
	if err := s.RunNow("t1"); err != nil {
		t.Fatalf("RunNow() error: %v", err)
	}

	tasks := s.GetTasks()
	if tasks[0].LastRun.Before(before) {
		t.Error("LastRun was not updated to after the call time")
	}
	if !tasks[0].NextRun.After(tasks[0].LastRun) {
		t.Error("NextRun should be after LastRun")
	}
}

// TestStartStop_Idempotent verifies calling Start/Stop twice hits the
// early-return guards without hanging or panicking, and does not wait on
// the internal 30s ticker.
func TestStartStop_Idempotent(t *testing.T) {
	s := New()
	s.Start()
	// Second call must hit the early-return guard since running is
	// already true.
	s.Start()
	s.Stop()
	// Second call must hit the early-return guard since running is
	// already false.
	s.Stop()
}

// TestParseInterval is table-driven over every named alias, a custom
// parseable duration, and an unparseable fallback.
func TestParseInterval(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"minutely", "minutely", time.Minute},
		{"hourly", "hourly", time.Hour},
		{"daily", "daily", 24 * time.Hour},
		{"weekly", "weekly", 7 * 24 * time.Hour},
		{"monthly", "monthly", 30 * 24 * time.Hour},
		{"custom parseable duration", "90s", 90 * time.Second},
		{"unparseable falls back to daily", "not-a-duration", 24 * time.Hour},
		{"empty falls back to daily", "", 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseInterval(tt.in); got != tt.want {
				t.Errorf("ParseInterval(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNextOccurrenceCronAndAliases(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.January, 10, 12, 0, 0, 0, loc)
	got, err := nextOccurrence("0 3 * * 0", start, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.January, 11, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("weekly occurrence = %v, want %v", got, want)
	}
	got, err = nextOccurrence("@every 15m", start, loc)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(start.Add(15 * time.Minute)) {
		t.Fatalf("interval occurrence = %v", got)
	}
}

func TestRunNowRecoversTaskPanic(t *testing.T) {
	s := New()
	s.AddTask("panic-task", time.Hour, func() error { panic("unexpected") })
	if err := s.RunNow("panic-task"); err == nil {
		t.Fatal("RunNow() should return an error for a task panic")
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].LastStatus != "failed" || tasks[0].LastError == "" {
		t.Fatalf("panic state = %+v", tasks)
	}
}

func TestSchedulerLogsRegisteredTasksAndTimezoneFallback(t *testing.T) {
	var mu sync.Mutex
	var entries []string
	logger := func(level, message string) {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, level+" "+message)
	}
	s, err := NewWithLogger(nil, "No/Such_Zone", time.Hour, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScheduledTask(TaskConfig{ID: "token_cleanup", Name: "Token Cleanup", Schedule: "@every 15m", Enabled: true}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Stop()
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(entries, "\n")
	if !strings.Contains(joined, `invalid server.scheduler.timezone "No/Such_Zone", using default "America/New_York"`) {
		t.Fatalf("timezone warning missing: %s", joined)
	}
	if !strings.Contains(joined, "INFO scheduler started with tasks: token_cleanup") {
		t.Fatalf("registered-task startup line missing: %s", joined)
	}
}

func TestPersistentExecutionWritesTaskAndHistory(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, err := NewWithStore(store, DefaultTimezone, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScheduledTask(TaskConfig{ID: "persisted", Name: "Persisted Task", Schedule: "@every 24h", Enabled: true}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow("persisted"); err != nil {
		t.Fatal(err)
	}
	var name, status string
	var runs int
	if err := store.Read().QueryRow(`SELECT name,last_status,run_count FROM scheduler_tasks WHERE id='persisted'`).Scan(&name, &status, &runs); err != nil {
		t.Fatal(err)
	}
	if name != "Persisted Task" || status != "success" || runs != 1 {
		t.Fatalf("persisted task = %q %q %d", name, status, runs)
	}
	var historyStatus string
	var finished sql.NullInt64
	if err := store.Read().QueryRow(`SELECT status,finished_at FROM scheduler_history WHERE task_id='persisted'`).Scan(&historyStatus, &finished); err != nil {
		t.Fatal(err)
	}
	if historyStatus != "success" || !finished.Valid {
		t.Fatalf("history = %q, finished=%v", historyStatus, finished)
	}
}

func TestPersistentExecutionRecoversInterruptedTask(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "server.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO scheduler_tasks(id,name,schedule,last_status,enabled) VALUES('interrupted','Interrupted','@every 1h','running',1)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO scheduler_history(task_id,started_at,status) VALUES('interrupted',?,'running')`, time.Now().Add(-time.Minute).Unix())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithStore(store, DefaultTimezone, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScheduledTask(TaskConfig{ID: "interrupted", Name: "Interrupted", Schedule: "@every 1h", Enabled: true}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var status, detail string
	var failures int
	if err := store.Read().QueryRow(`SELECT last_status,last_error,fail_count FROM scheduler_tasks WHERE id='interrupted'`).Scan(&status, &detail, &failures); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || detail == "" || failures != 1 {
		t.Fatalf("recovered task = %q %q %d", status, detail, failures)
	}
	var historyStatus string
	var finished sql.NullInt64
	if err := store.Read().QueryRow(`SELECT status,finished_at FROM scheduler_history WHERE task_id='interrupted'`).Scan(&historyStatus, &finished); err != nil {
		t.Fatal(err)
	}
	if historyStatus != "failed" || !finished.Valid {
		t.Fatalf("recovered history = %q, finished=%v", historyStatus, finished)
	}
}

func TestRunNowSkipsDisabledTask(t *testing.T) {
	s := New()
	calls := 0
	if err := s.AddScheduledTask(TaskConfig{ID: "disabled", Name: "Disabled", Schedule: "@every 1h", Enabled: false}, func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow("disabled"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("disabled callback ran %d times", calls)
	}
}

func TestFailureHandlerReceivesTaskErrorsWithoutAffectingTask(t *testing.T) {
	s := New()
	wantErr := errors.New("task failed")
	var handled []string
	s.SetFailureHandler(func(task Task, err error) {
		handled = append(handled, task.ID+":"+err.Error())
	})
	s.AddTask("failed-task", time.Hour, func() error { return wantErr })
	if err := s.RunNow("failed-task"); !errors.Is(err, wantErr) {
		t.Fatalf("RunNow() error = %v, want %v", err, wantErr)
	}
	if len(handled) != 1 || handled[0] != "failed-task:task failed" {
		t.Fatalf("failure handler events = %v", handled)
	}
}

func TestSchedulerObserversComposeAndRecoverIndependently(t *testing.T) {
	s := New()
	first, second := 0, 0
	s.SetObserver(func(ExecutionEvent) { first++; panic("observer failure") })
	s.AddObserver(func(ExecutionEvent) { second++ })
	s.AddTask("observed", time.Hour, func() error { return nil })
	if err := s.RunNow("observed"); err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if first != 2 || second != 2 {
		t.Fatalf("observer calls = first:%d second:%d, want 2 each", first, second)
	}
}
