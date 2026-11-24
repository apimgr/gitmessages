package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"

	"github.com/tursodatabase/libsql-client-go/libsql"
)

const libSQLDriverName = "gitmessages-libsql"

func init() {
	sql.Register(libSQLDriverName, prefixDriver{base: libsql.Driver{}})
}

type prefixDriver struct{ base driver.Driver }

func (driver prefixDriver) Open(name string) (driver.Conn, error) {
	connection, err := driver.base.Open(name)
	if err != nil {
		return nil, err
	}
	return prefixConn{base: connection}, nil
}

type prefixConn struct{ base driver.Conn }

func (connection prefixConn) Prepare(query string) (driver.Stmt, error) {
	statement, err := connection.base.Prepare(mapLibSQLIdentifiers(query))
	if err != nil {
		return nil, err
	}
	return statement, nil
}

func (connection prefixConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := connection.base.(driver.ConnPrepareContext); ok {
		statement, err := preparer.PrepareContext(ctx, mapLibSQLIdentifiers(query))
		if err != nil {
			return nil, err
		}
		return statement, nil
	}
	return connection.Prepare(query)
}

func (connection prefixConn) Close() error { return connection.base.Close() }

func (connection prefixConn) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection prefixConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := connection.base.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, options)
	}
	return nil, errors.New("libSQL connection does not support context-aware transactions")
}

func (connection prefixConn) Ping(ctx context.Context) error {
	if pinger, ok := connection.base.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (connection prefixConn) ExecContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	executor, ok := connection.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return executor.ExecContext(ctx, mapLibSQLIdentifiers(query), arguments)
}

func (connection prefixConn) QueryContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := connection.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, mapLibSQLIdentifiers(query), arguments)
}

func (connection prefixConn) IsValid() bool {
	if validator, ok := connection.base.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (connection prefixConn) ResetSession(ctx context.Context) error {
	if resetter, ok := connection.base.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

type prefixName string

const (
	prefixTableAppSecrets       prefixName = "app_secrets"
	prefixTableRateLimits       prefixName = "rate_limits"
	prefixTableAuditLog         prefixName = "audit_log"
	prefixTableOwners           prefixName = "owners"
	prefixTableAPITokens        prefixName = "api_tokens"
	prefixTableCaptcha          prefixName = "captcha_challenges"
	prefixTableSecurityReports  prefixName = "security_reports"
	prefixTableSchedulerTasks   prefixName = "scheduler_tasks"
	prefixTableSchedulerHistory prefixName = "scheduler_history"
	prefixTableSecurityThanks   prefixName = "security_thanks"
	prefixTableBackups          prefixName = "backups"
	prefixIndexRateLimits       prefixName = "idx_rate_limits_window"
	prefixIndexAuditTimestamp   prefixName = "idx_audit_timestamp"
	prefixIndexAuditCategory    prefixName = "idx_audit_category"
	prefixIndexSchedulerTask    prefixName = "idx_scheduler_history_task"
	prefixIndexSchedulerStart   prefixName = "idx_scheduler_history_started"
	prefixIndexTokenPrimary     prefixName = "idx_api_tokens_primary"
	prefixIndexTokenHash        prefixName = "idx_api_tokens_hash"
	prefixIndexTokenPrefix      prefixName = "idx_api_tokens_prefix"
	prefixIndexTokenOwner       prefixName = "idx_api_tokens_owner"
	prefixIndexTokenActive      prefixName = "idx_api_tokens_active"
	prefixIndexCaptchaExpiry    prefixName = "idx_captcha_challenges_expires"
	prefixIndexReportStatus     prefixName = "idx_security_reports_status"
)

var libSQLNames = map[string]prefixName{
	string(prefixTableAppSecrets): prefixTableAppSecrets, string(prefixTableRateLimits): prefixTableRateLimits,
	string(prefixTableAuditLog): prefixTableAuditLog, string(prefixTableOwners): prefixTableOwners,
	string(prefixTableAPITokens): prefixTableAPITokens, string(prefixTableCaptcha): prefixTableCaptcha,
	string(prefixTableSecurityReports): prefixTableSecurityReports, string(prefixTableSchedulerTasks): prefixTableSchedulerTasks,
	string(prefixTableSchedulerHistory): prefixTableSchedulerHistory, string(prefixTableSecurityThanks): prefixTableSecurityThanks,
	string(prefixTableBackups):    prefixTableBackups,
	string(prefixIndexRateLimits): prefixIndexRateLimits, string(prefixIndexAuditTimestamp): prefixIndexAuditTimestamp,
	string(prefixIndexAuditCategory): prefixIndexAuditCategory, string(prefixIndexTokenPrimary): prefixIndexTokenPrimary,
	string(prefixIndexSchedulerTask): prefixIndexSchedulerTask, string(prefixIndexSchedulerStart): prefixIndexSchedulerStart,
	string(prefixIndexTokenHash): prefixIndexTokenHash, string(prefixIndexTokenPrefix): prefixIndexTokenPrefix,
	string(prefixIndexTokenOwner): prefixIndexTokenOwner, string(prefixIndexTokenActive): prefixIndexTokenActive,
	string(prefixIndexCaptchaExpiry): prefixIndexCaptchaExpiry, string(prefixIndexReportStatus): prefixIndexReportStatus,
}

// mapLibSQLName is the single mapping point for remote server table and index names.
func mapLibSQLName(name string) string {
	if _, exists := libSQLNames[strings.ToLower(name)]; exists {
		return "srv_" + strings.ToLower(name)
	}
	return name
}

func mapLibSQLIdentifiers(query string) string {
	var output strings.Builder
	output.Grow(len(query) + 32)
	for index := 0; index < len(query); {
		character := query[index]
		switch character {
		case '\'', '"', '`':
			end := quotedEnd(query, index, character)
			if character == '\'' {
				output.WriteString(query[index:end])
			} else {
				identifier := query[index+1 : end-1]
				mapped := mapLibSQLName(identifier)
				output.WriteByte(character)
				output.WriteString(mapped)
				output.WriteByte(character)
			}
			index = end
		case '[':
			end := index + 1
			for end < len(query) && query[end] != ']' {
				end++
			}
			if end < len(query) {
				end++
			}
			output.WriteByte('[')
			output.WriteString(mapLibSQLName(query[index+1 : end-1]))
			output.WriteByte(']')
			index = end
		case '-', '/':
			if index+1 < len(query) && character == '-' && query[index+1] == '-' {
				end := index + 2
				for end < len(query) && query[end] != '\n' {
					end++
				}
				output.WriteString(query[index:end])
				index = end
			} else if index+1 < len(query) && character == '/' && query[index+1] == '*' {
				end := strings.Index(query[index+2:], "*/")
				if end < 0 {
					output.WriteString(query[index:])
					return output.String()
				}
				end += index + 4
				output.WriteString(query[index:end])
				index = end
			} else {
				output.WriteByte(character)
				index++
			}
		default:
			if isSQLIdentifierStart(character) {
				end := index + 1
				for end < len(query) && isSQLIdentifierPart(query[end]) {
					end++
				}
				output.WriteString(mapLibSQLName(query[index:end]))
				index = end
			} else {
				output.WriteByte(character)
				index++
			}
		}
	}
	return output.String()
}

func quotedEnd(query string, start int, quote byte) int {
	for index := start + 1; index < len(query); index++ {
		if query[index] != quote {
			continue
		}
		if index+1 < len(query) && query[index+1] == quote {
			index++
			continue
		}
		return index + 1
	}
	return len(query)
}

func isSQLIdentifierStart(character byte) bool {
	return character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func isSQLIdentifierPart(character byte) bool {
	return isSQLIdentifierStart(character) || character >= '0' && character <= '9' || character == '$'
}

var (
	_ driver.Driver             = prefixDriver{}
	_ driver.Conn               = prefixConn{}
	_ driver.ConnPrepareContext = prefixConn{}
	_ driver.ConnBeginTx        = prefixConn{}
	_ driver.Pinger             = prefixConn{}
	_ driver.ExecerContext      = prefixConn{}
	_ driver.QueryerContext     = prefixConn{}
	_ driver.Validator          = prefixConn{}
	_ driver.SessionResetter    = prefixConn{}
)
