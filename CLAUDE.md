# 🚀 Universal Server Template Specification v1.0

## 📖 Table of Contents
1. [Purpose & Scope](#purpose--scope)
2. [Core Requirements](#core-requirements)
3. [Database Architecture](#database-architecture)
4. [First User & Admin Flow](#first-user--admin-flow)
5. [Route Structure](#route-structure)
6. [Health Check Endpoint](#health-check-endpoint)
7. [API Architecture](#api-architecture)
8. [Web Interface](#web-interface)
9. [SSL/TLS & Let's Encrypt](#ssltls--lets-encrypt)
10. [Port Management](#port-management)
11. [Server Configuration](#server-configuration)
12. [Logging System](#logging-system)
13. [Built-in Scheduler](#built-in-scheduler)
14. [Admin Interface](#admin-interface)
15. [User Profile & Account Pages](#user-profile--account-pages)
16. [Backup & Restore](#backup--restore)
17. [Monitoring & Metrics](#monitoring--metrics)
18. [Development Mode](#development-mode)
19. [Live Reload Support](#live-reload-support)
20. [CLI Interface](#cli-interface)
21. [Build System](#build-system)
22. [Docker Configuration](#docker-configuration)
23. [Installation Scripts](#installation-scripts)
24. [Security Implementation](#security-implementation)
25. [Error Handling](#error-handling)
26. [Directory Structure](#directory-structure)
27. [Integration with Existing Projects](#integration-with-existing-projects)
28. [AI Development Tool Usage](#ai-development-tool-usage)
29. [Implementation Requirements](#implementation-requirements)
30. [Final Checklist](#final-checklist)

---

## 🎯 Purpose & Scope

### Exactly What This Template Is
A universal server template specification for:
- **New Projects**: Complete foundation to build any server application
- **Existing Projects**: Add server capabilities to any codebase without breaking existing functionality
- **AI Development**: Works with GitHub Copilot, ChatGPT, Claude, Cursor, and any AI assistant

Every requirement is mandatory for template features. Existing projects implement only what they need.

### Target Audience (In Priority Order)
1. **Self-Hosted Users**: Assume zero technical knowledge
2. **Small/Medium Business**: Assume zero technical knowledge  
3. **Enterprise**: May have technical staff

### Absolute Non-Negotiable Rules (All Mandatory)
1. Single static binary with all assets embedded - no external files
2. Database is the only configuration storage - zero config files
3. Validate every input before processing - no exceptions
4. Sanitize appropriately based on context - HTML, SQL, etc.
5. Save only validated data - reject invalid completely
6. Clear only invalid fields - keep valid data intact
7. Generated tokens/passwords shown exactly once - no retrieval possible
8. Test all external connections before enabling
9. Show tooltips on every input field and button
10. Security by default on everything
11. Mobile responsive required - not optional
12. Set sane defaults for all values
13. Security must not block legitimate usage
14. Zero AI or ML in core logic - deterministic only
15. Working directory is . (current directory)
16. All source code in ./src
17. All scripts in ./scripts

---

## 💾 Database Architecture

### Supported Databases (Priority Order)

```yaml
Required Support:
1. SQLite - Always present, always works, default database
2. MariaDB/MySQL - Port 3306, utf8mb4 charset required
3. PostgreSQL - Port 5432, UTF-8 encoding required  
4. MSSQL - Port 1433, TCP/IP enabled required

Optional Support:
- Valkey/Redis - Only for caching, never primary storage
```

### Database Connection Behavior

```yaml
On Startup:
1. Check for external database configuration
2. If configured:
   a. Test connection with 5 second timeout
   b. If successful: Use as primary, SQLite as cache
   c. If failed: Use SQLite only, show admin warning
3. If not configured:
   a. Use SQLite as primary and only database

During Operation:
1. External DB fails:
   a. Log exact error with timestamp
   b. Switch to read-only mode immediately
   c. Use SQLite cache for all reads
   d. Queue all writes with timestamps
   e. Show banner: "Database unavailable - Read only mode"
   f. Retry connection every 30 seconds
2. External DB recovers:
   a. Verify connection with test query
   b. Replay queued writes in order
   c. Resume normal operation
   d. Clear read-only banner
   e. Log recovery with timestamp
```

### Exact Database Schema (Required Tables)

```sql
-- Users table (exact structure required)
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username VARCHAR(255) UNIQUE NOT NULL,
  email VARCHAR(255) UNIQUE NOT NULL,  
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(255),
  avatar_url TEXT,
  bio TEXT,
  role VARCHAR(20) NOT NULL CHECK (role IN ('administrator', 'user', 'guest')),
  status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'pending')),
  timezone VARCHAR(50) DEFAULT 'UTC',
  language VARCHAR(10) DEFAULT 'en',
  theme VARCHAR(20) DEFAULT 'dark',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login TIMESTAMP NULL,
  failed_login_attempts INTEGER DEFAULT 0,
  locked_until TIMESTAMP NULL,
  metadata JSON
);

-- Sessions table (exact structure required)
CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token VARCHAR(255) UNIQUE NOT NULL,
  ip_address VARCHAR(45) NOT NULL,
  user_agent TEXT,
  device_name VARCHAR(100),
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  remember_me BOOLEAN DEFAULT false,
  is_active BOOLEAN DEFAULT true
);

-- Tokens table (exact structure required)
CREATE TABLE tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name VARCHAR(100) NOT NULL,
  token_hash VARCHAR(255) UNIQUE NOT NULL,
  last_used TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TIMESTAMP NULL
);

-- Settings table (exact structure required)
CREATE TABLE settings (
  key VARCHAR(255) PRIMARY KEY,
  value TEXT NOT NULL,
  type VARCHAR(20) NOT NULL CHECK (type IN ('string', 'number', 'boolean', 'json')),
  category VARCHAR(100) NOT NULL,
  description TEXT,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_by UUID REFERENCES users(id)
);

-- Audit log table (exact structure required)
CREATE TABLE audit_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id),
  action VARCHAR(100) NOT NULL,
  resource VARCHAR(255) NOT NULL,
  old_value TEXT,
  new_value TEXT,
  ip_address VARCHAR(45) NOT NULL,
  user_agent TEXT,
  success BOOLEAN NOT NULL,
  error_message TEXT,
  timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Scheduled tasks table (exact structure required)
CREATE TABLE scheduled_tasks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(100) UNIQUE NOT NULL,
  cron_expression VARCHAR(100) NOT NULL,
  command VARCHAR(500) NOT NULL,
  enabled BOOLEAN DEFAULT true,
  last_run TIMESTAMP NULL,
  next_run TIMESTAMP NOT NULL,
  last_status VARCHAR(20),
  last_error TEXT,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

---

## 👤 First User & Admin Flow

### Critical Flow Requirement (WEB-BASED ONLY)

**This entire flow happens in a web browser. No CLI. No API. Web forms only.**

```yaml
Exact Step-by-Step Web Flow:

1. User opens browser to http://server:port/
2. System checks: SELECT COUNT(*) FROM users
3. If count = 0: Redirect to /user/setup
4. /user/setup shows welcome screen with "Get Started" button
5. Click button → Redirect to /user/setup/register
6. User fills registration form at /user/setup/register
   - Username field (required, unique)
   - Email field (required, valid email)
   - Password field (required, min 8 chars)
7. User submits form
8. System creates first regular user account
9. System immediately redirects to /user/setup/admin
10. Web page shows: "Create Administrator Account"
    - Username field (pre-filled: "administrator", not editable)
    - Password field (required, min 12 chars)
    - Confirm password field (must match)
11. User submits admin creation form
12. System creates administrator account
13. System logs out regular user
14. System logs in as administrator
15. Browser redirects to /user/setup/complete
16. /user/setup/complete performs contextual redirect:
    - Currently admin → /admin or /admin/dashboard
    - Currently user → /user or /user/dashboard  
    - Not logged in → /auth/login
17. Admin can configure server at /admin/settings

Post-Setup Access:
  - /user/setup/* routes → 404 (setup complete)
  - Any user visiting / → Normal homepage
  - Admin visiting / → Sees as guest/anonymous
  - Users and admins have separate dashboards

Why This Flow:
  - User account MUST be created first (it's the owner)
  - Administrator is a separate system account
  - User owns the system, admin manages it
  - Clear separation of concerns

Administrator Limitations:
  - Can access: URLs starting with /admin/ only
  - Cannot access: URLs starting with /user/
  - Cannot access: Any application features
  - When viewing /: Treated as anonymous user
  - When viewing any non-admin page: Read-only guest access
```

---

## 🗺️ Route Structure

### Route Scoping Principles

**IMPORTANT**: Routes are scoped by context. Web routes return HTML, API routes return JSON/text.
- Everything user-related under `/user/*` (including initial setup)
- Everything admin-related under `/admin/*`
- Everything org-related under `/org/*` (if organizations enabled)
- Everything auth-related under `/auth/*`
- API mirrors web structure under `/api/v1/*`

```yaml
Route Standards:
  Web Routes (HTML responses):
    - Base: / 
    - Return: HTML pages
    - Authentication: Session-based
    - All routes follow scope pattern
  
  API Routes (JSON/text responses):
    - Base: /api/v1
    - Default: application/json
    - Text: Add .txt extension for plain text
    - Authentication: Token-based
    - Mirrors web route structure
  
  Documentation Routes:
    - Swagger UI: /api/docs
    - GraphQL Playground: /api/graphql
    - Both interactive and self-documenting

Scoping Rules:
  - Use path params over query params
  - Query params only where explicitly defined
  - Routes must be intuitive and predictable
  - Duplicate routes across scopes is acceptable
  - Each scope is self-contained
```

### Public Routes (No Authentication)

```yaml
Web Routes (HTML):
  GET  /                      - Homepage/landing page
  GET  /healthz               - Health check (JSON response)
  GET  /robots.txt            - Robots file (from database)
  GET  /.well-known/security.txt - Security file (from database, RFC 9116)
  GET  /security.txt          - Security file (fallback, redirects to /.well-known/security.txt)
  GET  /manifest.json         - PWA manifest
  GET  /static/*              - Static assets (embedded)

API Routes (JSON):
  GET  /api/v1/health         - Health check
  GET  /api/v1/health.txt     - Health check (plain text)
```

### Authentication Routes (No Authentication Required)

```yaml
Web Routes (HTML):
  GET  /auth/login            - Login form
  POST /auth/login            - Process login
  GET  /auth/logout           - Logout and redirect
  GET  /auth/register         - Registration form (if enabled)
  POST /auth/register         - Process registration
  GET  /auth/verify           - Email verification
  GET  /auth/verify/:token    - Verify email with token
  GET  /auth/password/reset   - Password reset request form
  POST /auth/password/reset   - Send reset email
  GET  /auth/password/new     - New password form (with token)
  POST /auth/password/new     - Set new password
  GET  /auth/2fa              - Two-factor authentication
  POST /auth/2fa              - Verify 2FA code

API Routes (JSON):
  POST /api/v1/auth/login     - Login {"username":"", "password":""}
  POST /api/v1/auth/logout    - Logout current session
  POST /api/v1/auth/register  - Register {"username":"", "email":"", "password":""}
  POST /api/v1/auth/refresh   - Refresh token {"token":""}
  POST /api/v1/auth/verify    - Send verification email {"email":""}
  GET  /api/v1/auth/verify/:token - Verify email token
  POST /api/v1/auth/password/reset - Request reset {"email":""}
  POST /api/v1/auth/password/new - Set new password {"token":"", "password":""}
  POST /api/v1/auth/2fa/setup - Setup 2FA (returns QR code)
  POST /api/v1/auth/2fa/verify - Verify 2FA {"code":""}
  DELETE /api/v1/auth/2fa     - Disable 2FA {"password":""}
  GET  /api/v1/auth/status    - Check auth status
  GET  /api/v1/auth/status.txt - Auth status as text
```

### Setup Routes (First Run Only)

```yaml
Web Routes (HTML):
  GET  /user/setup            - Check if setup needed, show welcome
  GET  /user/setup/register   - Create first user account
  POST /user/setup/register   - Process first user creation
  GET  /user/setup/admin      - Create administrator account  
  POST /user/setup/admin      - Process administrator creation
  GET  /user/setup/complete   - Setup complete, redirect based on auth state

Flow:
  1. User visits any page → System checks if users exist
  2. If no users → Redirect to /user/setup
  3. /user/setup → Shows welcome, continues to /user/setup/register
  4. User creates account → Redirect to /user/setup/admin
  5. Admin account created → Switch to admin, redirect to /user/setup/complete
  6. /user/setup/complete redirects based on current auth:
     - If logged in as admin → /admin (or /admin/dashboard)
     - If logged in as user → /user (or /user/dashboard)
     - If not logged in → /auth/login
  7. Admin configures server settings in /admin/settings

After Setup Behavior:
  - Setup routes return 404 (no longer accessible)
  - Direct visits to /user/setup → redirect to appropriate dashboard
  - System is now in normal operation mode

Why under /user:
  - First action is creating a USER account
  - Admin is secondary (created after user)
  - Follows the logical flow: user first, then admin
  - Makes it clear this is about account creation

API Routes (JSON):
  GET  /api/v1/user/setup/status    - Check if setup is needed
  POST /api/v1/user/setup/register  - Create first user
  POST /api/v1/user/setup/admin     - Create administrator
```

### User Routes (Authentication Required)

```yaml
Web Routes (HTML):
  GET  /user                  - User dashboard
  GET  /user/profile          - Profile management
  POST /user/profile          - Update profile
  GET  /user/settings         - User settings
  POST /user/settings         - Update settings
  GET  /user/tokens           - Token management
  POST /user/tokens           - Create token
  DELETE /user/tokens/:id     - Revoke token
  GET  /user/sessions         - Active sessions
  DELETE /user/sessions/:id   - Terminate session
  GET  /user/security         - Security settings
  POST /user/security         - Update security
  GET  /user/notifications    - Notification preferences
  POST /user/notifications    - Update notifications
  GET  /user/data             - Data management
  POST /user/data/export      - Request data export
  POST /user/data/delete      - Request account deletion

API Routes (JSON):
  GET  /api/v1/user                  - Get user info
  PUT  /api/v1/user                  - Update user info
  GET  /api/v1/user/profile          - Get profile
  PUT  /api/v1/user/profile          - Update profile
  GET  /api/v1/user/settings         - Get settings
  PUT  /api/v1/user/settings         - Update settings
  GET  /api/v1/user/tokens           - List tokens
  POST /api/v1/user/tokens           - Create token
  DELETE /api/v1/user/tokens/:id     - Delete token
  GET  /api/v1/user/sessions         - List sessions
  DELETE /api/v1/user/sessions/:id   - Kill session
  GET  /api/v1/user/security         - Get security settings
  PUT  /api/v1/user/security         - Update security
  POST /api/v1/user/avatar           - Upload avatar
  DELETE /api/v1/user/avatar         - Delete avatar
  GET  /api/v1/user/export           - Export user data
  DELETE /api/v1/user                - Delete account

Text Endpoints (Plain text):
  GET  /api/v1/user.txt              - User info as text
  GET  /api/v1/user/tokens.txt       - Token list as text
  GET  /api/v1/user/sessions.txt     - Session list as text
```

### Admin Routes (Administrator Role Required)

```yaml
Web Routes (HTML):
  GET  /admin                      - Admin dashboard
  GET  /admin/users                - User management
  GET  /admin/users/:id            - View specific user
  PUT  /admin/users/:id            - Update user
  DELETE /admin/users/:id          - Delete user
  GET  /admin/settings             - Server settings (main configuration)
  POST /admin/settings             - Update settings
  GET  /admin/database             - Database management
  POST /admin/database/test        - Test connection
  GET  /admin/ssl                  - SSL certificate management
  POST /admin/ssl/renew            - Renew certificates
  GET  /admin/logs                 - Log viewer
  GET  /admin/logs/:type           - View specific log
  GET  /admin/scheduler            - Scheduled tasks
  POST /admin/scheduler/:id        - Run task manually
  GET  /admin/audit                - Audit log
  GET  /admin/backup               - Backup management
  POST /admin/backup/create        - Create backup
  POST /admin/backup/restore       - Restore from backup
  GET  /admin/monitoring           - Monitoring configuration
  POST /admin/monitoring           - Update monitoring

API Routes (JSON):
  GET  /api/v1/admin/stats            - Server statistics
  GET  /api/v1/admin/users            - List all users
  GET  /api/v1/admin/users/:id        - Get user details
  PUT  /api/v1/admin/users/:id        - Update user
  DELETE /api/v1/admin/users/:id      - Delete user
  GET  /api/v1/admin/settings         - Get all settings
  PUT  /api/v1/admin/settings         - Update settings
  GET  /api/v1/admin/database         - Database status
  POST /api/v1/admin/database/test    - Test connection
  GET  /api/v1/admin/ssl              - Certificate status
  POST /api/v1/admin/ssl/renew        - Renew certificates
  GET  /api/v1/admin/logs             - List logs
  GET  /api/v1/admin/logs/:type       - Get specific log
  GET  /api/v1/admin/scheduler        - List scheduled tasks
  POST /api/v1/admin/scheduler/:id    - Run task
  GET  /api/v1/admin/audit            - Get audit log
  GET  /api/v1/admin/backup           - List backups
  POST /api/v1/admin/backup           - Create backup
  POST /api/v1/admin/backup/restore   - Restore backup
  DELETE /api/v1/admin/backup/:id     - Delete backup

Text Endpoints (Plain text):
  GET  /api/v1/admin/stats.txt        - Stats as text
  GET  /api/v1/admin/users.txt        - User list as text
  GET  /api/v1/admin/logs/:type.txt   - Log content as text
```

### Organization Routes (If Organizations Enabled)

```yaml
Web Routes (HTML):
  GET  /org                   - Organization dashboard
  GET  /org/profile           - Organization profile
  POST /org/profile           - Update profile
  GET  /org/members           - Member management
  POST /org/members           - Invite member
  DELETE /org/members/:id     - Remove member
  GET  /org/settings          - Organization settings
  POST /org/settings          - Update settings
  GET  /org/tokens            - Organization tokens
  POST /org/tokens            - Create token
  DELETE /org/tokens/:id      - Revoke token
  GET  /org/billing           - Billing information
  GET  /org/audit             - Organization audit log

API Routes (JSON):
  GET  /api/v1/org                   - Get org info
  PUT  /api/v1/org                   - Update org info
  GET  /api/v1/org/members           - List members
  POST /api/v1/org/members           - Invite member
  DELETE /api/v1/org/members/:id     - Remove member
  GET  /api/v1/org/settings          - Get settings
  PUT  /api/v1/org/settings          - Update settings
  GET  /api/v1/org/tokens            - List tokens
  POST /api/v1/org/tokens            - Create token
  DELETE /api/v1/org/tokens/:id      - Delete token
  GET  /api/v1/org/audit             - Get audit log
```

### Special Endpoints

```yaml
Documentation & Tools:
  GET  /api/docs              - Swagger UI (interactive API docs)
  GET  /api/graphql           - GraphQL playground
  POST /api/graphql           - GraphQL endpoint
  GET  /metrics               - Prometheus metrics (if enabled)

Development Mode Only:
  GET  /debug/routes          - List all routes
  GET  /debug/config          - Show configuration
  GET  /debug/db              - Database statistics
  GET  /debug/sessions        - Active sessions
  POST /debug/reset           - Reset to fresh state
  POST /debug/mock/users      - Generate test users
```

### Integration with Existing Projects

```yaml
Option 1 - Namespace Everything:
  Mount all template routes under /system:
  - /system/admin/*           - Admin interface
  - /system/user/*            - User interface
  - /system/api/v1/*          - API endpoints
  - /system/setup/*           - Setup wizard
  
Option 2 - Selective Scopes:
  Only add needed scopes:
  - /admin/* (if no existing admin)
  - /api/v1/admin/* (admin API only)
  - /healthz (always safe to add)
  
Option 3 - API Only:
  Skip web routes entirely:
  - /api/v1/* (all API endpoints)
  - /api/docs (documentation)
  - /api/graphql (if needed)

Response Format Standards:
  JSON Success:
    {
      "success": true,
      "data": {},
      "timestamp": "2024-01-01T00:00:00Z"
    }
  
  JSON Error:
    {
      "success": false,
      "error": {
        "code": "ERROR_CODE",
        "message": "Human readable message",
        "field": "field_name" (if applicable)
      },
      "timestamp": "2024-01-01T00:00:00Z"
    }
  
  Text Format (.txt endpoints):
    Simple key-value pairs or human-readable text
    No JSON structure, plain text only
```

---

## 🏥 Health Check Endpoint

### /healthz Endpoint Specification

```yaml
URL: /healthz
Method: GET
Authentication: None (public endpoint)
Content-Type: application/json

Response Structure:
{
  "status": "healthy|degraded|unhealthy",
  "timestamp": "2024-01-01T12:00:00Z",
  "version": "{version}",
  "uptime_seconds": 86400,
  "checks": {
    "database": {
      "status": "connected|disconnected|readonly",
      "type": "sqlite|mysql|postgres|mssql",
      "latency_ms": 5,
      "connection_pool": {
        "active": 2,
        "idle": 8,
        "max": 10
      }
    },
    "cache": {
      "status": "active|inactive",
      "type": "sqlite|redis|none",
      "hit_rate": 0.95,
      "size_bytes": 1048576,
      "entries": 1234
    },
    "disk": {
      "status": "ok|warning|critical",
      "data_dir": {
        "path": "/var/lib/{projectname}",
        "used_bytes": 1073741824,
        "free_bytes": 10737418240,
        "total_bytes": 11811160064,
        "used_percent": 10
      },
      "log_dir": {
        "path": "/var/log/{projectname}",
        "used_bytes": 104857600,
        "free_bytes": 10737418240,
        "total_bytes": 10842275840,
        "used_percent": 1
      }
    },
    "memory": {
      "status": "ok|warning|critical",
      "used_bytes": 104857600,
      "total_bytes": 1073741824,
      "used_percent": 10,
      "heap_bytes": 52428800,
      "gc_runs": 42
    },
    "ssl": {
      "enabled": true,
      "status": "valid|expiring|expired|none",
      "expires_at": "2024-03-01T00:00:00Z",
      "days_remaining": 59,
      "issuer": "Let's Encrypt|Self-Signed|Unknown"
    },
    "scheduler": {
      "status": "running|stopped",
      "tasks_total": 5,
      "tasks_enabled": 5,
      "next_run": "2024-01-01T02:00:00Z"
    },
    "sessions": {
      "active": 123,
      "total_today": 456
    },
    "requests": {
      "total_today": 12345,
      "rate_per_minute": 82,
      "errors_today": 12,
      "error_rate": 0.001
    }
  },
  "server": {
    "address": "192.168.1.100",
    "http_port": 8080,
    "https_port": 8443,
    "https_enabled": true,
    "pid": 1234,
    "started_at": "2024-01-01T00:00:00Z"
  },
  "features": {
    "registration_enabled": true,
    "api_enabled": true,
    "maintenance_mode": false,
    "graphql_enabled": true,
    "websocket_enabled": false,
    "live_reload_enabled": true
  }
}

Status Determination:
  healthy: All checks pass
  degraded: Non-critical checks fail (cache, high disk usage, etc.)
  unhealthy: Critical checks fail (database down, disk full, etc.)

HTTP Status Codes:
  200: healthy or degraded
  503: unhealthy

Thresholds:
  Disk Warning: > 80% used
  Disk Critical: > 95% used
  Memory Warning: > 80% used
  Memory Critical: > 95% used
  SSL Expiring: < 30 days
  SSL Critical: < 7 days
  Error Rate Warning: > 0.01 (1%)
  Error Rate Critical: > 0.05 (5%)

Security Rules (NEVER include):
  - Usernames
  - Passwords
  - API tokens
  - Session tokens
  - Email addresses (except server's own)
  - IP addresses (except server's own)
  - Sensitive file paths
  - Database credentials
  - Internal hostnames
  - Debug stack traces
```

---

## 🔌 API Architecture

### RESTful API v1

```yaml
Standards:
  Base Path: /api/v1
  Default Format: application/json
  Text Format: Add .txt extension for plain text
  Documentation: Interactive and self-documenting
  
Core Principles:
  - Use path params over query params
  - Query params only where explicitly defined  
  - Routes mirror web structure for consistency
  - All routes scoped by context (/user, /admin, /org)
  - Public routes at / and /api/v1
  
Documentation Endpoints:
  Swagger UI: /api/docs
    - Interactive API documentation
    - Try-it-out functionality
    - Auto-generated from code
  
  GraphQL Playground: /api/graphql
    - Interactive GraphQL IDE
    - Schema introspection
    - Query builder interface
  
Naming Conventions:
  - Use "tokens" NOT "api_keys" or "api_tokens"
  - Use SERVER_ADDRESS NOT SERVER_NAME
  - Keep routes intuitive and simple
  - Follow REST standards
  - Consistent pluralization

Response Formats:
  JSON (default):
    Success:
      {
        "success": true,
        "data": {},
        "timestamp": "Jan 1, 2024 12:00:00 PM EST",
        "timestamp_utc": "2024-01-01T17:00:00Z"  # Optional for APIs
      }
    
    Error:
      {
        "success": false,
        "error": {
          "code": "RATE_LIMIT_EXCEEDED",
          "message": "Too many requests. Please try again after 12:15 PM.",
          "retry_after": "Jan 1, 2024 12:15:00 PM EST",
          "retry_in": "15 minutes",
          "field": "username" (if validation error)
        },
        "timestamp": "Jan 1, 2024 12:00:00 PM EST"
      }
  
  Text (.txt extension):
    - Plain text response
    - No JSON structure
    - Human-readable format
    - Line-based key-value pairs

Rate Limiting:
  - Per IP for public endpoints
  - Per token for authenticated endpoints
  - Headers indicate limit status:
    X-RateLimit-Limit: 100
    X-RateLimit-Remaining: 95
    X-RateLimit-Reset: "Jan 1, 2024 12:15:00 PM EST"
    X-RateLimit-Reset-In: "15 minutes"
  
  Alternative formats (configurable):
    X-RateLimit-Reset: "2024-01-01 12:15:00 EST"  # Short format
    X-RateLimit-Reset: "in 15 minutes"            # Relative only
    X-RateLimit-Reset: "12:15 PM"                 # Time only (if today)

Versioning:
  - URL versioning: /api/v1, /api/v2
  - Version in response headers
  - Deprecation warnings in headers
  - Backwards compatibility for 1 year
```

---

## 🌐 Web Interface

### Exact HTML5 Requirements

```yaml
DOCTYPE: <!DOCTYPE html>
HTML Tag: <html lang="en">
Head Required Elements:
  - <meta charset="UTF-8">
  - <meta name="viewport" content="width=device-width, initial-scale=1.0">
  - <meta name="description" content="{server.description || server.tagline || default}">
  - <title>{page title} - {server.title}</title>
  - <link rel="stylesheet" href="/static/css/main.css">
  - <link rel="manifest" href="/manifest.json">
  - <link rel="icon" type="image/png" href="/static/favicon.png">

Body Structure:
  <body data-theme="dark">
    <header id="main-header">
      <div class="header-container">
        <div class="header-left">
          <button class="mobile-menu-toggle">☰</button>
          <a class="logo" href="/">{server.title || projectname}</a>
        </div>
        <nav id="main-nav" class="header-center">
          <!-- Main navigation -->
        </nav>
        <div class="header-right">
          <div class="notification-dropdown">
            <button class="notification-bell" data-count="0">
              🔔<span class="notification-badge">0</span>
            </button>
          </div>
          <div class="profile-dropdown">
            <button class="profile-toggle">
              <img class="profile-avatar" src="/user/avatar">
              <span class="profile-name">{username}</span>
              <span class="caret">▼</span>
            </button>
            <!-- Profile menu dropdown -->
          </div>
        </div>
      </div>
    </header>
    <main id="main-content">
      {content}
    </main>
    <footer id="main-footer">
      {footer content - always at bottom}
    </footer>
    <div id="modal-container"></div>
    <div id="toast-container"></div>
    <script src="/static/js/main.js"></script>
  </body>

Navigation Requirements:
  Profile Menu (Required):
    - Top right corner next to notification bell
    - Shows avatar (uploaded, gravatar, or initials)
    - Dropdown with user options when logged in
    - Login/Register options when logged out
    - Admin panel link for administrators
  
  Session Persistence:
    - 30-day default session duration
    - "Remember me" checkbox on login
    - Secure HttpOnly cookies
    - Multi-device support
    - Automatic token refresh
    - No forced re-login on refresh
  
  Avatar System:
    Priority Order:
      1. User uploaded image
      2. Gravatar (if email)
      3. Generated initials (colored background)
      4. Default icon
  
  Mobile Responsive:
    - Hamburger menu for main nav < 720px
    - Profile menu stays visible
    - Full-screen modals on mobile
```

### UI Component Requirements (MANDATORY - No Simple Popups)

```yaml
Modals (NOT alert/confirm/prompt):
  Structure:
    <div class="modal" id="modal-{name}">
      <div class="modal-backdrop"></div>
      <div class="modal-content">
        <div class="modal-header">
          <h2 class="modal-title">{title}</h2>
          <button class="modal-close" aria-label="Close">×</button>
        </div>
        <div class="modal-body">{content}</div>
        <div class="modal-footer">
          <button class="btn btn-secondary">Cancel</button>
          <button class="btn btn-primary">Confirm</button>
        </div>
      </div>
    </div>
  
  Behavior:
    - Fade in/out animation (300ms)
    - Backdrop click to close (optional)
    - ESC key to close
    - Focus trap inside modal
    - Return focus on close
    - Stack multiple modals with z-index

Toggles/Switches (NOT checkboxes for settings):
  Structure:
    <label class="toggle">
      <input type="checkbox" />
      <span class="toggle-slider"></span>
      <span class="toggle-label">{label}</span>
    </label>
  
  Style:
    - iOS-style sliding toggle
    - Smooth transition (200ms)
    - Clear on/off state
    - Color change on state
    - Disabled state support

Alerts/Notifications (NOT window.alert):
  Structure:
    <div class="alert alert-{type}">
      <div class="alert-icon">{icon}</div>
      <div class="alert-content">
        <div class="alert-title">{title}</div>
        <div class="alert-message">{message}</div>
      </div>
      <button class="alert-close">×</button>
    </div>
  
  Types:
    - info (blue) ℹ️
    - success (green) ✅
    - warning (yellow) ⚠️
    - error (red) ❌
  
  Behavior:
    - Slide in from top-right
    - Auto-dismiss after 5 seconds (configurable)
    - Manual dismiss via X
    - Stack vertically
    - Persist important ones

Banners (Site-wide notifications):
  Structure:
    <div class="banner banner-{type}">
      <div class="banner-content">
        <span class="banner-icon">{icon}</span>
        <span class="banner-message">{message}</span>
        <a class="banner-action" href="#">{action}</a>
      </div>
      <button class="banner-dismiss">×</button>
    </div>
  
  Types:
    - maintenance (yellow) - Scheduled maintenance
    - error (red) - System issues
    - info (blue) - Announcements
    - success (green) - Positive updates
  
  Placement:
    - Below header, above content
    - Full width
    - Pushes content down
    - Smooth slide animation

Loading States (NOT browser default):
  Button Loading:
    <button class="btn btn-primary loading">
      <span class="spinner"></span>
      <span>Loading...</span>
    </button>
  
  Page Loading:
    <div class="page-loader">
      <div class="loader-spinner"></div>
      <div class="loader-text">Loading...</div>
    </div>
  
  Skeleton Screens:
    <div class="skeleton">
      <div class="skeleton-header"></div>
      <div class="skeleton-text"></div>
      <div class="skeleton-text"></div>
    </div>

Forms (Enhanced, not basic HTML):
  Input Fields:
    <div class="form-group">
      <label class="form-label" for="input-{name}">
        {label}
        <span class="required">*</span>
      </label>
      <input 
        class="form-input" 
        id="input-{name}"
        type="text"
        placeholder="{placeholder}"
      />
      <span class="form-hint">{hint text}</span>
      <span class="form-error">{error message}</span>
    </div>
  
  Features:
    - Floating labels on focus
    - Real-time validation
    - Error states with messages
    - Success states with checkmarks
    - Character counters
    - Input masks for formatting
    - Tooltip hints on hover

Tooltips (NOT title attribute):
  Structure:
    <span data-tooltip="{content}" data-position="top">
      {element}
      <span class="tooltip">{content}</span>
    </span>
  
  Features:
    - Appear on hover/focus
    - Multiple positions (top/bottom/left/right)
    - Rich content support (HTML)
    - Delay before showing (500ms)
    - Smart positioning (viewport aware)
    - Mobile touch support

Tables (Enhanced, not basic HTML):
  Features:
    - Sortable columns
    - Hover effects
    - Striped rows
    - Responsive (horizontal scroll)
    - Sticky header
    - Row selection
    - Inline actions
    - Pagination controls
    - Search/filter inputs

Cards (Content containers):
  Features:
    - Consistent padding
    - Border and shadow
    - Hover effects
    - Collapsible sections
    - Loading states

Tabs (NOT page reload navigation):
  Features:
    - Smooth transitions
    - Keyboard navigation
    - ARIA compliant
    - Remember last active
    - Lazy loading content
    - Mobile responsive (dropdown on small screens)

Progress Indicators:
  - Progress bars with labels
  - Circular progress indicators
  - Step indicators for wizards
```

### CSS Requirements (Professional Design System)

```css
/* CSS Variables for Consistency */
:root {
  /* Colors - Dark Theme (Default) */
  --bg-primary: #1a1a1a;
  --bg-secondary: #2d2d2d;
  --bg-tertiary: #404040;
  --text-primary: #ffffff;
  --text-secondary: #b0b0b0;
  --text-tertiary: #808080;
  --accent-primary: #0066cc;
  --accent-success: #00aa00;
  --accent-warning: #ff9900;
  --accent-danger: #cc0000;
  --border-color: #404040;
  
  /* Spacing */
  --space-xs: 4px;
  --space-sm: 8px;
  --space-md: 16px;
  --space-lg: 24px;
  --space-xl: 32px;
  
  /* Typography */
  --font-primary: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  --font-mono: "SF Mono", Monaco, Consolas, monospace;
  
  /* Transitions */
  --transition-fast: 150ms ease;
  --transition-normal: 300ms ease;
  --transition-slow: 500ms ease;
  
  /* Shadows */
  --shadow-sm: 0 1px 3px rgba(0,0,0,0.12);
  --shadow-md: 0 4px 6px rgba(0,0,0,0.16);
  --shadow-lg: 0 10px 20px rgba(0,0,0,0.2);
  
  /* Z-index Scale */
  --z-dropdown: 1000;
  --z-modal-backdrop: 2000;
  --z-modal: 2001;
  --z-notification: 3000;
  --z-tooltip: 4000;
}

/* Light Theme Override */
[data-theme="light"] {
  --bg-primary: #ffffff;
  --bg-secondary: #f5f5f5;
  --text-primary: #1a1a1a;
  --border-color: #e0e0e0;
}

/* Responsive Breakpoints (Mobile First) */
@media (max-width: 719px) {
  .container { 
    width: 98%; 
    margin: 0 1%;
  }
}

@media (min-width: 720px) {
  .container { 
    width: 90%; 
    margin: 0 5%;
  }
}
```

### JavaScript Requirements (No Basic Popups)

```javascript
// FORBIDDEN - Never use these:
// ❌ alert()
// ❌ confirm()  
// ❌ prompt()
// ❌ document.write()

// REQUIRED - Professional UI functions:
class UI {
  // Modal Management
  static showModal(options) {
    // Creates and shows custom modal
    // Returns Promise for user action
  }
  
  // Toast Notifications
  static showToast(message, type, duration) {
    // Shows toast notification
    // Auto-dismisses after duration
  }
  
  // Confirmation Dialogs
  static confirm(options) {
    // Shows custom confirmation modal
    // Returns Promise<boolean>
  }
  
  // Timezone Conversion
  static convertTimestamps() {
    // Find all <time> elements
    // Convert to user's local timezone
    // Update display text
    // Keep original in data attribute
  }
  
  // Relative Time Updates
  static updateRelativeTimes() {
    // Find all [data-format="relative"]
    // Update "2 minutes ago" → "3 minutes ago"
    // Run every minute
  }
}

// On page load
document.addEventListener('DOMContentLoaded', () => {
  UI.convertTimestamps();  // Convert all times to user timezone
  setInterval(UI.updateRelativeTimes, 60000);  // Update relative times
});

// Example Usage:
// Server sends: <time data-unix="1704124800">Jan 1, 2024 12:00 PM EST</time>
// JS converts to: "Jan 1, 2024 9:00 AM PST" (if user in California)

// Example Usage:
// Instead of: alert('Success!')
UI.showToast('Operation successful', 'success', 3000);

// Instead of: if(confirm('Delete?'))
const confirmed = await UI.confirm({
  title: 'Delete User',
  message: 'This action cannot be undone.',
  confirmText: 'Delete',
  confirmClass: 'btn-danger'
});
```

### PWA Manifest

```json
{
  "name": "{projectname}",
  "short_name": "{projectname}",
  "description": "{project description}",
  "start_url": "/",
  "display": "standalone",
  "orientation": "any",
  "theme_color": "#1a1a1a",
  "background_color": "#1a1a1a",
  "icons": [
    {
      "src": "/static/icon-192.png",
      "sizes": "192x192",
      "type": "image/png"
    },
    {
      "src": "/static/icon-512.png",
      "sizes": "512x512",
      "type": "image/png"
    }
  ]
}
```

### Security Headers (All Responses)

```yaml
Required Headers:
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  X-XSS-Protection: 1; mode=block
  Referrer-Policy: strict-origin-when-cross-origin
  Permissions-Policy: geolocation=(), microphone=(), camera=()
  Content-Security-Policy: 
    default-src 'self';
    script-src 'self' 'unsafe-inline';
    style-src 'self' 'unsafe-inline';
    img-src 'self' data: https:;
    font-src 'self' data:;
    connect-src 'self';
    frame-ancestors 'none';

CORS Header:
  Access-Control-Allow-Origin: * (configurable via admin)
  Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
  Access-Control-Allow-Headers: Content-Type, Authorization
```

---

## 🔒 SSL/TLS & Let's Encrypt

### Exact Certificate Handling

```yaml
When HTTPS is Enabled:
  
Step 1 - Check Existing Certificates:
  Check path: /etc/letsencrypt/live/
  Note: /etc/letsencrypt/live/domain is a literal directory, not a variable
  If certificates found:
    - Use for HTTPS
    - Do NOT manage (no renewal, no modification)
    - Log: "Using existing Let's Encrypt certificates"
  
Step 2 - Port-Based Certificate Management:
  If ports are exactly 80,443:
    - Use HTTP-01 challenge
    - Request certificate from Let's Encrypt
    - Save to: /etc/{projectname}/ssl/certs/cert.pem
    - Save key to: /etc/{projectname}/ssl/private/key.pem
    - Save chain to: /etc/{projectname}/ssl/certs/chain.pem
    - Set permissions: cert=644, key=600
    - Schedule renewal check daily at 2:00 AM
  
Step 3 - Non-Standard Ports:
    If DNS-01 configured:
      - Use DNS challenge
      - Support all providers + RFC2136
    Else:
      - Generate self-signed certificate
      - Valid for 365 days
      - Log warning: "Using self-signed certificate"

Supported Challenges:
  HTTP-01:
    - Requires port 80
    - Creates /.well-known/acme-challenge/{token}
    - Automatic response handling
  
  TLS-ALPN-01:
    - Requires port 443
    - Direct TLS negotiation
    - No file system access needed
  
  DNS-01:
    - No port requirements
    - Supports all DNS providers
    - Supports RFC2136 dynamic updates
    - Enables wildcard certificates

Renewal Process:
  Daily at 2:00 AM:
    - Check certificate expiry
    - If < 30 days:
      - Attempt renewal
      - If success: Reload certificate
      - If fail: Alert admin, continue using current
```

---

## 🔌 Port Management

### Exact Port Configuration Rules

```yaml
Default Behavior (No Port Specified):
  1. Generate random number between 64000-64999
  2. Check if port is in use (bind test)
  3. If in use: Try next number
  4. If available: 
     - Save to database settings table
     - Use for all future starts
  
Single Port Configuration (e.g., --port 8080):
  - Use for HTTP only
  - No HTTPS available
  - Save to database
  
Dual Port Configuration (e.g., --port "8080,8443"):
  - First number: HTTP port
  - Second number: HTTPS port
  - Both saved to database
  - HTTPS requires certificates
  
Standard Ports (--port "80,443"):
  - Requires root/administrator privileges
  - Automatically triggers Let's Encrypt
  - Uses HTTP-01 challenge
  - Production mode activated

Port Display Rules:
  - Never show 0.0.0.0 to user
  - Never show 127.0.0.1 to user  
  - Never show localhost to user
  - Show actual server IP or FQDN
  - Detect via: hostname -I | awk '{print $1}'
```

---

## ⚙️ Server Configuration

### No Configuration Files - Database Only

```yaml
Initialization Variables (First Run Only):
  Environment variables checked ONCE on first startup:
    - DB_TYPE: sqlite|mysql|postgres|mssql (default: sqlite)
    - DB_HOST: database hostname (default: localhost)
    - DB_PORT: database port (default: per DB type)
    - DB_NAME: database name (default: {projectname})
    - DB_USER: database username
    - DB_PASSWORD: database password
    - PORT: server port(s)
    - ADDRESS: listen address (default: 0.0.0.0)
  
  After first run:
    - All settings stored in database
    - Environment variables ignored
    - Web UI modifies database directly
    - No .env files
    - No config.json
    - No settings.yaml
    - Database is the ONLY source of truth

Settings Table Usage:
  Server settings stored as:
    - key: 'server.title', value: 'My Awesome App', type: 'string'
    - key: 'server.tagline', value: 'Your personal cloud', type: 'string'
    - key: 'server.description', value: 'A comprehensive platform for...', type: 'string'
    - key: 'server.address', value: '0.0.0.0', type: 'string'
    - key: 'server.http_port', value: '8080', type: 'number'
    - key: 'server.https_port', value: '8443', type: 'number'
    - key: 'server.https_enabled', value: 'true', type: 'boolean'
    - key: 'server.timezone', value: 'America/New_York', type: 'string'
    - key: 'server.date_format', value: 'US', type: 'string'
    - key: 'server.time_format', value: '12-hour', type: 'string'
    - key: 'db.type', value: 'postgres', type: 'string'
    - key: 'db.host', value: 'localhost', type: 'string'
    [etc...]

Binary vs Display Names:
  Binary Name: {projectname}
    - Used for: Executable, CLI, file paths, logs
    - Never changes after build
    - Example: "myserver"
  
  Server Title: Configurable via settings
    - Used for: Web UI header, page titles, emails
    - Can be changed anytime via admin
    - Example: "My Awesome App"
    - Falls back to {projectname} if not set
  
  Server Tagline: Short slogan
    - Used for: Under title, email signatures
    - Example: "Your personal cloud"
    - Optional, can be empty
  
  Server Description: Full description
    - Used for: About page, meta description, API docs
    - Example: "A comprehensive platform for managing your personal data, files, and applications with enterprise-grade security and privacy."
    - Can be multiple paragraphs
  
  Usage Examples:
    Homepage:
      <h1>My Awesome App</h1>
      <p class="tagline">Your personal cloud</p>
    
    About Page:
      <h1>About My Awesome App</h1>
      <p>{server.description}</p>
    
    Meta Tags:
      <meta name="description" content="{server.description truncated to 160 chars}">
      <meta property="og:title" content="{server.title}">
      <meta property="og:description" content="{server.description}">
    
    API Response:
      {
        "name": "My Awesome App",
        "tagline": "Your personal cloud",
        "description": "A comprehensive platform...",
        "version": "1.0.0"
      }

Date/Time Format Examples:
  Priority Order:
    1. User's timezone (if set in profile)
    2. Browser's timezone (if detected)
    3. Server's timezone (fallback)
  
  US Format (12-hour):
    - Full: "Jan 1, 2024 3:30:00 PM EST"
    - Date: "Jan 1, 2024"
    - Time: "3:30 PM"
  
  EU Format (24-hour):
    - Full: "1 Jan 2024 15:30:00 CET"
    - Date: "1 Jan 2024"
    - Time: "15:30"
  
  ISO Format (developer-friendly):
    - Full: "2024-01-01 15:30:00 EST"
    - Date: "2024-01-01"
    - Time: "15:30:00"
  
  Relative (always human-readable):
    - "in 5 minutes"
    - "2 hours ago"
    - "yesterday at 3:30 PM"
    - "next Monday at 10:00 AM"

Timezone Handling:
  Priority for Display:
    1. User's saved preference (from profile)
    2. Browser timezone (if no preference set)
    3. Server timezone (if all else fails)
  
  User Preference Storage:
    - key: 'user.timezone', value: 'America/Los_Angeles'
    - key: 'user.date_format', value: 'US'
    - key: 'user.time_format', value: '12-hour'
    - key: 'user.relative_time', value: 'true'
    - key: 'user.week_starts', value: 'sunday'
  
  Server Always Provides:
    {
      "timestamp": "Jan 1, 2024 12:00:00 PM EST",  # Server timezone
      "timestamp_utc": "2024-01-01T17:00:00Z",      # UTC for conversion
      "timestamp_unix": 1704124800,                  # Unix for JS Date()
      "timestamp_iso": "2024-01-01T12:00:00-05:00"  # ISO with timezone
    }
  
  Client-Side Display Logic:
    ```javascript
    function displayTime(timestamp_unix, element) {
      const userTz = getUserTimezone();  // From profile or browser
      const userFormat = getUserDateFormat();  // From profile
      
      if (userTz === 'auto') {
        // Use browser's timezone
        timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
      } else {
        // Use saved preference
        timezone = userTz;
      }
      
      // Format according to user preferences
      const options = {
        timeZone: timezone,
        hour12: (userFormat.time === '12-hour'),
        ...userFormat.options
      };
      
      const date = new Date(timestamp_unix * 1000);
      element.textContent = date.toLocaleString(userFormat.locale, options);
    }
    ```
  
  Examples for Different Users:
    Server time: Jan 1, 2024 12:00 PM EST
    
    User A (Profile: PST, 12-hour, US format):
      Sees: "Jan 1, 2024 9:00 AM PST"
    
    User B (Profile: GMT, 24-hour, EU format):  
      Sees: "1 Jan 2024 17:00 GMT"
    
    User C (Profile: Auto, browser in Tokyo):
      Sees: "2024年1月2日 2:00" (localized)
    
    User D (No profile, no JS):
      Sees: "Jan 1, 2024 12:00 PM EST" (server default)
```

---

## 📊 Logging System

### Exact Log Formats

```yaml
Access Log:
  Location: /var/log/{projectname}/access.log
  Default Format: Apache Combined
  Example:
    192.168.1.100 - john [01/Jan/2024:12:00:00 +0000] "GET /user/profile HTTP/1.1" 200 4521 "http://example.com/" "Mozilla/5.0"
  
  Configurable Formats (via Admin UI):
    - Apache Combined (default)
    - Apache Common
    - Nginx
    - JSON
    - Custom

Error Log:
  Location: /var/log/{projectname}/error.log
  Format:
    [2024-01-01 12:00:00.123] [ERROR] [module] Message
    Stack trace if applicable
    Additional context

Audit Log:
  Location: /var/log/{projectname}/audit.log
  Format: JSON lines
  Example:
    {"timestamp":"2024-01-01T12:00:00Z","user":"admin","action":"user.update","resource":"user:123","old_value":"...","new_value":"...","ip":"192.168.1.100","success":true}

Log Rotation:
  - Rotate daily at midnight
  - Keep 30 days
  - Compress after 1 day with gzip
  - Archive monthly to /var/log/{projectname}/archive/
```

---

## ⏰ Built-in Scheduler

### Exact Required Scheduled Tasks

```yaml
Certificate Renewal:
  Cron: 0 2 * * *  (Daily at 2:00 AM)
  Task:
    1. Check each certificate expiry
    2. If expires in < 30 days:
       - Attempt renewal via Let's Encrypt
       - If success: Replace certificate, reload
       - If fail: Log error, alert admin
    3. Log results to audit

Database Backup:
  Cron: 0 3 * * *  (Daily at 3:00 AM)
  Task:
    1. Create database dump
    2. Compress with gzip
    3. Save to backup directory
    4. Rotate old backups
    5. Upload to external storage (if configured)
    6. Log results

User Data Backup:
  Cron: 0 4 * * *  (Daily at 4:00 AM)
  Task:
    1. Archive user uploaded files
    2. Include avatars and generated content
    3. Compress with tar.gz
    4. Save to backup directory
    5. Rotate old archives
    6. Log results

Log Rotation:
  Cron: 0 0 * * *  (Daily at midnight)
  Task:
    1. Rotate all log files
    2. Compress yesterday's logs
    3. Delete logs older than 30 days
    4. Archive monthly logs

Session Cleanup:
  Cron: */15 * * * *  (Every 15 minutes)
  Task:
    1. DELETE FROM sessions WHERE expires_at < NOW()
    2. Log count of removed sessions

Health Check:
  Cron: */5 * * * *  (Every 5 minutes)
  Task:
    1. Check database connection
    2. Check disk space (warn if < 10%)
    3. Check memory usage (warn if > 90%)
    4. Check certificate expiry
    5. Update health status

Backup Cleanup:
  Cron: 0 5 * * 0  (Weekly on Sunday at 5:00 AM)
  Task:
    1. Remove daily backups older than 30 days
    2. Remove weekly backups older than 12 weeks
    3. Remove monthly backups older than 12 months
    4. Update storage statistics
    5. Alert if low on backup space
```

---

## 👨‍💼 Admin Interface

### Exact Admin Pages and Functions

```yaml
/admin/dashboard:
  Widgets:
    - Active Users (count)
    - Total Users (count)
    - Server Uptime (duration)
    - Database Status (ok/error)
    - Request Rate (last 24 hours, line chart)
    - Response Times (last 24 hours, line chart)
    - Error Rate (last 24 hours, bar chart)
    - Recent Activity (last 10 audit log entries)

/admin/users:
  Table:
    - ID (UUID)
    - Username
    - Email
    - Role (administrator/user/guest)
    - Status (active/suspended/pending)
    - Created
    - Last Login
    - Actions (View, Edit, Delete, Suspend)
  Features:
    - Pagination (50 per page)
    - Search by username or email
    - Filter by role and status
    - Sort by any column

/admin/settings:
  Categories:
    Server:
      - server.title (text) - Application display name (shown in UI)
      - server.tagline (text) - Short subtitle/slogan (one line)
      - server.description (textarea) - Full description (shown in about, meta tags)
      - server.address (text)
      - server.http_port (number 1-65535)
      - server.https_port (number 1-65535)
      - server.https_enabled (checkbox)
      - server.timezone (dropdown: America/New_York, etc.)
      - server.date_format (dropdown: US, EU, ISO)
      - server.time_format (dropdown: 12-hour, 24-hour)
    
    Database:
      - db.type (select: sqlite/mysql/postgres/mssql)
      - db.host (text, if not sqlite)
      - db.port (number, if not sqlite)
      - db.name (text, if not sqlite)
      - db.user (text, if not sqlite)
      - db.password (password, if not sqlite)
      - [Test Connection] button
    
    Security:
      - security.session_timeout (number, minutes)
      - security.max_login_attempts (number)
      - security.lockout_duration (number, minutes)
      - security.password_min_length (number)
    
    Files:
      - robots.txt (textarea with syntax highlighting)
      - security.txt (textarea, saved to /.well-known/security.txt)

/admin/backup:
  Management interface for backup/restore operations
  Manual backup triggers
  Restore from backup uploads
  Backup history and downloads

/admin/monitoring:
  Prometheus metrics configuration
  Enable/disable metrics endpoint
  Generate access tokens
  Configure metric categories
```

---

## 👤 User Profile & Account Pages

### Comprehensive Profile Management

```yaml
/user/profile - Main Profile Page:
  
  Layout Structure:
    Header Section:
      - Large avatar (200x200px)
      - Upload/Change avatar button
      - Display name (large font)
      - Username (@username)
      - Member since date
      - Last active
      - Profile completion percentage
    
    Tabs/Sections:
      1. Profile Information
      2. Account Settings  
      3. Security
      4. Sessions
      5. Notifications
      6. Tokens
      7. Privacy & Data
    
    Tab 1 - Profile Information:
      Personal Details:
        - Display Name
        - Username (unique)
        - Bio (500 chars)
        - Website
        - Location
        - Timezone
      
      Contact Information:
        - Email (verification required)
        - Backup Email (optional)
        - Phone (optional)
        
      Avatar Settings:
        - Current avatar display
        - Upload new (drag & drop)
        - Use Gravatar option
        - Remove avatar
        - Avatar history (last 5)
    
    Tab 2 - Account Settings:
      Preferences:
        - Language (dropdown)
        - Timezone (dropdown with search):
          * Auto-detect (use browser timezone)
          * Or select from list (America/New_York, Europe/London, etc.)
          * Shows current time in selected zone: "Currently 3:30 PM"
        - Date format (dropdown with preview):
          * US: "Jan 1, 2024" 
          * EU: "1 Jan 2024"
          * ISO: "2024-01-01"
          * Custom: User defined
        - Time format (toggle with preview):
          * 12-hour: "3:30 PM"
          * 24-hour: "15:30"
        - Relative time (toggle):
          * Show relative: "2 hours ago"
          * Show absolute: "Jan 1, 2024 3:30 PM"
        - First day of week (dropdown):
          * Sunday (US standard)
          * Monday (ISO standard)
        - Theme (dark/light/auto)
        - Accessibility options
      
      Email Preferences:
        - Email visibility
        - Newsletter subscription
        - Product updates
        - Security alerts (always on)
    
    Tab 3 - Security:
      Password:
        - Last changed date
        - Password strength indicator
        - Change password button
        
      Two-Factor Authentication:
        - Status (enabled/disabled)
        - Setup/Manage button
        - Backup codes
        - Recovery email
      
      Login Security:
        - Suspicious login alerts
        - New device alerts
        - Email verification for new devices
    
    Tab 4 - Active Sessions:
      Current Session:
        - Highlighted current device
        - IP address (partially masked)
        - Browser/OS
        - Location (city level)
        - Login time
      
      Other Sessions:
        - Device name/type
        - Browser/OS
        - Location
        - Last active
        - Terminate button per session
        - Terminate All button
    
    Tab 5 - Notifications:
      Channels:
        - Email (per category)
        - In-app (per category)
        - Push (if enabled)
      
      Categories:
        - Security alerts
        - Account changes
        - Activity notifications
      
      Quiet Hours:
        - Enable/disable
        - Time range
        - Timezone
        - Override for critical
    
    Tab 6 - Tokens:
      Create Token:
        - Token name
        - Expiration
        - Permissions
        - Generate button
      
      Active Tokens:
        - Name
        - Created
        - Last Used
        - Expires
        - Actions (Delete)
      
      Usage Stats:
        - Requests today/month
        - Rate limit status
    
    Tab 7 - Privacy & Data:
      Privacy Settings:
        - Profile visibility
        - Online status
        - Last active display
        - Message preferences
      
      Data Management:
        Export Data:
          - Format options (JSON/CSV/XML)
          - Request Export button
          - Download when ready
        
        Delete Account:
          - Warning message
          - Grace period (30 days)
          - Requires password
          - Delete Account button
```

### User Dashboard (/user)

```yaml
Dashboard Layout:
  Welcome Section:
    - Greeting with name
    - Profile completion prompt
    - Quick stats
  
  Quick Actions Grid:
    - Edit Profile
    - Change Password
    - View Tokens
    - Download Data
    - Manage Sessions
    - Notification Settings
  
  Recent Activity:
    - Last 10 actions
    - Timestamps
    
  Account Overview:
    - Plan details
    - Usage metrics
    - Storage used
  
  Security Status:
    - Password strength
    - 2FA status
    - Active sessions count
```

---

## 💾 Backup & Restore

### Automatic Backup System

```yaml
Backup Schedule:
  Database Backup:
    Cron: 0 3 * * *  (Daily at 3:00 AM)
    Retention: 30 days local, 90 days if external storage
    Format: SQL dump (compressed)
    Location: /var/lib/{projectname}/backups/db/
    Naming: {projectname}_db_YYYY-MM-DD_HH-MM-SS.sql.gz
  
  User Data Backup:
    Cron: 0 4 * * *  (Daily at 4:00 AM)
    Includes:
      - Uploaded files
      - User avatars
      - Generated reports
    Format: tar.gz archive
    Location: /var/lib/{projectname}/backups/data/
    Naming: {projectname}_data_YYYY-MM-DD_HH-MM-SS.tar.gz
  
  Settings Backup:
    Trigger: On every settings change
    Includes:
      - All settings table data
      - SSL certificates (encrypted)
    Format: JSON (encrypted)
    Location: /var/lib/{projectname}/backups/config/
    Keep: Last 100 versions

Backup Management:
  Rotation:
    - Daily: Keep 30 days
    - Weekly: Keep 12 weeks  
    - Monthly: Keep 12 months
    - Yearly: Keep indefinitely
  
  Storage:
    Local (Always):
      Path: /var/lib/{projectname}/backups/
      Min required: 10GB
      Alert if < 1GB free
    
    External (Optional):
      - S3 compatible
      - SFTP/FTP
      - Network attached storage
```

### Restore Process

```yaml
Pre-Restore:
  1. Verify backup integrity
  2. Check version compatibility
  3. Ensure sufficient disk space
  4. Create restore point
  5. Enter maintenance mode

Database Restore:
  1. Stop application
  2. Backup current database
  3. Import backup SQL
  4. Run migrations if needed
  5. Verify data integrity
  6. Restart services

User Data Restore:
  1. Backup current data
  2. Extract archive
  3. Verify files
  4. Move to locations
  5. Fix permissions
  6. Update references

Post-Restore:
  1. Run health checks
  2. Verify /healthz
  3. Test critical functions
  4. Notify admin
  5. Exit maintenance mode
```

---

## 📊 Monitoring & Metrics

### Prometheus Metrics (Disabled by Default)

```yaml
Configuration:
  Default State: DISABLED
  Enable via: Admin UI → Settings → Monitoring
  Endpoint: /metrics (when enabled)
  Authentication: Bearer token required
  Format: Prometheus text format

Available Metrics:
  System:
    - {projectname}_up
    - {projectname}_version_info
    - {projectname}_start_time_seconds
    - {projectname}_http_requests_total
    - {projectname}_http_request_duration_seconds
    
  Database:
    - {projectname}_db_connections_active
    - {projectname}_db_connections_idle
    - {projectname}_db_queries_total
    - {projectname}_db_query_duration_seconds
    
  Business:
    - {projectname}_users_total
    - {projectname}_sessions_active
    - {projectname}_tokens_active
    - {projectname}_logins_total

Security:
  - Disabled by default
  - Requires explicit enablement
  - Token authentication
  - IP whitelist optional
  - No sensitive data
  - Rate limited
```

---

## 🛠️ Development Mode

### Development Environment

```yaml
Detection (ANY of):
  - Environment: DEV=true (first run only)
  - Build tag: -tags development
  - Binary name: {projectname}-dev
  - Flag: --dev

Never in Production:
  - Disabled if port 80/443
  - Disabled if external DB
  - Warning if attempted

Development Features:
  Hot Reload:
    - File watcher on ./src
    - Template reload without restart
    - CSS/JS reload without rebuild
    - Database schema auto-migration
  
  Debug Endpoints:
    GET /debug/routes     - List all routes
    GET /debug/config     - Show configuration
    GET /debug/db         - Database statistics
    GET /debug/sessions   - Active sessions
    POST /debug/reset     - Reset to fresh state
  
  Enhanced Logging:
    - SQL query logging
    - HTTP request/response bodies
    - Template rendering times
    - Detailed stack traces
  
  Development Tools:
    - Generate test users
    - Generate test data
    - Reset database
    - Clear cache
    - Disable rate limiting
    - Fast session timeout (5 min)
    - CORS allow all origins

Build Differences:
  Development:
    - Source maps included
    - Assets not minified
    - Templates not cached
    - Debug symbols included
    - Binary size: ~2x larger
  
  Production:
    - Source maps stripped
    - Assets minified
    - Templates embedded
    - Debug symbols stripped
    - Binary size: optimized

Development Commands:
  make dev          # Build development version
  make run-dev      # Run with hot reload
  make test-watch   # Run tests on change
  make mock-data    # Generate test data
  make reset-dev    # Reset development
```

---

## 🔄 Live Reload Support

### Automatic Reload Triggers

```yaml
Configuration Changes (No Restart Required):
  Database Settings:
    - All settings table changes
    - User preferences
    - Feature toggles
    - Rate limits
    - Security policies
    - Email settings
    - Storage paths
  
  Content Updates:
    - robots.txt changes
    - security.txt changes
    - Template modifications (dev mode)
    - Static asset updates (dev mode)
    - Translation files
    - Email templates
  
  SSL Certificates:
    - Certificate renewal
    - Certificate replacement
    - New certificate installation
    - Certificate chain updates

User & Permission Changes (Instant):
  - User role changes
  - User status changes
  - Permission updates
  - Session invalidation
  - Token revocation
  - Organization settings

Scheduler Updates (Dynamic):
  - Task enable/disable
  - Cron expression changes
  - Task configuration
  - New task addition
  - Task deletion

Requires Restart:
  - Port changes
  - Database connection string
  - Binary updates
  - Core security settings
  - Memory limits
  - Worker pool size

Implementation:
  File Watchers:
    Production:
      - Database polling every 5 seconds for settings changes
      - Certificate directory monitoring
      - Signal handling (SIGHUP for reload)
    
    Development:
      - File system watcher on ./src
      - Template directory watching
      - Static asset watching
      - Instant reload on change
  
  Reload Process:
    1. Detect change
    2. Validate new configuration
    3. Create new handler/worker
    4. Gracefully drain old connections
    5. Switch to new configuration
    6. Clean up old handlers
    7. Log reload event
  
  WebSocket Notifications:
    - Send reload event to connected clients
    - Clients refresh if needed
    - Admin UI shows "Settings Updated" toast
    - No user disruption

Database Change Detection:
  Settings Watcher:
    ```sql
    SELECT key, value, updated_at 
    FROM settings 
    WHERE updated_at > last_check
    ```
    - Check every 5 seconds
    - Compare checksums
    - Apply changes without restart
  
  Certificate Watcher:
    - Monitor /etc/{projectname}/ssl/
    - Monitor /etc/letsencrypt/live/
    - Reload on file change
    - Verify certificate validity
    - Seamless SSL reload

Signal Handling:
  SIGHUP (reload):
    - Reload configuration
    - Reload certificates
    - Clear caches
    - Keep connections alive
  
  SIGUSR1 (reopen logs):
    - Close current log files
    - Open new log files
    - No service interruption
  
  SIGUSR2 (graceful restart):
    - Start new process
    - Transfer listening sockets
    - Drain old connections
    - Zero downtime restart

Admin UI Integration:
  Live Settings:
    - Changes apply immediately
    - No "Save and Restart" button
    - Green checkmark on successful apply
    - Rollback on error
  
  Status Indicators:
    - "Live" badge when changes applied
    - "Restart Required" for core changes
    - "Reloading..." spinner during reload
    - Error messages if reload fails

Development Mode Enhanced:
  Hot Module Replacement:
    - Code changes without restart
    - Template changes instant
    - CSS/JS injection
    - State preservation
  
  Browser Auto-Refresh:
    - WebSocket connection to server
    - Refresh on backend changes
    - Preserve form data
    - Maintain scroll position

API Endpoints:
  GET  /api/v1/admin/reload/status  - Check if reload needed
  POST /api/v1/admin/reload         - Trigger manual reload
  GET  /api/v1/admin/reload/history - Reload event history
```

---

## 💻 CLI Interface

### Exact CLI Commands and Behavior

```bash
# Commands that DO NOT require privileges
{projectname} --help
  Output:
    Usage: {projectname} [OPTIONS]
    Options:
      --help            Show this help message
      --version         Show version information
      --status          Show server status and exit with code
      --port PORT       Set port(s) (e.g., 8080 or "8080,8443")
      --data DIR        Set data directory (must be directory)
      --config DIR      Set config directory (must be directory)
      --address ADDR    Set listen address (e.g., 0.0.0.0)
      --dev             Run in development mode

{projectname} --version
  Output:
    {projectname} version {version}
    Built: {build date}
    Commit: {git commit hash}

{projectname} --status
  Output (if running):
    ✅ Server: Running on 192.168.1.100:8080
    ✅ Database: Connected (PostgreSQL)
    ✅ Cache: Active
    🔒 SSL: Valid until 2024-03-01
    📊 Health: Optimal
    Exit code: 0
  
  Output (if not running):
    ❌ Server: Not running
    Exit code: 1

# Commands that MAY require privileges
{projectname} --port 8080
  Behavior:
    - Sets HTTP port to 8080
    - Saves to database
    - No HTTPS

{projectname} --port "8080,8443"
  Behavior:
    - Sets HTTP port to 8080
    - Sets HTTPS port to 8443
    - Saves to database

{projectname} --data /var/lib/{projectname}
  Behavior:
    - If path exists as file: Remove file
    - Create directory with parents
    - Set as data directory
    - Save to database

{projectname} --config /etc/{projectname}
  Behavior:
    - If path exists as file: Remove file
    - Create directory with parents
    - Set as config directory
    - Save to database

{projectname} --address 0.0.0.0
  Behavior:
    - Set listen address
    - Save to database
    - Binds to all interfaces
```

---

## 🔨 Build System

### Exact Makefile Targets and Behavior

```makefile
# Variables
PROJECTNAME = {projectname}
PROJECTORG = casapps
VERSION = $(shell cat release.txt || echo "0.0.1")
COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Targets (exact names required)
.PHONY: build release test docker dev

build:
	# Build for all platforms
	# Output to ./binaries/
	# Naming: {projectname}-{os}-{arch}
	# Strip -musl binaries
	# Also create {projectname} for host system
	# Auto-increment version in release.txt
	
	# Linux AMD64
	# Linux ARM64
	# Windows AMD64/ARM64
	# macOS AMD64/ARM64
	# BSD AMD64

release:
	# Delete existing release/tag if exists
	# Create new GitHub release
	# Upload all binaries

test:
	# Run all unit tests
	# Run integration tests
	# Run benchmarks

docker:
	# Build container
	# Tag as {projectname}:dev
	# Push to ghcr.io/{projectorg}/{projectname}:latest

dev:
	# Build development version
	# Enable hot reload
	# Include debug symbols
```

### Version Management

```yaml
File: release.txt
Format: MAJOR.MINOR.PATCH (Semantic Versioning)
Example: 1.0.0

Behavior:
  - Read on build
  - Auto-increment PATCH on each build
  - Embedded in binary
  - Shown in --version
  - Shown in admin UI
  - Used for GitHub releases
```

---

## 🐳 Docker Configuration

### Exact Dockerfile

```dockerfile
# Multi-stage build - NO interpretation allowed
FROM alpine:latest AS builder

# Install requirements
RUN apk add --no-cache bash curl make go

# Copy source
WORKDIR /build
COPY . .

# Build binary
RUN make build

# Runtime stage
FROM scratch

# Copy binary only
COPY --from=builder /build/binaries/{projectname}-linux-amd64 /{projectname}

# Metadata labels (required)
LABEL org.opencontainers.image.source="https://github.com/{projectorg}/{projectname}"
LABEL org.opencontainers.image.description="{projectname} server"
LABEL org.opencontainers.image.licenses="MIT"

# Expose port (informational only)
EXPOSE 80

# Run
ENTRYPOINT ["/{projectname}"]
```

### Exact Docker Compose

```yaml
# NO version field
# NO build definition
services:
  {projectname}:
    image: ghcr.io/{projectorg}/{projectname}:latest
    container_name: {projectname}
    restart: unless-stopped
    environment:
      - DB_TYPE=sqlite  # First run only
    volumes:
      - ./rootfs/data/{projectname}:/data
      - ./rootfs/config/{projectname}:/config
    ports:
      # Development (random port 64000-64999)
      - "127.0.0.1:64080:80"
      # Production (uncomment)
      # - "172.17.0.1:64080:80"
    networks:
      - {projectname}

networks:
  {projectname}:
    name: {projectname}
    driver: bridge
    external: false
```

---

## 📦 Installation Scripts

### Directory Structure

```
./scripts/
├── install.sh       # Main installer, detects OS
├── linux.sh         # Linux-specific
├── macos.sh         # macOS-specific
├── windows.ps1      # Windows-specific
├── restore.sh       # Backup restore script
└── README.md        # Installation at top, dev at bottom
```

### User Creation Requirements

```yaml
System User:
  - Same UID and GID
  - Between 100-999 (find unused)
  - Home: config or data directory
  - Shell: /bin/false or /sbin/nologin
  
Directory Creation:
  With Root:
    /etc/{projectname}/
    /var/lib/{projectname}/
    /var/log/{projectname}/
  
  Without Root:
    ~/.config/{projectname}/
    ~/.local/share/{projectname}/
    ~/.cache/{projectname}/
```

### Installation Process

```bash
# Main installer (install.sh)
#!/usr/bin/env bash
set -e

# Detect OS
OS="$(uname -s)"
case "${OS}" in
    Linux*)     ./scripts/linux.sh "$@";;
    Darwin*)    ./scripts/macos.sh "$@";;
    MINGW*|MSYS*|CYGWIN*) 
                echo "Please run windows.ps1 in PowerShell"
                exit 1;;
    *)          echo "Unsupported OS: ${OS}"
                exit 1;;
esac
```

---

## 🔐 Security Implementation

### Exact Validation Rules

```yaml
Username:
  - Regex: ^[a-zA-Z0-9_]+$
  - Min length: 3
  - Max length: 50
  - Case insensitive unique
  - Reserved: admin, administrator, root, system

Email:
  - Regex: ^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$
  - Max length: 255
  - Case insensitive unique

Password:
  - Min length: 8 (users), 12 (administrators)
  - Max length: 128
  - Must contain: lowercase, uppercase, number
  - Should contain: special character

Session Management:
  - Default duration: 30 days
  - Remember me: 30 days
  - No remember me: Session only (browser close)
  - Token rotation: At 50% lifetime
  - Multi-device: Unlimited sessions per user
  - Secure cookies: HttpOnly, Secure, SameSite=Lax
  - Activity tracking: Update last_activity on each request
  - Auto-refresh: Every 5 minutes when active

Token Generation:
  - 32 bytes random data
  - Base64url encoded
  - Prefix: "tok_"
  - Format: tok_[52 characters]
  - Show ONCE only
  - Hash with SHA-256 for storage

Rate Limiting:
  - Login: 5 attempts per 15 minutes per IP
  - API: 100 requests per minute per token
  - Registration: 3 per hour per IP
  - Password Reset: 3 per hour per email
  - Session Creation: 10 per hour per user
```

---

## 🚨 Error Handling

### Database Connection Error (The ONLY Critical Error)

```yaml
On Connection Loss:
  1. Log exact error with timestamp
  2. Set global flag: READ_ONLY_MODE = true
  3. Show admin banner: "⚠️ Database Unavailable - Read Only Mode"
  4. Admin UI shows troubleshooting steps
  5. Self-healing: Retry every 30 seconds
  6. On recovery: Process queued writes, clear banner

Behavior in Read-Only Mode:
  - GET requests: Serve from SQLite cache
  - POST/PUT/DELETE: Queue with timestamp
  - Admin: Browse only, cannot modify
  - Forms show: "Read-only mode - changes queued"
  - /healthz returns: {"status":"degraded","database":"disconnected"}

Admin Troubleshooting UI:
  /admin/database/troubleshoot
  Steps shown:
    1. Check database service status
    2. Verify connection credentials
    3. Check network connectivity
    4. Review error logs
    [Retry Connection Now] button
```

---

## 📁 Directory Structure

### Exact Project Structure (No Ambiguity)

```
./ (Project root - working directory)
├── src/                      # ALL source code
│   ├── main.{ext}          # Entry point
│   ├── server/              # HTTP server
│   ├── database/            # Database layer
│   ├── auth/                # Authentication
│   ├── api/                 # API routes
│   ├── web/                 # Web routes
│   ├── admin/               # Admin routes
│   ├── scheduler/           # Cron tasks
│   ├── static/              # Embedded assets
│   └── templates/           # HTML templates
├── scripts/                  # Installation scripts
│   ├── install.sh
│   ├── linux.sh
│   ├── macos.sh
│   ├── windows.ps1
│   ├── restore.sh
│   └── README.md
├── binaries/                # Built binaries (gitignored)
├── release/                 # Release artifacts (gitignored)
├── rootfs/                  # Docker volumes
│   ├── data/
│   │   └── {projectname}/
│   ├── config/
│   │   └── {projectname}/
│   └── db/
│       ├── postgres/
│       ├── mysql/
│       └── sqlite/
├── tests/                   # Test files
│   ├── unit/
│   ├── integration/
│   └── e2e/
├── docs/                    # Documentation (if needed)
├── .github/                 # GitHub specific
│   └── workflows/
├── Makefile                 # Build automation (required)
├── Dockerfile               # Container definition (required)
├── docker-compose.yml       # Compose file (required)
├── Jenkinsfile             # CI/CD (required)
├── README.md               # Main docs (install/prod at top)
├── LICENSE.md              # MIT + embedded licenses (required)
├── release.txt             # Version tracking (required)
├── .gitignore              # Git ignores (required)
└── .dockerignore           # Docker ignores (required)

DO NOT PUT:
  - Source files in root
  - Config files anywhere
  - .env files
  - Random scripts in root
  - Build output in src/
```

### .gitignore Requirements

```gitignore
# Binaries
binaries/
release/
*.exe
*.dll
*.so
*.dylib

# Test binary
*.test

# Databases
*.db
*.sqlite
*.sqlite3

# Logs
*.log

# OS
.DS_Store
Thumbs.db

# IDE
.vscode/
.idea/
*.swp

# Dependencies (language specific)
vendor/
node_modules/
__pycache__/

# Environment (should not exist but ignore)
.env
.env.*

# Temporary
tmp/
temp/
*.tmp
```

---

## 🔄 Integration with Existing Projects

### Adding to Existing Codebase

```yaml
Step 1 - Add Template Code:
  1. Create src/server/ in existing project
  2. Copy template database schema
  3. Add authentication module
  4. Add admin routes under /admin
  5. Keep existing routes unchanged

Step 2 - Database Integration:
  If has users table:
    - Add missing columns only
    - Map existing roles
  If no users table:
    - Create all template tables

Step 3 - Route Integration:
  Mount template routes:
    - /admin/* → Template admin
    - /api/v1/admin/* → Admin API
    - /healthz → Health check
    - /setup/* → First run only
  Keep existing routes unchanged

Step 4 - Binary Building:
  - Embed all assets
  - Create single binary
  - Include all code

Step 5 - Migration:
  1. Deploy alongside existing
  2. Run on different port
  3. Test template features
  4. Migrate users if needed
  5. Switch traffic when ready
```

---

## 🤖 AI Development Tool Usage

### For New Projects

```yaml
Tell AI:
  "Create a new server following Universal Server Template Specification v1.0
   Project name: {exact name}
   Organization: casapps
   Purpose: {specific purpose}
   Language: {go|rust|python|node|etc}
   Database: {sqlite|postgres|mysql|mssql}
   
   Requirements:
   - Single binary with embedded assets
   - Database for all config (no config files)
   - Administrator separate from users
   - Admin can only browse as guest
   - Routes scoped correctly
   - Tokens shown once only
   - /healthz endpoint with all data
   - Professional UI components only
   - 30-day persistent sessions
   - Input validation on everything"
```

### For Existing Projects

```yaml
Tell AI:
  "Add Universal Server Template v1.0 to existing {language} project
   
   Current:
   - Database: {type}
   - Framework: {framework}
   - User system: {exists|none}
   
   Add:
   - Admin system under /admin
   - Database layer with SQLite fallback
   - Authentication if missing
   - API under /api/v1
   - /healthz endpoint
   - Professional UI components
   - Single binary build
   
   Do not modify existing routes"
```

### Validation Checklist for AI Output

```yaml
□ Single binary with all assets embedded?
□ No configuration files created?
□ Database used for all settings?
□ Administrator account separate?
□ Admin limited to /admin routes?
□ Admin browses as guest only?
□ Routes flexible for existing projects?
□ API uses tokens not api_keys?
□ /healthz endpoint comprehensive?
□ Tokens shown once only?
□ All inputs validated?
□ Professional UI components (no alert/confirm)?
□ Sessions persist for 30 days?
□ Mobile responsive (98%/90%)?
□ Dark theme default?
□ Security headers present?
□ No localhost shown to users?
□ Using SERVER_ADDRESS?
□ Working directory is . ?
□ All source in ./src?
□ No AI/ML in core logic?
```

---

## 📝 Implementation Requirements

### Language-Agnostic Requirements

```yaml
Must Have:
  1. Single static binary
  2. All assets embedded
  3. Database for config
  4. SQLite always present
  5. First user flow exact
  6. Admin separation complete
  7. Routes flexible for integration
  8. API v1 complete
  9. /healthz comprehensive
  10. Professional UI components
  11. PWA support
  12. Dark theme default
  13. Security headers
  14. Let's Encrypt
  15. Installation scripts
  16. Multi-platform builds
  17. Persistent sessions (30 days)
  18. Backup/restore system
  19. Development mode
  20. Prometheus metrics (disabled by default)

Binary Naming:
  - {projectname}-linux-amd64
  - {projectname}-linux-arm64
  - {projectname}-windows-amd64.exe
  - {projectname}-windows-arm64.exe
  - {projectname}-macos-amd64
  - {projectname}-macos-arm64
  - {projectname}-bsd-amd64
  - {projectname} (host system)

Required Files:
  - Makefile (exact targets)
  - Dockerfile (Alpine to scratch)
  - docker-compose.yml (exact format)
  - README.md (install at top)
  - LICENSE.md (MIT + embedded)
  - release.txt (version)
  - .gitignore (complete)

Jenkinsfile Requirements:
  agents: arm64, amd64
  server: jenkins.casjay.cc
```

---

## ✅ Final Checklist

### Before Implementation Complete

```yaml
Core Architecture:
  □ Single binary, everything embedded
  □ Working directory is . (current directory)
  □ All source in ./src
  □ All scripts in ./scripts
  □ No config files anywhere
  □ Database only configuration
  □ SQLite always works
  □ External DB with fallback

User System:
  □ First user creates admin
  □ Admin username is "administrator"
  □ Admin only /admin access
  □ Admin browses as guest
  □ Regular users blocked from admin
  □ 30-day persistent sessions
  □ Profile menu with avatar system

Web Interface:
  □ 98% width < 720px
  □ 90% width >= 720px
  □ Footer at bottom
  □ Dark theme default
  □ Professional UI components only
  □ No alert(), confirm(), prompt()
  □ PWA manifest
  □ /healthz comprehensive

Security:
  □ All inputs validated
  □ Tokens shown once
  □ Security headers
  □ Rate limiting
  □ Audit logging
  □ Session persistence secure

API:
  □ RESTful v1
  □ GraphQL endpoint
  □ Swagger docs
  □ .txt responses
  □ tokens naming (not api_keys)

Operations:
  □ Health at /healthz
  □ Apache log format
  □ Log rotation
  □ Scheduled tasks
  □ Let's Encrypt automation
  □ Self-healing attempts
  □ Backup/restore system
  □ Prometheus metrics (disabled)

Build & Deploy:
  □ Makefile targets
  □ Multi-platform binaries
  □ Docker multi-stage
  □ Install scripts for all OS
  □ Version tracking
  □ Development mode support

Integration:
  □ Works with new projects
  □ Works with existing projects
  □ Routes can be prefixed
  □ Non-breaking additions
```

---

*This is the Universal Server Template Specification v1.0 - a complete, unambiguous template for building server applications. Every requirement is mandatory for template features. Existing projects implement only what they need. The working directory is always . (current directory). No interpretation allowed. Follow exactly as written.*

