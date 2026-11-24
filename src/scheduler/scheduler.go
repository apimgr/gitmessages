package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/gitmessages/src/database"
)

const DefaultTimezone = "America/New_York"

type TaskConfig struct {
	ID, Name, Schedule   string
	Enabled, RetryOnFail bool
	RetryDelay           time.Duration
	MaxRetries           int
}

type Task struct {
	ID, Name, Schedule    string
	Interval              time.Duration
	Func                  func() error
	LastRun, NextRun      time.Time
	LastStatus, LastError string
	RunCount, FailCount   int64
	Enabled, RetryOnFail  bool
	RetryDelay            time.Duration
	MaxRetries            int
}

type ExecutionEvent struct {
	Task     string
	Status   string
	Error    string
	Duration time.Duration
	Running  bool
	LastRun  time.Time
}

type Scheduler struct {
	tasks             map[string]*Task
	store             *database.Store
	loc               *time.Location
	catchUp           time.Duration
	mu                sync.RWMutex
	running, stopping bool
	stop              chan struct{}
	loopDone          chan struct{}
	active            sync.WaitGroup
	logger            func(string, string)
	observerMu        sync.RWMutex
	observers         []func(ExecutionEvent)
	failureHandler    func(Task, error)
}

func New() *Scheduler { s, _ := NewWithStore(nil, DefaultTimezone, time.Hour); return s }

// NewWithStore constructs a scheduler using persistent server.db state.
func NewWithStore(store *database.Store, timezone string, catchUp time.Duration) (*Scheduler, error) {
	return NewWithLogger(store, timezone, catchUp, nil)
}

// NewWithLogger constructs a persistent scheduler and reports timezone fallback warnings through logger.
func NewWithLogger(store *database.Store, timezone string, catchUp time.Duration, logger func(level, message string)) (*Scheduler, error) {
	if timezone == "" {
		timezone = DefaultTimezone
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil && timezone != DefaultTimezone {
		if logger != nil {
			logger("WARN", fmt.Sprintf("invalid server.scheduler.timezone %q, using default %q", timezone, DefaultTimezone))
		}
		timezone = DefaultTimezone
		loc, err = time.LoadLocation(timezone)
	}
	if err != nil {
		if logger != nil {
			logger("WARN", fmt.Sprintf("scheduler default timezone %q unavailable, using UTC", DefaultTimezone))
		}
		loc = time.UTC
	}
	if catchUp <= 0 {
		catchUp = time.Hour
	}
	if logger == nil {
		logger = func(string, string) {}
	}
	return &Scheduler{tasks: map[string]*Task{}, store: store, loc: loc, catchUp: catchUp, stop: make(chan struct{}), loopDone: make(chan struct{}), logger: logger}, nil
}

// SetLogger configures file-backed scheduler messages. An empty logger silences all task output.
func (s *Scheduler) SetLogger(logger func(level, message string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if logger == nil {
		s.logger = func(string, string) {}
		return
	}
	s.logger = logger
}

func (s *Scheduler) SetObserver(observer func(ExecutionEvent)) {
	s.observerMu.Lock()
	if observer == nil {
		s.observers = nil
	} else {
		s.observers = []func(ExecutionEvent){observer}
	}
	s.observerMu.Unlock()
}

func (s *Scheduler) AddObserver(observer func(ExecutionEvent)) {
	if observer == nil {
		return
	}
	s.observerMu.Lock()
	s.observers = append(s.observers, observer)
	s.observerMu.Unlock()
}

func (s *Scheduler) SetFailureHandler(handler func(Task, error)) {
	s.observerMu.Lock()
	s.failureHandler = handler
	s.observerMu.Unlock()
}

func (s *Scheduler) observe(event ExecutionEvent) {
	s.observerMu.RLock()
	observers := append([]func(ExecutionEvent){}, s.observers...)
	s.observerMu.RUnlock()
	for _, observer := range observers {
		func() {
			defer func() { _ = recover() }()
			observer(event)
		}()
	}
}

// AddScheduledTask registers a callback and restores its persistent state.
func (s *Scheduler) AddScheduledTask(c TaskConfig, fn func() error) error {
	if c.ID == "" {
		return errors.New("task ID is required")
	}
	if fn == nil {
		return errors.New("task callback is required")
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 5 * time.Minute
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	next, err := nextOccurrence(c.Schedule, time.Now().In(s.loc), s.loc)
	if err != nil {
		return err
	}
	t := &Task{ID: c.ID, Name: c.Name, Schedule: c.Schedule, Func: fn, Enabled: c.Enabled, RetryOnFail: c.RetryOnFail, RetryDelay: c.RetryDelay, MaxRetries: c.MaxRetries, NextRun: next}
	if strings.HasPrefix(c.Schedule, "@every ") {
		t.Interval, _ = time.ParseDuration(strings.TrimPrefix(c.Schedule, "@every "))
	}
	if s.store != nil {
		var enabled int
		var lastRun, nextRun sql.NullInt64
		var status, lastErr, storedSchedule sql.NullString
		var storedName sql.NullString
		err = s.store.Read().QueryRow(`SELECT enabled,last_run,last_status,last_error,next_run,run_count,fail_count,schedule,name FROM scheduler_tasks WHERE id=?`, c.ID).Scan(&enabled, &lastRun, &status, &lastErr, &nextRun, &t.RunCount, &t.FailCount, &storedSchedule, &storedName)
		if err == nil {
			t.Enabled = enabled != 0
			if lastRun.Valid {
				t.LastRun = time.Unix(lastRun.Int64, 0)
			}
			t.LastStatus = status.String
			t.LastError = lastErr.String
			if storedName.Valid && c.Name == c.ID {
				t.Name = storedName.String
			}
			if nextRun.Valid && storedSchedule.String == c.Schedule {
				t.NextRun = time.Unix(nextRun.Int64, 0)
			}
			if t.LastStatus == "running" {
				t.LastStatus = "failed"
				t.LastError = "task was interrupted before completion"
				t.FailCount++
				if t.Enabled {
					t.NextRun = time.Now().In(s.loc)
				}
				if err := s.finishInterruptedHistory(c.ID, t.LastError); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load scheduler task %q: %w", c.ID, err)
		}
		if err = s.persistTask(t); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.tasks[t.ID] = t
	s.mu.Unlock()
	return nil
}

// AddTask keeps source compatibility with the original interval API.
func (s *Scheduler) AddTask(name string, interval time.Duration, fn func() error) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if err := s.AddScheduledTask(TaskConfig{ID: name, Name: name, Schedule: "@every " + interval.String(), Enabled: true}, fn); err != nil {
		s.log("ERROR", fmt.Sprintf("register scheduler task %q: %v", name, err))
	}
}

func (s *Scheduler) RemoveTask(name string) {
	s.mu.Lock()
	delete(s.tasks, name)
	s.mu.Unlock()
	if s.store != nil {
		_ = s.store.Write(context.Background(), func(tx *sql.Tx) error { _, e := tx.Exec(`DELETE FROM scheduler_tasks WHERE id=?`, name); return e })
	}
}
func (s *Scheduler) EnableTask(name string)  { s.setEnabled(name, true) }
func (s *Scheduler) DisableTask(name string) { s.setEnabled(name, false) }
func (s *Scheduler) setEnabled(name string, v bool) {
	s.mu.Lock()
	t, ok := s.tasks[name]
	if ok {
		t.Enabled = v
		if v && t.NextRun.Before(time.Now()) {
			t.NextRun, _ = nextOccurrence(t.Schedule, time.Now().In(s.loc), s.loc)
		}
	}
	s.mu.Unlock()
	if ok && s.store != nil {
		_ = s.persistTask(t)
	}
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running || s.stopping {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	s.loopDone = make(chan struct{})
	now := time.Now().In(s.loc)
	due := []*Task{}
	for _, t := range s.tasks {
		if t.Enabled && !t.NextRun.IsZero() && !t.NextRun.After(now) {
			if now.Sub(t.NextRun) <= s.catchUp {
				due = append(due, t)
			} else {
				t.NextRun, _ = nextOccurrence(t.Schedule, now, s.loc)
				go func(task *Task) {
					if err := s.persistTask(task); err != nil {
						s.log("ERROR", fmt.Sprintf("persist scheduler task %q: %v", task.ID, err))
					}
				}(t)
			}
		}
	}
	s.mu.Unlock()
	tasks := s.GetTasks()
	names := make([]string, 0, len(tasks))
	for _, task := range tasks {
		names = append(names, task.ID)
	}
	sort.Strings(names)
	s.log("INFO", "scheduler started with tasks: "+strings.Join(names, ", "))
	go s.loop()
	sortTasks(due)
	for _, t := range due {
		s.dispatch(t)
	}
}

func (s *Scheduler) loop() {
	defer close(s.loopDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.runDueTasks(now)
		}
	}
}

func (s *Scheduler) runDueTasks(now time.Time) {
	s.mu.RLock()
	due := []*Task{}
	for _, t := range s.tasks {
		if t.Enabled && !t.NextRun.IsZero() && !now.Before(t.NextRun) {
			due = append(due, t)
		}
	}
	s.mu.RUnlock()
	sortTasks(due)
	for _, t := range due {
		s.dispatch(t)
	}
}

func (s *Scheduler) dispatch(t *Task) {
	s.mu.Lock()
	if s.stopping || !t.Enabled || t.NextRun.After(time.Now().In(s.loc)) {
		s.mu.Unlock()
		return
	}
	s.active.Add(1)
	t.NextRun, _ = nextOccurrence(t.Schedule, time.Now().In(s.loc), s.loc)
	s.mu.Unlock()
	go func() { defer s.active.Done(); _ = s.execute(t) }()
}

func callTask(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("task panic: %v", v)
		}
	}()
	return fn()
}

func (s *Scheduler) execute(t *Task) error {
	started := time.Now()
	s.observe(ExecutionEvent{Task: t.ID, Running: true})
	s.mu.Lock()
	t.LastRun = started
	t.LastStatus = "running"
	t.LastError = ""
	s.mu.Unlock()
	historyID, err := s.startHistory(t, started)
	if err != nil {
		s.log("ERROR", fmt.Sprintf("start scheduler history for %q: %v", t.ID, err))
	}
	err = nil
	exhausted := false
retryLoop:
	for attempt := 0; ; attempt++ {
		err = callTask(t.Func)
		if err == nil || !t.RetryOnFail || attempt >= t.MaxRetries {
			exhausted = err != nil && t.RetryOnFail && attempt >= t.MaxRetries
			break
		}
		delay := t.RetryDelay * time.Duration(1<<min(attempt, 10))
		select {
		case <-time.After(delay):
		case <-s.stop:
			err = errors.New("scheduler stopped during retry")
			exhausted = true
			break retryLoop
		}
	}
	s.mu.Lock()
	t.LastRun = time.Now()
	t.LastStatus = "success"
	t.LastError = ""
	if err != nil {
		t.LastStatus = "failed"
		t.LastError = err.Error()
		t.FailCount++
	} else {
		t.RunCount++
	}
	s.mu.Unlock()
	status := "success"
	if err != nil {
		status = "error"
	}
	event := ExecutionEvent{Task: t.ID, Status: status, Duration: time.Since(started), Running: false, LastRun: t.LastRun}
	if err != nil {
		event.Error = err.Error()
	}
	s.observe(event)
	if s.store != nil {
		if e := s.finishHistory(t, historyID, started, err); e != nil {
			s.log("ERROR", fmt.Sprintf("finish scheduler history for %q: %v", t.ID, e))
		}
		if e := s.persistTask(t); e != nil {
			s.log("ERROR", fmt.Sprintf("persist scheduler task %q: %v", t.ID, e))
		}
	}
	if err != nil {
		level := "WARN"
		if exhausted {
			level = "ERROR"
		}
		s.log(level, fmt.Sprintf("scheduler task %q failed: %v", t.ID, err))
		s.observerMu.RLock()
		handler := s.failureHandler
		s.observerMu.RUnlock()
		if handler != nil {
			func() {
				defer func() { _ = recover() }()
				handler(*t, err)
			}()
		}
	} else {
		s.log("DEBUG", fmt.Sprintf("scheduler task %q completed", t.ID))
	}
	return err
}

func (s *Scheduler) RunNow(name string) error {
	s.mu.RLock()
	t, ok := s.tasks[name]
	stopping := s.stopping
	enabled := ok && t.Enabled
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if stopping {
		return errors.New("scheduler is stopping")
	}
	if !enabled {
		return nil
	}
	done := make(chan error, 1)
	s.mu.Lock()
	stopping, enabled = s.stopping, t.Enabled
	if stopping || !enabled {
		s.mu.Unlock()
		if stopping {
			return errors.New("scheduler is stopping")
		}
		return nil
	}
	s.active.Add(1)
	t.NextRun, _ = nextOccurrence(t.Schedule, time.Now().In(s.loc), s.loc)
	s.mu.Unlock()
	go func() { defer s.active.Done(); done <- s.execute(t) }()
	return <-done
}

// Stop prevents new work and waits at most 30 seconds for active callbacks.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	s.running = false
	close(s.stop)
	loopDone := s.loopDone
	s.mu.Unlock()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	select {
	case <-loopDone:
	case <-deadline.C:
		s.log("WARN", "scheduler shutdown timed out; active tasks will retry after restart")
		return
	}
	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
	case <-deadline.C:
		s.log("WARN", "scheduler shutdown timed out; active tasks will retry after restart")
	}
	s.mu.Lock()
	s.stopping = false
	s.mu.Unlock()
}

func (s *Scheduler) GetTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	return out
}

func (s *Scheduler) persistTask(t *Task) error {
	if s.store == nil {
		return nil
	}
	return s.store.Write(context.Background(), func(tx *sql.Tx) error { return upsertTask(tx, t) })
}

func (s *Scheduler) startHistory(t *Task, started time.Time) (int64, error) {
	if s.store == nil {
		return 0, nil
	}
	var id int64
	err := s.store.Write(context.Background(), func(tx *sql.Tx) error {
		if err := upsertTask(tx, t); err != nil {
			return err
		}
		result, err := tx.Exec(`INSERT INTO scheduler_history(task_id,started_at,status) VALUES(?,?,'running')`, t.ID, started.Unix())
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	return id, err
}

func (s *Scheduler) finishHistory(t *Task, id int64, started time.Time, runErr error) error {
	if s.store == nil {
		return nil
	}
	status := "success"
	var detail any
	if runErr != nil {
		status = "failed"
		detail = runErr.Error()
	}
	finished := time.Now()
	return s.store.Write(context.Background(), func(tx *sql.Tx) error {
		if err := upsertTask(tx, t); err != nil {
			return err
		}
		if id != 0 {
			if _, err := tx.Exec(`UPDATE scheduler_history SET finished_at=?,status=?,error=?,duration_ms=? WHERE id=?`, finished.Unix(), status, detail, finished.Sub(started).Milliseconds(), id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM scheduler_history WHERE task_id=? AND id NOT IN (SELECT id FROM scheduler_history WHERE task_id=? ORDER BY started_at DESC,id DESC LIMIT 100)`, t.ID, t.ID)
		return err
	})
}

func (s *Scheduler) finishInterruptedHistory(taskID, detail string) error {
	if s.store == nil {
		return nil
	}
	now := time.Now().Unix()
	return s.store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE scheduler_history SET finished_at=?,status='failed',error=?,duration_ms=MAX(0,(?-started_at)*1000) WHERE task_id=? AND finished_at IS NULL`, now, detail, now, taskID)
		return err
	})
}

func upsertTask(tx *sql.Tx, t *Task) error {
	var last, next any
	if !t.LastRun.IsZero() {
		last = t.LastRun.Unix()
	}
	if !t.NextRun.IsZero() {
		next = t.NextRun.Unix()
	}
	enabled := 0
	if t.Enabled {
		enabled = 1
	}
	_, err := tx.Exec(`INSERT INTO scheduler_tasks(id,name,schedule,last_run,last_status,last_error,next_run,run_count,fail_count,enabled) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,schedule=excluded.schedule,last_run=excluded.last_run,last_status=excluded.last_status,last_error=excluded.last_error,next_run=excluded.next_run,run_count=excluded.run_count,fail_count=excluded.fail_count,enabled=excluded.enabled`, t.ID, t.Name, t.Schedule, last, t.LastStatus, t.LastError, next, t.RunCount, t.FailCount, enabled)
	return err
}

func sortTasks(ts []*Task) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].NextRun.Before(ts[j-1].NextRun); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func ParseInterval(v string) time.Duration {
	switch v {
	case "minutely":
		return time.Minute
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	}
	if d, e := time.ParseDuration(v); e == nil && d > 0 {
		return d
	}
	return 24 * time.Hour
}

func (s *Scheduler) log(level, message string) {
	s.mu.RLock()
	logger := s.logger
	s.mu.RUnlock()
	if logger != nil {
		logger(level, message)
	}
}

func nextOccurrence(schedule string, after time.Time, loc *time.Location) (time.Time, error) {
	schedule = strings.TrimSpace(schedule)
	if strings.HasPrefix(schedule, "@every ") {
		d, e := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(schedule, "@every ")))
		if e != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("invalid interval schedule %q", schedule)
		}
		return after.Add(d), nil
	}
	aliases := map[string]string{"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *"}
	if v, ok := aliases[schedule]; ok {
		schedule = v
	}
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("invalid cron schedule %q: expected five fields", schedule)
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		m, e := parseField(f, ranges[i][0], ranges[i][1])
		if e != nil {
			return time.Time{}, fmt.Errorf("invalid cron schedule %q: %w", schedule, e)
		}
		sets[i] = m
	}
	c := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 60*24*366*5; i++ {
		minute, hour, day, month, wday := c.Minute(), c.Hour(), c.Day(), int(c.Month()), int(c.Weekday())
		dm, wm := sets[2][day], sets[4][wday]
		var dayOK bool
		if fields[2] == "*" {
			dayOK = wm
		} else if fields[4] == "*" {
			dayOK = dm
		} else {
			dayOK = dm || wm
		}
		if sets[0][minute] && sets[1][hour] && dayOK && sets[3][month] {
			return c, nil
		}
		c = c.Add(time.Minute)
	}
	return time.Time{}, errors.New("cron schedule has no occurrence within five years")
}

func parseField(f string, minV, maxV int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, p := range strings.Split(f, ",") {
		step := 1
		base := p
		if v := strings.Split(p, "/"); len(v) == 2 {
			base = v[0]
			n, e := strconv.Atoi(v[1])
			if e != nil || n <= 0 {
				return nil, errors.New("invalid step")
			}
			step = n
		} else if len(v) > 2 {
			return nil, errors.New("invalid step")
		}
		start, end := minV, maxV
		if base != "*" {
			b := strings.Split(base, "-")
			if len(b) > 2 {
				return nil, errors.New("invalid range")
			}
			n, e := strconv.Atoi(b[0])
			if e != nil {
				return nil, errors.New("invalid value")
			}
			start, end = n, n
			if len(b) == 2 {
				end, e = strconv.Atoi(b[1])
				if e != nil {
					return nil, errors.New("invalid range")
				}
			}
		}
		if start < minV || end > maxV || start > end {
			return nil, errors.New("value out of range")
		}
		for n := start; n <= end; n += step {
			out[n] = true
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty field")
	}
	return out, nil
}
