package database

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS app_secrets (
		name TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS app_secret_revocations (
		name TEXT NOT NULL,
		version INTEGER NOT NULL,
		node_id TEXT NOT NULL,
		revoked_at TEXT NOT NULL,
		PRIMARY KEY (name, version, node_id)
	)`,
	`CREATE TABLE IF NOT EXISTS rate_limits (
		key TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 1,
		window_start INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s','now'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_rate_limits_window ON rate_limits(window_start)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		level TEXT NOT NULL DEFAULT 'info',
		category TEXT NOT NULL,
		action TEXT NOT NULL,
		actor_ip TEXT,
		target_type TEXT,
		target_id TEXT,
		details TEXT,
		success INTEGER NOT NULL DEFAULT 1
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_log(category)`,
	`CREATE TABLE IF NOT EXISTS owners (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s','now'))
	)`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')),
		name TEXT,
		token_hash TEXT NOT NULL UNIQUE,
		token_prefix TEXT NOT NULL,
		token_last4 TEXT NOT NULL,
		token_enc TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		rotated_at INTEGER,
		expires_at INTEGER,
		last_used_at INTEGER,
		revoked_at INTEGER,
		revoked_reason TEXT
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(token_prefix)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,
	`CREATE TABLE IF NOT EXISTS captcha_challenges (
		id TEXT PRIMARY KEY,
		answer_hash TEXT NOT NULL,
		expires_at TIMESTAMP NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expires ON captcha_challenges(expires_at)`,
	`CREATE TABLE IF NOT EXISTS security_reports (
		tracking_id TEXT PRIMARY KEY,
		id_window INTEGER NOT NULL,
		severity TEXT NOT NULL,
		component TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'received',
		body_enc BLOB NOT NULL,
		body_alg TEXT NOT NULL,
		contact_enc BLOB,
		credit_pref TEXT NOT NULL DEFAULT 'no',
		credit_name TEXT,
		researcher_comment TEXT NOT NULL DEFAULT '',
		disclosure_date DATE,
		cve_requested BOOLEAN NOT NULL DEFAULT FALSE,
		token_hash TEXT NOT NULL,
		token_last_used DATE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		closed_at TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_security_reports_status ON security_reports(status, created_at)`,
	`CREATE TABLE IF NOT EXISTS scheduler_tasks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		schedule TEXT NOT NULL,
		last_run INTEGER,
		last_status TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		next_run INTEGER,
		run_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
	)`,
	`CREATE TABLE IF NOT EXISTS scheduler_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		finished_at INTEGER,
		status TEXT NOT NULL,
		error TEXT,
		duration_ms INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS security_thanks (
		tracking_id TEXT PRIMARY KEY,
		credit TEXT NOT NULL,
		severity TEXT NOT NULL,
		disclosed_on DATE NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS pgp_key_metadata (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		fingerprint TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		last_rotated_at TEXT,
		keyservers_published TEXT NOT NULL DEFAULT '[]',
		revoked INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0, 1))
	)`,
	`CREATE TABLE IF NOT EXISTS backups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		action TEXT NOT NULL,
		filename TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		encrypted INTEGER NOT NULL DEFAULT 0,
		success INTEGER NOT NULL DEFAULT 1,
		details TEXT
	)`,
}
