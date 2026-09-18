package scheduler

import (
	"errors"
	"testing"
	"time"
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
