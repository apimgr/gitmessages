package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

type QueryObservation struct {
	Operation string
	Table     string
	ErrorType string
	Duration  time.Duration
}

type queryObserver struct {
	mu       sync.RWMutex
	callback func(QueryObservation)
}

var sqliteDriverID atomic.Uint64

func (o *queryObserver) observe(query string, started time.Time, err error) {
	o.mu.RLock()
	callback := o.callback
	o.mu.RUnlock()
	if callback == nil {
		return
	}
	operation, table := queryLabels(query)
	errorType := ""
	if err != nil {
		errorType = classifyDatabaseError(err)
	}
	callback(QueryObservation{Operation: operation, Table: table, ErrorType: errorType, Duration: time.Since(started)})
}

type instrumentedDriver struct {
	inner    driver.Driver
	observer *queryObserver
}

func (d *instrumentedDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{Conn: connection, observer: d.observer}, nil
}

type legacyConnBegin interface{ Begin() (driver.Tx, error) }
type legacyStmtExec interface {
	Exec([]driver.Value) (driver.Result, error)
}
type legacyStmtQuery interface {
	Query([]driver.Value) (driver.Rows, error)
}

type instrumentedConn struct {
	driver.Conn
	observer *queryObserver
}

func (c *instrumentedConn) NewBackup(destinationURI string) (*sqlite.Backup, error) {
	backupper, ok := c.Conn.(interface {
		NewBackup(string) (*sqlite.Backup, error)
	})
	if !ok {
		return nil, errors.New("SQLite driver does not support online backups")
	}
	return backupper.NewBackup(destinationURI)
}

func (c *instrumentedConn) Prepare(query string) (driver.Stmt, error) {
	statement, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{Stmt: statement, query: query, observer: c.observer}, nil
}

func (c *instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var statement driver.Stmt
	var err error
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		statement, err = preparer.PrepareContext(ctx, query)
	} else {
		statement, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{Stmt: statement, query: query, observer: c.observer}, nil
}

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	executor, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	result, err := executor.ExecContext(ctx, query, args)
	c.observer.observe(query, started, err)
	return result, err
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	c.observer.observe(query, started, err)
	return rows, err
}

func (c *instrumentedConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, options)
	}
	if options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelDefault) {
		return nil, driver.ErrSkip
	}
	legacy, ok := c.Conn.(legacyConnBegin)
	if !ok {
		return nil, driver.ErrSkip
	}
	return legacy.Begin()
}

func (c *instrumentedConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *instrumentedConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (c *instrumentedConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

type instrumentedStmt struct {
	driver.Stmt
	query    string
	observer *queryObserver
}

func (s *instrumentedStmt) Exec(args []driver.Value) (driver.Result, error) {
	started := time.Now()
	legacy, ok := s.Stmt.(legacyStmtExec)
	if !ok {
		return nil, driver.ErrSkip
	}
	result, err := legacy.Exec(args)
	s.observer.observe(s.query, started, err)
	return result, err
}

func (s *instrumentedStmt) Query(args []driver.Value) (driver.Rows, error) {
	started := time.Now()
	legacy, ok := s.Stmt.(legacyStmtQuery)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := legacy.Query(args)
	s.observer.observe(s.query, started, err)
	return rows, err
}

func (s *instrumentedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	executor, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	result, err := executor.ExecContext(ctx, args)
	s.observer.observe(s.query, started, err)
	return result, err
}

func (s *instrumentedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	rows, err := queryer.QueryContext(ctx, args)
	s.observer.observe(s.query, started, err)
	return rows, err
}

func queryLabels(query string) (string, string) {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	operation := "other"
	for _, word := range words {
		word = strings.Trim(word, "();\"[]")
		switch word {
		case "select", "insert", "update", "delete":
			operation = word
		}
		if operation != "other" {
			break
		}
	}
	table := "unknown"
	for index, word := range words {
		word = strings.Trim(word, "();\"[]")
		if word != "from" && word != "into" && word != "update" {
			continue
		}
		if index+1 >= len(words) {
			break
		}
		candidate := strings.Trim(words[index+1], "();,\"[]")
		if separator := strings.IndexAny(candidate, "("); separator >= 0 {
			candidate = candidate[:separator]
		}
		parts := strings.Split(candidate, ".")
		candidate = parts[len(parts)-1]
		if candidate != "" {
			table = candidate
		}
		break
	}
	return operation, table
}

func classifyDatabaseError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, driver.ErrBadConn), strings.Contains(message, "connection"):
		return "connection"
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timeout"), strings.Contains(message, "locked"), strings.Contains(message, "busy"):
		return "timeout"
	case strings.Contains(message, "unique constraint"), strings.Contains(message, "primary key"):
		return "duplicate"
	case strings.Contains(message, "constraint"):
		return "constraint"
	default:
		return "other"
	}
}

func registerInstrumentedSQLite(observer *queryObserver) string {
	name := fmt.Sprintf("gitmessages-sqlite-%d", sqliteDriverID.Add(1))
	sql.Register(name, &instrumentedDriver{inner: &sqlite.Driver{}, observer: observer})
	return name
}
