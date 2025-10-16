# gitmessages - TODO List

## ✅ Completed

### Core API
- [x] Database schema with SQLite
- [x] Message loading from embedded JSON (5096 messages)
- [x] Random message endpoint with cycle tracking
- [x] No duplicate messages until all exhausted
- [x] Stats endpoint showing usage metrics
- [x] Full message list endpoint (/api/v1/messages.json)
- [x] Plain text endpoints (.txt variants)
- [x] Health check endpoint (/healthz)

### Frontend Framework
- [x] HTML templates with base layout
- [x] CSS design system (SPEC v1.0 compliant)
  - [x] Dark theme by default
  - [x] Light theme support
  - [x] Responsive design (98% width <720px, 90% width ≥720px)
  - [x] CSS variables for consistency
- [x] Professional JavaScript UI components
  - [x] Modal dialogs (NO alert/confirm/prompt)
  - [x] Toast notifications
  - [x] Form validation helpers
  - [x] API request helpers
  - [x] Timezone conversion
- [x] PWA manifest
- [x] Static asset serving (CSS, JS, images)
- [x] Security headers (CORS, X-Frame-Options, etc.)

### Pages Created
- [x] Homepage with API demo
- [x] Setup flow pages (welcome, register, admin)
- [x] Login page
- [x] User dashboard
- [x] Admin dashboard

## 🚧 In Progress

None currently

## 📋 TODO - Authentication & Sessions

### User Management
- [ ] User registration implementation
  - [ ] Username validation (3-50 chars, alphanumeric + underscore)
  - [ ] Email validation
  - [ ] Password hashing (bcrypt)
  - [ ] Password strength requirements (8 chars min, uppercase, lowercase, number)
- [ ] Login authentication
  - [ ] Session creation (30-day default)
  - [ ] Remember me functionality
  - [ ] Session token generation and storage
- [ ] First user setup flow
  - [ ] Check if users exist in database
  - [ ] Create first user (owner)
  - [ ] Create administrator account (username: "administrator", min 12 chars password)
  - [ ] Auto-login as administrator
  - [ ] Redirect to /admin/settings

### Session Management
- [ ] Session persistence (30 days default)
- [ ] Multi-device support
- [ ] Session listing page
- [ ] Terminate session functionality
- [ ] Auto-refresh tokens at 50% lifetime
- [ ] Activity tracking (last_activity update)

### Admin Account
- [ ] Administrator role separation
  - [ ] Admin can only access /admin/* routes
  - [ ] Admin browses other pages as guest/anonymous
  - [ ] Cannot access /user/* routes
- [ ] Admin authentication check middleware

## 📋 TODO - User Features

### Profile Management
- [ ] User profile page
  - [ ] Avatar upload/change
  - [ ] Display name editing
  - [ ] Bio editing
  - [ ] Email change with verification
- [ ] User settings page
  - [ ] Timezone selection
  - [ ] Date format (US/EU/ISO)
  - [ ] Time format (12/24 hour)
  - [ ] Theme toggle (dark/light/auto)
  - [ ] Language selection

### API Tokens
- [ ] Token generation
  - [ ] Show token ONCE only
  - [ ] Token format: tok_[52 chars base64url]
  - [ ] SHA-256 hash for storage
- [ ] Token management page
  - [ ] List active tokens
  - [ ] Token name/description
  - [ ] Last used timestamp
  - [ ] Revoke token functionality
- [ ] Token authentication for API endpoints

### Security
- [ ] Two-factor authentication (2FA)
  - [ ] Setup page with QR code
  - [ ] Backup codes
  - [ ] Verification on login
- [ ] Password change
- [ ] Failed login tracking
- [ ] Account lockout after 5 failed attempts (15 min duration)

## 📋 TODO - Admin Features

### User Management
- [ ] Users list page
  - [ ] Pagination (50 per page)
  - [ ] Search by username/email
  - [ ] Filter by role/status
  - [ ] Sort by any column
- [ ] User detail page
- [ ] Edit user (role, status)
- [ ] Suspend/unsuspend user
- [ ] Delete user

### Server Settings
- [ ] Settings management page
  - [ ] Server title/tagline/description
  - [ ] Port configuration
  - [ ] HTTPS enable/disable
  - [ ] Timezone and date/time formats
  - [ ] Registration enable/disable
- [ ] Database settings
  - [ ] Connection configuration
  - [ ] Test connection button
  - [ ] Fallback to SQLite on failure
- [ ] robots.txt editor (save to database)
- [ ] security.txt editor (save to database)

### SSL/TLS Management
- [ ] Certificate status display
- [ ] Let's Encrypt integration
  - [ ] Auto-request on ports 80,443
  - [ ] HTTP-01 challenge
  - [ ] DNS-01 challenge support
  - [ ] Certificate renewal (daily at 2 AM)
- [ ] Self-signed certificate generation
- [ ] Certificate upload

### System Monitoring
- [ ] Health check integration
- [ ] Disk space monitoring
- [ ] Memory usage monitoring
- [ ] Active sessions display
- [ ] Request rate graphs

### Scheduler
- [ ] Scheduled tasks management
  - [ ] List all tasks
  - [ ] Enable/disable tasks
  - [ ] Edit cron expressions
  - [ ] Manual task execution
  - [ ] Task history/logs
- [ ] Built-in tasks:
  - [ ] Certificate renewal (daily 2 AM)
  - [ ] Database backup (daily 3 AM)
  - [ ] User data backup (daily 4 AM)
  - [ ] Log rotation (daily midnight)
  - [ ] Session cleanup (every 15 min)
  - [ ] Health check (every 5 min)

### Backup & Restore
- [ ] Backup management page
  - [ ] Manual backup trigger
  - [ ] List backups with dates
  - [ ] Download backup
  - [ ] Delete old backups
- [ ] Restore functionality
  - [ ] Upload backup
  - [ ] Restore with verification
  - [ ] Maintenance mode during restore
- [ ] Automatic retention:
  - [ ] Daily: keep 30 days
  - [ ] Weekly: keep 12 weeks
  - [ ] Monthly: keep 12 months

### Audit Log
- [ ] Audit log viewer
  - [ ] Filter by user/action/resource
  - [ ] Date range selection
  - [ ] Export to CSV/JSON
- [ ] Audit logging for all actions:
  - [ ] User creation/modification/deletion
  - [ ] Settings changes
  - [ ] Login attempts
  - [ ] Token creation/revocation
  - [ ] Admin actions

## 📋 TODO - Database & Storage

### Database
- [ ] Read-only mode on connection failure
  - [ ] Switch to SQLite cache
  - [ ] Queue writes with timestamps
  - [ ] Banner notification
  - [ ] Auto-retry every 30 seconds
  - [ ] Replay queued writes on recovery
- [ ] External database support
  - [ ] MySQL/MariaDB (port 3306)
  - [ ] PostgreSQL (port 5432)
  - [ ] MSSQL (port 1433)
- [ ] Connection pooling configuration
- [ ] Migration system

### Settings Storage
- [ ] All settings in database (NO config files)
- [ ] Settings categories:
  - [ ] server.* (title, ports, timezone, etc.)
  - [ ] db.* (database connection)
  - [ ] security.* (session timeout, lockout, etc.)
  - [ ] features.* (registration enabled, etc.)

## 📋 TODO - Build & Deploy

### Build System
- [ ] Multi-platform builds
  - [ ] Linux AMD64
  - [ ] Linux ARM64
  - [ ] Windows AMD64/ARM64
  - [ ] macOS AMD64/ARM64
  - [ ] BSD AMD64
- [ ] Version auto-increment in release.txt
- [ ] Strip debug symbols for production
- [ ] Embed all assets in binary

### Docker
- [ ] Multi-stage Dockerfile (Alpine → scratch)
- [ ] docker-compose.yml with volumes
- [ ] GitHub Container Registry publishing

### Installation
- [ ] install.sh (OS detection)
- [ ] linux.sh (systemd service)
- [ ] macos.sh (launchd service)
- [ ] windows.ps1 (NSSM service)
- [ ] System user creation (UID/GID 100-999)

## 📋 TODO - API Enhancements

### Documentation
- [ ] Swagger UI at /api/docs
- [ ] GraphQL endpoint at /api/graphql
- [ ] Interactive API playground

### Rate Limiting
- [ ] Per-IP for public endpoints (100 req/min)
- [ ] Per-token for authenticated endpoints
- [ ] Rate limit headers:
  - [ ] X-RateLimit-Limit
  - [ ] X-RateLimit-Remaining
  - [ ] X-RateLimit-Reset

### Additional Endpoints
- [ ] /api/v1/user/* (all user endpoints)
- [ ] /api/v1/admin/* (all admin endpoints)
- [ ] /api/v1/auth/* (authentication endpoints)

## 📋 TODO - Development Features

### Development Mode
- [ ] Enable with --dev flag or DEV=true
- [ ] Hot reload for templates
- [ ] Hot reload for CSS/JS
- [ ] Debug endpoints at /debug/*
- [ ] SQL query logging
- [ ] Detailed stack traces
- [ ] Test data generation

### Live Reload
- [ ] Database settings polling (every 5 sec)
- [ ] Certificate directory monitoring
- [ ] SIGHUP for configuration reload
- [ ] WebSocket for client notification
- [ ] Zero-downtime restarts

## 📋 TODO - Monitoring

### Prometheus Metrics
- [ ] /metrics endpoint (disabled by default)
- [ ] Token authentication for metrics
- [ ] System metrics (CPU, memory, disk)
- [ ] HTTP metrics (requests, duration, errors)
- [ ] Database metrics (connections, queries)
- [ ] Business metrics (users, sessions, tokens)

## 📋 TODO - Testing

### Unit Tests
- [ ] Database layer tests
- [ ] API handler tests
- [ ] Authentication tests
- [ ] Session management tests

### Integration Tests
- [ ] Full setup flow test
- [ ] Login/logout flow test
- [ ] Token creation/usage test
- [ ] Admin operations test

### E2E Tests
- [ ] Browser automation tests
- [ ] Mobile responsive tests
- [ ] API client tests

## 📋 Future Enhancements

### Organizations (Optional)
- [ ] Organization creation
- [ ] Member management
- [ ] Organization tokens
- [ ] Team permissions
- [ ] Organization billing

### Email System
- [ ] SMTP configuration
- [ ] Email verification
- [ ] Password reset emails
- [ ] Notification emails
- [ ] Email templates

### Webhooks
- [ ] Webhook configuration
- [ ] Event triggers
- [ ] Retry logic
- [ ] Webhook logs

### Advanced Features
- [ ] IP whitelist/blacklist
- [ ] Geographic restrictions
- [ ] Custom themes
- [ ] Plugin system
- [ ] API versioning (v2, v3)

---

## 🎯 Priority Order

1. **Authentication & Sessions** (Critical)
2. **User Features** (High)
3. **Admin Features** (High)
4. **Database & Storage** (Medium)
5. **Build & Deploy** (Medium)
6. **API Enhancements** (Medium)
7. **Development Features** (Low)
8. **Monitoring** (Low)
9. **Testing** (Ongoing)
10. **Future Enhancements** (Optional)

---

*Last Updated: 2025-10-04*
*SPEC Version: Universal Server Template Specification v1.0*
