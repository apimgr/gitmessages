-- Messages table (loaded from messages.json)
CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  content TEXT NOT NULL UNIQUE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Message usage tracking
CREATE TABLE IF NOT EXISTS message_usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  used_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reset_cycle INTEGER NOT NULL DEFAULT 0,
  UNIQUE(message_id, reset_cycle)
);

-- Index for performance
CREATE INDEX IF NOT EXISTS idx_message_usage_cycle ON message_usage(reset_cycle);
CREATE INDEX IF NOT EXISTS idx_message_usage_message ON message_usage(message_id);

-- Track current cycle for reset logic
CREATE TABLE IF NOT EXISTS usage_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Initialize cycle counter
INSERT OR IGNORE INTO usage_metadata (key, value) VALUES ('current_cycle', '0');

-- Users table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  display_name TEXT,
  avatar_url TEXT,
  bio TEXT,
  role TEXT NOT NULL CHECK (role IN ('administrator', 'user', 'guest')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'pending')),
  timezone TEXT DEFAULT 'UTC',
  language TEXT DEFAULT 'en',
  theme TEXT DEFAULT 'dark',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login TIMESTAMP NULL,
  failed_login_attempts INTEGER DEFAULT 0,
  locked_until TIMESTAMP NULL,
  metadata TEXT
);

-- Sessions table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token TEXT UNIQUE NOT NULL,
  ip_address TEXT NOT NULL,
  user_agent TEXT,
  device_name TEXT,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  remember_me INTEGER DEFAULT 0,
  is_active INTEGER DEFAULT 1
);

-- Tokens table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT UNIQUE NOT NULL,
  last_used TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TIMESTAMP NULL
);

-- Settings table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('string', 'number', 'boolean', 'json')),
  category TEXT NOT NULL,
  description TEXT,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_by TEXT REFERENCES users(id)
);

-- Audit log table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS audit_log (
  id TEXT PRIMARY KEY,
  user_id TEXT REFERENCES users(id),
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  old_value TEXT,
  new_value TEXT,
  ip_address TEXT NOT NULL,
  user_agent TEXT,
  success INTEGER NOT NULL,
  error_message TEXT,
  timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Scheduled tasks table (exact structure required by spec)
CREATE TABLE IF NOT EXISTS scheduled_tasks (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  cron_expression TEXT NOT NULL,
  command TEXT NOT NULL,
  enabled INTEGER DEFAULT 1,
  last_run TIMESTAMP NULL,
  next_run TIMESTAMP NOT NULL,
  last_status TEXT,
  last_error TEXT,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
