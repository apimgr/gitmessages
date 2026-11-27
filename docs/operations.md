# Operations

This guide covers routine maintenance for a running Git Messages server. Configuration lives in `server.yml`, `secrets.yml`, and `yaml.d/`; the service has no administration web panel.

## Startup output

When the server runs in a foreground terminal, it prints a width-aware startup banner with the version, mode, and configured public URL. Running Tor, I2P, and SMTP addresses appear when configured. A newly generated operator token appears once in the banner as the setup token. `--color` and `NO_COLOR` control banner styling; log records remain plain text. Containers, daemons, and headless runs use log output without the terminal banner. On first run, the server logs the path where it created `server.yml`.

## Backups

Create and verify a manual backup with the server binary:

```sh
gitmessages --maintenance backup
gitmessages --maintenance backup /secure/path/gitmessages.tar.gz
gitmessages --maintenance backup --include-ssl --include-data
gitmessages --maintenance backup test
```

Without a filename, the server creates a timestamped archive in its configured backup directory. Use `--include-ssl` to add the full SSL directory and `--include-data` to add the data directory. Every SQLite backup includes `server.yml`, `server.db`, existing configuration directories, and any referenced cache TLS CA/client-certificate files. Sensitive archives may contain credentials, owner-token material, encryption keys, reports, and private configuration; keep them access-controlled and outside public web roots.

Remote libSQL backup and restore are currently unavailable. The CLI stops with an explicit error instead of operating on a local SQLite file.

Disaster-recovery restore enforces the configured archive size, total expanded size, file count, and per-file size limits. It does not apply a compression-ratio limit. Archive imports are not available in this product.

For remote libSQL, `server.database.cache.enabled` maintains a local SQLite read cache at `server.database.cache.path`. The cache is refreshed on startup, after successful remote writes, and at `sync_interval`. When `offline_mode` is enabled, an unavailable remote can use only a cache refreshed within `max_age`; this mode is read-only. The cache contains a replica of database records and should be protected like the remote database. It is a performance/offline cache, not a backup.

Archives use `.tar.gz`; when `server.backup.encryption_password` is set, they use `.tar.gz.enc` and AES-256-GCM encryption. Compliance mode requires that password before backups can run. Store the password separately from the archive. `backup test [file]` verifies an archive without restoring it; when the file is omitted, the server selects the full backup with the latest UTC date in its filename, breaking date ties by file modification time. If no full backup exists, it selects the newer daily or hourly incremental by modification time. The encrypted manifest is not read to select an archive; an incremental is verified together with its base.

Restore an archive from the server host:

```sh
gitmessages --maintenance restore /path/to/gitmessages_backup.tar.gz
gitmessages --maintenance restore
```

When the file is omitted, the server selects the full backup with the latest UTC date in its filename, breaking date ties by file modification time, and asks you to type `RESTORE`. It does not select an incremental archive by default. Encrypted archives prompt for their backup password. Restore is destructive and requires the configured operator authorization when running as the service account; a root-run restore requires confirmation. A restore is checked before files are replaced. For an explicitly selected incremental archive, keep its full base archive in the same directory.

The built-in scheduler performs the daily full and incremental SQLite backup work. It also applies the configured retention policy and skips scheduled backups when the disk threshold is exceeded. Configure schedules, backup location, retention, encryption, and restore size limits as described in [Configuration](configuration.md#scheduler) and its backup settings. Scheduled backups are unavailable when the server uses remote libSQL.

## Scheduled maintenance

The scheduler runs inside the server process and stores task state in `server.db`. It resumes eligible missed jobs after restart within `server.scheduler.catch_up_window`. Do not run a separate cron job or system timer for application maintenance. Task schedules, retry settings, and time zone are configured under `server.scheduler` in `yaml.d/maintenance.yml`.

Scheduled work includes SSL renewal, GeoIP and security-feed updates, security-report cleanup, log rotation, disk and health checks, backups, and update checks. Tor and I2P health tasks are registered only when those features are active. Task failures are logged and exposed through the scheduler metrics described in [Internal metrics](metrics.md).

During database maintenance, writes return `503`. Reads continue while the database or an allowed fresh offline cache can answer. If neither can answer, only health endpoints remain available and other routes return `503`.

## Debug diagnostics

Unexpected handler panics produce a generic 500 response. The stack trace is written to the private application log, never returned to the browser or API client. If the themed error page cannot render, a minimal static fallback is returned.

Enable diagnostics with the persisted `server.mode: debug` setting or `gitmessages --mode debug`; both are explicit opt-ins saved to `server.yml`. `--debug` and `DEBUG=true` enable diagnostics for one process without changing the saved mode. `--debug=false` or `DEBUG=false` disables diagnostics for that process. Debug settings under `server.debug` control pprof, runtime handlers, query/cache logging, optional body previews, and profiling rates.

The optional top-level `output.emoji` setting controls startup-banner emoji. An explicit `true` or `false` overrides `NO_COLOR`; an unset value follows automatic terminal detection. `TERM=dumb` always disables emoji. `output.color` can explicitly enable or disable banner color, with `--color` taking precedence.

When enabled, every `/debug/*` endpoint requires the operator token as an `Authorization: Bearer …` header. The handlers return 404 when diagnostics are disabled. Available endpoints include `/debug/pprof/*`, `/debug/vars`, `/debug/config`, `/debug/routes`, `/debug/cache`, `/debug/db`, `/debug/scheduler`, `/debug/memory`, `/debug/goroutines`, and `/debug/error500`. These are root paths and are not part of the versioned API. The command line and expvar `cmdline` value expose only the binary name; configuration credentials and token-shaped log values are redacted.

Request body logging is off by default. If enabled with `server.debug.log_bodies`, previews are bounded by `max_body_log_size` (10 KB by default). Database query logs include operation, table, duration, and error category; query arguments are redacted. Protect access to debug logs and endpoints as operational data.

## Updates

Check for a newer server release without replacing the running binary:

```sh
gitmessages --update check
```

Install an available update with `gitmessages --update` or `gitmessages --update yes`. Select the release channel with `gitmessages --update branch stable`, `beta`, or `devel`. The server stores the selected channel in its configuration. A manual update check or installation follows the configured channel and does not require scheduled-update deferral.

Scheduled checks run daily. They notify the server operator by log and, when configured, email. Automatic installation is disabled by default; configure `server.update.auto_install: true` only when the service manager and installation permissions support the staged restart flow. The `server.update.defer_days` setting applies to scheduled stable and beta releases.

## Service and email checks

Use `gitmessages --service status` to inspect service state. The `--service` command also provides start, stop, restart, reload, install, uninstall, and disable operations; `gitmessages --service --help` lists its supported forms. Service installation and uninstall behavior is described in the [installation guide](installation.md#linux-systemd).

To validate configured notification templates and the SMTP relay, use `gitmessages email test --dry-run`. Send a test message with `gitmessages email test --to ADDRESS`. SMTP delivery is required for email notifications but not for serving the public catalog or API.
