Reconciled against a full line-by-line audit of AI.md (all 33 PARTs) vs actual
repo state. AI.md is ground truth; every item below is a verified code
deviation. Ordered critical-first within each PART grouping.

## [ ] CRITICAL: Delete src/admin/ entirely — violates AI.md PART 1 and IDEA.md
Read: AI.md PART 1 (~line 5359), IDEA.md "Non-goals" (lines 34-35)
AI.md PART 1 explicitly forbids an admin web UI and any runtime
config-mutation API ("There is no admin web UI, no runtime config mutation
via API... No endpoints that write to or mutate server configuration").
IDEA.md independently states "No admin web panel" and "No user accounts,
registration, or login of any kind" as non-goals. `src/admin/auth.go` (313
lines) and `src/admin/handlers.go` (405 lines) implement a full session-cookie
login system + 8 routes (`/admin`, `/admin/login`, `/admin/logout`,
`/admin/dashboard`, `/admin/settings`, `/api/v1/admin/status`,
`/api/v1/admin/config`, `/api/v1/admin/reload`) — this is orphaned code that
must be removed, not fixed. Config is server.yml-only per both specs.

## [ ] CRITICAL: Build src/server/ — no HTTP route/handler layer exists
Read: AI.md PART 3 structure checklist (line 44402), PART 14
No `src/server/` directory exists at all. All current routing lives ad hoc
in `src/main.go` via a bare `http.ServeMux` (AI.md PART 3 requires
`github.com/go-chi/chi/v5`, not present in go.mod). This blocks nearly every
other API/frontend gap below — REST types/templates endpoints, GraphQL,
Swagger, and the `/server/*` pages all need this layer first.

## [ ] CRITICAL: Build the commit-type registry — core product data is missing entirely
Read: IDEA.md "Data model & sensitivity" (lines 50-70)
`src/messages/data/messages.json` is a flat list of 5,096 unrelated
random/joke commit-message strings — NOT the commit type registry. Nothing
in `src/**/*.go` defines the 11 commit types (feat, fix, docs, style,
refactor, perf, test, chore, ci, build, revert) with
`type`/`emoji`/`description`/`example`/`breaking` fields, or message
templates (`name`/`message`/`category`). Add a proper data file + Go types
matching IDEA.md's schema. Required before any REST/GraphQL/web UI type
endpoint can serve real data.

## [ ] CRITICAL: Web frontend has no template layer at all
Read: AI.md PART 16 (line 20320+)
No `html/template` usage anywhere in the repo (`find -iname "*template*"`
returns nothing). HTML is built via raw `fmt.Sprintf` string literals
directly in src/main.go. No `templates/` dir, no CSS files, no dark/light/
auto theme CSS custom properties. Violates "Server-Side Processing... HTML
rendering: Server (Go templates)."

## [ ] CRITICAL: REST/Swagger/GraphQL trio missing — only a partial REST surface exists
Read: AI.md PART 14 (line 17674+), line 18653 ("ALL PROJECTS GET ALL THREE")
No `src/swagger/` or `src/graphql/` packages exist; no `/api/swagger`,
`/api/graphql` routes. API version is hardcoded literal `v1` instead of
`{api_version}`/`APIBasePath()`. Route naming violates "nouns not verbs"
(`/api/v1/random`, `POST /api/v1/reset`). No `{ok,data}`/`{ok,error,...}`
response envelope anywhere in main.go handlers.

## [ ] CRITICAL: Required /server/* pages and health schema missing
Read: AI.md PART 13 (line 17048+), PART 16 (line 19194+)
IDEA.md requires `/server/about`, `/server/help`, `/server/healthz`,
`/server/privacy`, `/server/terms` — none exist; only bare `/healthz` and
`/api/v1/healthz` (wrong namespace, AI.md wants `/server/healthz` +
`/api/{api_version}/server/healthz`). `handleHealthz` (main.go:335-341)
returns only `{"status","version"}` vs AI.md's required schema
(`project, status, pending_restart, version, go_version, build{...}, uptime,
mode, timestamp, features, checks{database,cache,disk,scheduler}, stats`).
`handleHealthzText` just writes literal `"OK"` instead of flattened
dot-notation fields.

## [ ] CRITICAL: Production mode leaks raw internal errors to clients
Read: AI.md PART 9 (~line 10500+), api-rules.md
`mode.GetErrorDetail(err)` and `mode.GetCacheHeaders()` (src/mode/mode.go)
are fully implemented but have ZERO call sites anywhere else in the repo —
dead code. `handleRandom`/`handleRandomText`/`handleMessages` (main.go
~366-414) call `err.Error()` directly into HTTP responses with no mode
check, and use ad hoc `{"success":false,"error":...}` shapes instead of
AI.md's `{ok,data}`/`{ok,error,message,details?}` envelope. This is a live
security-relevant regression, not just a spec gap — wire the existing
sanitization functions into every handler.

## [ ] CRITICAL: No security middleware chain at all
Read: AI.md PART 11 (line 13638+)
grep across src/ for RequestID/RateLimit/GeoIP/Blocklist/Allowlist/
PathSecurity/SecurityHeaders finds nothing except an unrelated CORS
middleware (main.go:288). The full required chain (URL normalize →
RequestID → PathSecurity → SecurityHeaders → Allowlist → Blocklist →
RateLimit → GeoIP → Auth → Logging) does not exist. No CSP/HSTS/
Permissions-Policy/COOP/COEP/CORP headers. No audit.log/security.log/
access.log/error.log structured logging (no `slog` usage anywhere).

## [ ] CRITICAL: No database layer at all
Read: AI.md PART 10 (line 13232+)
grep for `database/sql`/`modernc.org/sqlite` across src/ returns zero
matches. Note: this factually contradicts AI.md PART 10, but also directly
conflicts with IDEA.md's explicit no-DB design ("All convention data is
embedded in the binary... No PII stored"). This is a genuine AI.md vs
IDEA.md conflict on a structural requirement (not just admin/auth) —
needs a decision on whether server.db (secrets, sessions, scheduler state,
audit log per PART 10/11) is in scope for this project before implementing.

## [ ] CRITICAL: Scheduler package exists but is never wired in — dead code
Read: AI.md PART 18 (line 26942+)
`src/scheduler/scheduler.go` is never imported/instantiated anywhere outside
itself — no task ever runs. None of the 11 required built-in tasks
(ssl_renewal, geoip_update, blocklist_update, cve_update, update_check,
token_cleanup, log_rotation, backup_daily, backup_hourly, healthcheck_self,
tor_health) are registered. `AddTask` only accepts `time.Duration`, not cron
expressions (`0 3 * * *`) or `@hourly`-style keywords. No persistent state in
server.db, no catch-up-on-startup logic, no retry policy, no graceful
shutdown wait for in-flight tasks, no scheduler CLI (list/show/run/enable/
disable/history).

## [ ] CRITICAL: Metrics endpoint configured but not implemented
Read: AI.md PART 20 (line 27476+)
`/metrics` is a config default string only (config.go:103) — no route
registration, no handler, no `prometheus/client_golang` in go.mod. None of
the required app/HTTP/DB/auth/scheduler/system/runtime/Tor/rate-limit metric
families exist.

## [ ] CRITICAL: Privilege escalation / service-user model entirely missing
Read: AI.md PART 23 (line 30036+)
No sudo/su/pkexec/doas/UAC detection anywhere. `service.go Install()`
unconditionally writes to root-owned paths with no root check, no user-mode
service fallback. No service-user/group creation (UID/GID matching, 200-899
range). systemd unit hardcodes `User=root Group=root`, contradicting the
privilege-drop-after-binding model AI.md requires throughout.

## [ ] CRITICAL: OpenRC and SysVinit service managers missing
Read: AI.md PART 24 (line 30652+)
Only systemd, runit, launchd, Windows, generic BSD rc.d are implemented in
service.go's `ServiceType` enum. `DetectServiceManager` falls back to
`ServiceUnknown` on non-systemd/non-runit Linux instead of detecting these
two required init systems.

## [ ] CRITICAL: Makefile is not the AI.md PART 25 Makefile — builds directly on host
Read: AI.md PART 25 (line 30965+), makefile-rules.md
13 ad hoc targets instead of the required 6 (`dev, local, build, test,
release, docker`) — `local` is missing entirely. Every target (`build`,
`test`, `dev`, etc.) runs `go build`/`go test` directly on the host with zero
Docker usage — direct violation of makefile-rules.md. No
`GOFLAGS=-buildvcs=false`, no coverage gate, no `casjaysdev/go:latest`
anywhere in the file. `test` target references `./tests/unit/...` and
`./tests/integration/...` which don't exist.

## [ ] CRITICAL: Zero Go unit tests exist anywhere in src/
Read: AI.md PART 28 (line 35457+)
`find src -name "*_test.go"` returns zero results across all 8 packages
(admin, mode, messages, ssl, config, service, paths, scheduler). AI.md
requires a `*_test.go` per package with ≥60% coverage enforced in CI,
written alongside feature code. This is distinct from (and in addition to)
the already-known missing tests/run_tests.sh etc.

## [ ] CRITICAL: docker/Dockerfile violates multiple non-negotiable rules
Read: AI.md PART 26 (line 31751+)
`ENV MODE=development` is baked into the image (must never be set — binary
defaults to production; also violates binary-rules.md). No `USER` directive
— container runs as root (must be non-root). Builder is `golang:alpine`
instead of mandated `casjaysdev/go:latest`. Static `LABEL` blocks baked in
instead of applied via CI `--label`/`annotations:` at build time. Dockerfile
does its own `mkdir -p /config /data/...` (binary must own all directory
creation, not the Dockerfile). `git` missing from installed packages.

## [ ] CRITICAL: entrypoint.sh violates its own explicit "does NOT" list
Read: AI.md PART 26 (line 32066-32147)
`setup_directories()` mkdirs/chowns and `start_tor()` fully manages Tor
itself — AI.md explicitly forbids the entrypoint from creating directories,
setting permissions, or managing Tor. `start_app()` backgrounds the binary
with `&` and never `exec`s it, breaking PID-1 signal delivery — this is the
exact anti-pattern AI.md calls out by name.

## [ ] HIGH: PART 4 OS-specific paths — broader than the already-known macOS/container gaps
Read: AI.md PART 4 (line 6642+)
`src/paths/paths.go`'s `Directories` struct has no `Cache` field at all on
any OS (macOS should be `/Library/Caches`, Linux `~/.cache`, etc). BSD is
lumped into the Linux `default:` branch with wrong root paths (should be
`/usr/local/etc`, `/var/db`, `/var/backups`). Windows paths only cover
Config/Data/Logs — no PID/SSL/Security/DB paths anywhere. Linux user log
path is wrong (`~/.local/share/.../logs` instead of `~/.local/log/...`).
`GetBackupDir()` unconditionally returns `/mnt/Backups/{org}/{project}`
regardless of OS/privilege/container status. No PID file, SSL dir, Security
dir, or DB dir logic exists at all for any OS.

## [ ] HIGH: config.go missing large sections of the required schema
Read: AI.md PART 5 (line 6838+)
No `config.ParseBool()`/`IsTruthy()` (required for ALL boolean parsing
project-wide — mode.go uses raw string compare instead). No `IsDebug()`
method or `server.debug:` config block. No `server.database` section. No
`server.maintenance` section (self_healing, cleanup). No random port
auto-selection (64000-64999) on first run found in config.go. Missing
entirely from schema: `server.baseurl`, `server.limits.*`,
`server.compression.*`, `server.trusted_proxies.*`, `tor.*`,
`server.rate_limit.*`, `server.i18n.*`, `server.contact.*`,
`server.tracking.*`, `server.privacy.*`, `server.cache.*`, plus all
`web.headers/csp/cors/hsts/permissions_policy` fields — roughly 15% of the
specified config schema currently exists.

## [ ] HIGH: mode.go missing the full Debug-flag model (broader than tracked)
Read: AI.md PART 6 (line 8633+)
Beyond the already-tracked missing devel/debug shortcuts: no `SetAppMode`,
`SetDebugEnabled`, `IsDebugEnabled`, `FromEnv`, `GetAppModeString` (with
`[debugging]` suffix), or profiling-rate wiring
(`runtime.SetBlockProfileRate`/`SetMutexProfileFraction`) exist.
`ShouldEnableProfiling()`/`ShouldShowDebugEndpoints()` currently key off
`IsDevelopment()` alone, meaning debug endpoints activate automatically in
dev mode with no `--debug` flag — directly contradicts AI.md ("Debug
endpoints: Disabled (use --debug to enable)" even in Development). No
`/debug/*` routes are wired anywhere (pprof, /debug/vars, /debug/config,
/debug/routes, /debug/cache, /debug/db, /debug/scheduler all absent). No
startup mode banner (`🔒 Running in mode: production [debugging]`).

## [ ] HIGH: src/main.go missing most of PART 8's required CLI flags
Read: AI.md PART 8 (line 9988+)
Only `--port, --address, --config, --version, --status, --help, --mode,
--update, --service, --maintenance` exist. Missing entirely: `--data,
--cache, --log, --backup (standalone), --pid, --baseurl, --daemon, --debug,
--color, --lang, --shell {completions,init}`. `--maintenance` subcommand only
implements `backup, restore, update, mode, setup` — missing `pgp, token,
data, compliance, --help`.

## [ ] HIGH: PART 21 Backup & Restore is a naive tar wrapper, not the spec
Read: AI.md PART 21 (line 28886+)
`maintenanceBackup`/`maintenanceRestore` (main.go:779-797) just tar the
config dir. Missing: itemized server.db backup, `--include-ssl`/
`--include-data` flags, manifest.json w/ checksum, AES-256-GCM+Argon2id
encryption, retention policy, backup verification, `backup_daily` scheduler
task. `maintenanceRestore` runs `tar -xzf <file> -C /` with no authorization
tiering and no pre-restore verification — a real destructive-operation
safety bug given attacker- or corruption-influenced archives. Filenames
don't match the required `{project}_backup_YYYY-MM-DD_HHMMSS.tar.gz[.enc]`
pattern.

## [ ] HIGH: PART 22 self-update mechanism is a stub
Read: AI.md PART 22 (line 29436+)
`--update`/`--maintenance update` CLI surface exists and wires `branch`
correctly, but `maintenanceUpdate()`/`handleUpdateCommand` just print
"Update feature not yet implemented." No `updater` package, no GitHub
Releases check, no checksum verification, no per-OS binary replacement, no
`update_check` scheduler task.

## [ ] HIGH: SSL/TLS package exists but is never wired into the server
Read: AI.md PART 15 (line 19370+)
`src/ssl/ssl.go` (218 lines) is never imported/called from main.go — no
`ListenAndServeTLS` anywhere. The server has zero HTTPS capability despite
the package existing. TLS-ALPN-01/DNS-01 challenges and the lego
multi-DNS-provider requirement are unimplemented (only http-01 via plain
autocert). Certificate lookup only checks one of the four required path
tiers. No renewal logic (daily check, auto-renew 7 days before expiry).

## [ ] HIGH: Email/notification, GeoIP, and metrics config are placeholders only
Read: AI.md PART 17 (line 26377+), PART 19 (line 27366+), PART 20 (line 27476+)
No `src/server/template/email/` templates. `NotificationsConfig` struct has
no SMTP/Email fields at all — zero SMTP code anywhere. No GeoIP config
struct or package (`maxminddb-golang` not in go.mod). `MetricsConfig`
missing `include_runtime`, `token`, and bucket arrays.

## [ ] HIGH: I18N and Tor hidden service are complete, total gaps
Read: AI.md PART 30 (line 37928+), PART 31 (line 39329+)
No `src/common/i18n/` package, no locale files, no translation functions, no
`--lang` flag — every user-facing string is hardcoded English. No
`TorService`/`TorManager`, no torrc generation, no `server.tor` config, and
`github.com/cretz/bine` is not even declared in go.mod despite being a
mandatory dependency.

## [ ] HIGH: docker-compose files (both root and docker/) missing required structure
Read: AI.md PART 26 (line 32188+)
Beyond the already-tracked duplicate-file conflict: neither compose file has
`x-logging:`, `pull_policy: always`, `healthcheck:`, or `hostname:`.
Production compose explicitly sets `MODE=production` (must set neither MODE
nor DEBUG). `environment:` uses list style instead of required map style.
Volumes use `./rootfs/config/...` instead of `./volumes/config:/config:z`
(wrong host dir, missing `:z` SELinux label). No cache (Valkey) service.

## [ ] HIGH: .github/ needs ci.yml as a required file, not just the optional four
Read: AI.md PART 27 (line 32977+)
Already tracked: release.yml/beta.yml/daily.yml/docker.yml are missing.
Additionally, `ci.yml` is mandatory (not optional like the other three) and
is also missing — flag it explicitly when authoring workflows.

## [ ] MEDIUM: .gitignore / .claude structure deviations
Read: AI.md PART 3 (line 5776+, esp. 6165-6170 and 5931-5938)
`.gitignore` is missing the required AI-tool-directories block (`.claude/`,
`.cursor/`, `.aider/`, `.ai/`, `.windsurf/`) — yet `.claude/rules/*.md` (13
files) are currently tracked in git, contradicting AI.md's stated intent
that `.claude/` is regenerated, not committed. `.claude/CLAUDE.md`,
`.claude/settings.json`, `.claude/.mcp.json` (unconditional per AI.md's
tree) don't exist on disk. `docker/Dockerfile.dev`,
`docker/docker-compose.dev.yml`, `docker/docker-compose.test.yml` are
missing (required, not optional). Root `Jenkinsfile` is missing. `chi/v5` is
not in go.mod despite being the mandated router (plain `http.ServeMux` used
instead).

## [ ] MEDIUM: PART 24 service files diverge from AI.md's exact per-OS templates
Read: AI.md PART 24 (line 30652+)
runit log script uses `svlogd -tt ./main` instead of the required absolute
`/var/log/{org}/{name}` path. launchd plist path uses an invented
`com.%s.%s.plist` naming instead of the defined `{plist_name}`, and its log
paths (`/Library/Logs/...`) don't match AI.md's `/var/log/{org}/{name}/`
requirement. Windows service uses raw `sc.exe create` shell-out instead of
`golang.org/x/sys/windows/svc` + Virtual Service Account. No `Disable()`
(stop+disable, keep data) distinct from `Uninstall()`; `Uninstall()` doesn't
clean up config/data/cache/log/backup dirs or the service user/group, and
has no confirmation prompt.

## [ ] MEDIUM: PWA, sitemap, and URL-normalization gaps in the web layer
Read: AI.md PART 16 (line 22020+, 24574+, 20430+)
Only `/manifest.json` exists — no service worker, no offline cache. No
`/sitemap.xml` route at all (mandatory for all projects). No trailing-slash
301 canonicalization middleware.

## [ ] MEDIUM: Argon2id plaintext-password fallback in src/admin/auth.go
Read: AI.md PART 11 (line 15464+)
Only relevant if src/admin/ is kept rather than deleted per the item above —
`auth.go:83-89` falls back to constant-time comparison of raw plaintext if
the stored hash doesn't start with `$argon2`, with no forced migration path,
undercutting the "always hashed" guarantee.

## [ ] LOW: docs/, mkdocs.yml, .readthedocs.yaml — confirm exact required file set
Read: AI.md PART 29 (line 37134+)
Already tracked as missing; confirms exact list: index.md, installation.md,
configuration.md, api.md, cli.md (required here since a CLI is mandated),
security.md, integrations.md, development.md, stylesheets/ (optional),
requirements.txt (pinned mkdocs-material>=9.5.0 etc), plus root-level
mkdocs.yml and .readthedocs.yaml (the ReadTheDocs build entrypoint — its
absence blocks hosting entirely, not just the docs content).

## [ ] LOW: src/client/ required internal layout, beyond bare existence
Read: AI.md PART 32 (line 40687+)
Already tracked as missing entirely; confirms required structure once
started: src/client/init.go, src/client/paths/paths.go,
src/client/setup/wizard.go (bubbletea TUI), src/client/tui/{styles,layout}.go
(lipgloss + responsive SizeMode table), src/client/cli/output.go,
src/client/gui/{gui,gui_linux,gui_darwin,gui_windows,gui_bsd}.go (native
GTK4/Cocoa/Win32 — explicitly not Electron/webview), cli.yml at
~/.config/{org}/{name}/cli.yml (0600 perms), auto mode-detection
(GUI/TUI/CLI/headless, no --tui/--cli/--gui flags). None of bubbletea/
lipgloss/gotk4 are in go.mod yet.

## [ ] Reconcile duplicate docker-compose.yml — root-level file conflicts with docker/docker-compose.yml (port 64080 vs 64580, different volume paths, stray DB_TYPE=sqlite contradicting IDEA.md's no-DB design); only docker/docker-compose.yml is spec-mandated
Read: AI.md PART 26

## [ ] Populate .github/ policy files (CODEOWNERS, SECURITY.md, ISSUE_TEMPLATE/*, PULL_REQUEST_TEMPLATE) — .github/ currently empty
Read: AI.md PART 1

## [ ] Implement tests/run_tests.sh, tests/docker.sh, tests/incus.sh
Read: AI.md PART 28
