# Configuration

This reference documents the server and CLI settings supported by Git Messages. The implementation specification is available in the [project repository](https://github.com/apimgr/gitmessages/blob/main/AI.md).

## Configuration files and precedence

The server reads `server.yml` from the platform configuration directory. Select another directory with `--config DIR` or `CONFIG_DIR`. Settings merge in this order: built-in defaults, `server.yml`, regular `yaml.d/*.yml` and `yaml.d/*.yaml` files sorted by filename, then `secrets.yml`. Maps merge recursively; later scalar and list values replace earlier values. CLI flags and environment overrides take precedence for the settings they support.

`server.yml` is intended for node-local settings. Area files in `yaml.d/` hold web, security, contact, notification, overlay, and other settings. The loader migrates recognized legacy sections and secrets from a combined `server.yml`; it keeps a private `server.yml.bak` during migration. Store credentials in `secrets.yml`. Configuration files are owner-only and `yaml.d/` is private.

| Platform/runtime | Default configuration directory |
|---|---|
| Linux/Unix as root | `/etc/apimgr/gitmessages` |
| Linux/Unix as a regular user | `$HOME/.config/apimgr/gitmessages` |
| macOS as root | `/Library/Application Support/apimgr/gitmessages` |
| macOS as a regular user | `$HOME/Library/Application Support/apimgr/gitmessages` |
| Windows | `%APPDATA%\apimgr\gitmessages` |
| Container | `/config/gitmessages` |

Use this layout for a drop-in:

```text
{config_dir}/
├── server.yml
├── secrets.yml
└── yaml.d/
    ├── contact.yml
    ├── metrics.yml
    ├── notifications.yml
    ├── security.yml
    ├── tor.yml
    └── i2p.yml
```

Area names are conventions; the loader accepts supported settings in any merged YAML file. Do not put credentials in a drop-in. Supported legacy keys are migrated before validation; any remaining unknown key causes configuration loading to fail.

## Server settings

| YAML key | Accepted values and behavior |
|---|---|
| `server.port` | A TCP port from 1–65535, or a comma-separated `HTTP,HTTPS` pair such as `80,443`. Defaults to `PORT`, port 80 in a detected container, or an available port in 64000–64999 on a host. `--port` overrides the environment and file. |
| `server.ssl.enabled` | Enables TLS for a single configured port. In dual-port mode, the second port is HTTPS. Port 443 is HTTPS-only by default. |
| `server.ssl.cert`, `server.ssl.key` | Optional paths to an explicit certificate and private key; both must be provided together. Otherwise, the server searches the documented system and config SSL directories. |
| `server.ssl.min_version` | Minimum TLS version: `TLS1.2` (default) or `TLS1.3`. |
| `server.ssl.letsencrypt.enabled` | Enables automatic Let's Encrypt certificates for configured `server.fqdn` or `DOMAIN`. |
| `server.ssl.letsencrypt.email` | Contact email supplied for ACME registration. |
| `server.ssl.letsencrypt.challenge` | ACME challenge type: `http-01`, `tls-alpn-01`, or `dns-01`. |
| `server.ssl.letsencrypt.staging` | Requests certificates from the ACME staging endpoint for testing. |
| `server.ssl.letsencrypt.dns_provider` | Lego DNS provider name used for DNS-01 challenges. |
| `server.ssl.letsencrypt.dns_credentials` | Flat map of provider environment variable names to values; keep credentials in `secrets.yml`. |
| `server.address` | IP address to bind. Defaults to `LISTEN`, then `[::]`. `--address` has the highest priority. |
| `server.fqdn` | Configured public host name; used by URL and MCP origin handling. |
| `server.api_version` | API route version prefix matching `v` followed by digits; defaults to `v1`. Changing it requires a server restart. The active version is advertised at `GET /api/autodiscover`. |
| `server.baseurl` | Optional path prefix for public URLs, such as `/messages`. |
| `server.mode` | `production`, `development`, or `debug`; `--mode` and `MODE` can override it. |
| `server.debug.pprof`, `runtime_endpoints` | Control pprof and runtime diagnostic handlers after debug has been explicitly enabled for the process. |
| `server.debug.log_queries`, `log_cache` | Enable database and cache diagnostic logs when `--debug` or `DEBUG` is active. Query values are never logged. |
| `server.debug.log_bodies`, `max_body_log_size` | Opt into bounded request/response body previews; defaults to off and `10KB`. Credential-shaped values are redacted. |
| `server.debug.block_profile_rate`, `mutex_profile_fraction` | Runtime profiling rates used only while debug diagnostics are enabled; zero disables the corresponding profile. |
| `server.token` | Operator bearer token. Keep it in `secrets.yml`; the server creates one when absent. |
| `server.trusted_proxies.additional` | List of proxy IP addresses or CIDRs permitted to supply forwarded headers. Private and loopback peers are trusted automatically. |
| `server.trusted_proxies.client_ip_header` | Configures the client-IP header selection policy. |
| `server.url_detection.learning` | Enables request-driven domain learning; defaults to `true`. |
| `server.url_detection.min_samples` | Minimum observations before inferring a wildcard domain; defaults to `3`. |
| `server.url_detection.sample_window` | Duration used to analyze domain patterns; defaults to `5m`. |
| `server.url_detection.log_changes` | Logs inferred public URL changes; defaults to `true`. |
| `server.url_detection.live_reload` | Applies learned URL changes without a restart; defaults to `true`. |
| `server.cors.allowed_origins` | `*` or a list of HTTP/HTTPS origin URLs. A wildcard does not authorize MCP origins. |
| `server.healthz.root.enabled` | Enables the direct `/healthz` alias for `/server/healthz`; defaults to `false`. `/api/healthz` is an unversioned alias and is always registered. |
| `server.rate_limit.read.{requests,window}` | Read request limit and window in seconds. |
| `server.rate_limit.write.{requests,window}` | Write request limit and window in seconds. |
| `server.rate_limit.health.{requests,window}` | Health endpoint limit and window in seconds. |
| `server.rate_limit.strict.{requests,window}` | Sensitive-action limit and window in seconds. |
| `server.rate_limit.auth_failures.{requests,window}` | Failed-authentication limit and window in seconds. |
| `server.rate_limit.global_burst` | Maximum global burst allowance. |
| `server.update.branch` | `stable`, `beta`, or `devel` update channel. |
| `server.update.auto_install` | Whether eligible updates are installed automatically. |
| `server.update.defer_days` | Automatic update delay from 0–365 days. |
| `server.update.cli_min_version` | Minimum CLI version advertised to clients. |
| `server.logs.access_format` | Access log format; defaults to `apache`. |
| `server.logs.level` | Log level; empty follows the server mode (`info` in production, `debug` in development or debug mode). |

## Database and cache connections

The default database is local SQLite at `server.db` in the configured database directory. Remote libSQL/Turso is selected with `server.database.driver: libsql` and a `libsql://` URL, or an `https://` URL with `server.database.token`. SQLite accepts a file path or `file:` URL. The URL scheme selects the driver and overrides the separate driver field. Credential-bearing URLs and the separate token belong in `secrets.yml`; connection URLs are redacted in diagnostics.

`DATABASE_URL`, `DATABASE_DRIVER`, and `CACHE_URL` seed the saved settings on first run only. A URL with credentials is saved in `secrets.yml`; a URL without credentials is saved in `server.yml`. On later starts these variables are ignored, and a mismatch is reported by variable name. `${NAME}` and `${NAME:-default}` references in configuration values are expanded after files are merged and are not written back.

| YAML key | Default and behavior |
|---|---|
| `server.database.driver` | `sqlite`; accepts `sqlite`, `sqlite2`, `sqlite3`, `libsql`, or `turso`. |
| `server.database.url` | Empty uses the default `server.db`; remote libSQL requires a URL. Accepted remote schemes are `libsql://` and `https://`. |
| `server.database.token` | Optional libSQL auth token kept in `secrets.yml`; an `authToken` URL option takes precedence. |
| `server.database.pool.max_open` | `5`; maximum open database connections. For SQLite this sizes the read pool only; the write pool stays at one connection. |
| `server.database.pool.max_idle` | `2`; maximum idle connections, capped at `max_open`. |
| `server.database.pool.max_lifetime` | `5m`; maximum connection lifetime. SQLite writer lifetime remains unlimited by the concurrency contract. |
| `server.database.pool.max_idle_time` | `1m`; maximum idle duration before a pooled connection closes. |
| `server.database.cache.enabled` | `false`; enables the local SQLite cache for remote libSQL/Turso. |
| `server.database.cache.path` | Required when enabled; path to the private local SQLite cache file. Relative paths are resolved beside the configured database file. |
| `server.database.cache.sync_interval` | `30s`; interval for refreshing the local cache from remote libSQL. |
| `server.database.cache.offline_mode` | `true`; permits read-only service from a cache that is no older than `max_age` when remote libSQL is unavailable. |
| `server.database.cache.max_age` | `1h`; older cache contents force a remote read and are not used as an offline fallback. |
| `server.cache.type` | `memory`; accepts `none`, `memory`, `valkey`, or `redis`. Valkey/Redis counters support rate limits across restarts and instances. |
| `server.cache.url` | Optional `valkey://`, `valkeys://`, `redis://`, or `rediss://` connection URL. A saved URL takes precedence over the host, port, username, password, and database fields. |
| `server.cache.host`, `port`, `username`, `password`, `db` | Separate connection fields; default host `localhost`, port `6379`, database `0`. Keep passwords in `secrets.yml`. |
| `server.cache.tls`, `tls_skip_verify` | TLS is off unless requested by the URL scheme, URL `tls` option, or this field. TLS defaults to skipping certificate verification; set `tls_skip_verify: false` to verify the certificate and host. |
| `server.cache.tls_ca`, `tls_cert`, `tls_key` | Optional CA and client certificate files inside `{config_dir}/ssl/`. Configure both client certificate and key together. |
| `server.cache.pool_size`, `min_idle`, `timeout` | Redis/Valkey connection pool controls; defaults are `10`, `2`, and `5s`. |
| `server.cache.prefix`, `ttl` | Counter key prefix and default expiration; defaults are `gitmessages:` and `1h`. Explicit counter windows, including the one-minute rate-limit window, take precedence over this fallback TTL. |

Passwords may be typed directly in URL userinfo; valid `%XX` sequences are decoded. A cache URL may use a password-only userinfo such as `valkey://:{password}@host:6379/0`. URL query credentials (`password`, `pwd`, `token`, and `authToken`) are treated as secrets. Use the separate password/token fields when a literal password contains an ambiguous `%XX` sequence.

## Internationalization

Configure the server's default content language and enabled browser-language choices under `server.i18n`:

```yaml
server:
  i18n:
    default_language: en
    available_languages: [en, es, fr, de, it, ar]
```

The embedded locale registry includes English, Spanish, French, German, Italian, Arabic, Chinese, and Japanese. Unsupported configured languages are discarded; operator email uses `default_language` because it has no request-language context.

## Security feeds and GeoIP

Security and GeoIP settings are stored in the security configuration area. The public catalog is independent of these operational datasets.

| YAML key | Behavior |
|---|---|
| `server.security.allowlist` | Trusted client IP addresses and CIDRs that bypass blocklist, rate-limit, and GeoIP checks; authentication and authorization still apply. |
| `server.security.blocked_ips` | Explicit IP addresses and CIDRs rejected before rate limiting, with an optional description (`(no description)` when omitted). |
| `server.csrf` | Frontend CSRF protection; enabled by default, with configurable cookie/header names, token length, Secure policy, and frontend-only exempt paths. |
| `server.security.reports.retention_days` | Days to retain closed security reports; defaults to `365`. Open reports are never purged. |
| `server.security.blocklists.enabled` | Enables the built-in FireHOL list and configured extra sources. |
| `server.security.blocklists.extra_sources[]` | Extra HTTPS IP or domain sources with an ID, type, URL, and optional `enabled` flag. |
| `server.geoip.enabled` | Enables GeoIP checks; defaults to `true`, with no countries blocked by default. |
| `server.geoip.deny_countries` | ISO country codes denied after blocklists and rate limiting. |
| `server.geoip.allow_countries` | Country allow policy; it does not bypass authentication. |
| `server.geoip.presets` | Named country-code lists used by country policy. |
| `data.security_dir` | Optional operational security-data directory; `{data_dir}` expands to the configured data path. |
| `data.cve.source` | NVD API endpoint used for CVE updates. |
| `data.cve.api_key` | Optional NVD API key; keep it in `secrets.yml`. |
| `data.cve.filter_by_cpe` | Enables filtering NVD results by configured CPE data. |
| `data.trivy.enabled` | Enables Trivy vulnerability database updates. |
| `data.trivy.source` | OCI image reference for the Trivy database. |

GeoIP lookups fail open when data is unavailable, and private, loopback, and link-local addresses are not country-blocked. See [Security](security.md) for request-check ordering and feed behavior.

## Scheduler

The built-in scheduler uses persistent task state in the server database and runs while the server is running. Time zone and task settings are stored in `yaml.d/maintenance.yml`.

| YAML key | Behavior |
|---|---|
| `server.scheduler.timezone` | IANA time zone for scheduled jobs; defaults to `America/New_York`. |
| `server.scheduler.catch_up_window` | Maximum delay for running a missed task after startup; defaults to `1h`. |
| `server.scheduler.tasks.<task>.enabled` | Enables or disables an allowed task. Critical tasks cannot be disabled. |
| `server.scheduler.tasks.<task>.schedule` | Five-field cron expression or supported `@every` interval. |
| `server.scheduler.tasks.<task>.retry_on_fail` | Retries failed tasks with bounded backoff. |
| `server.scheduler.tasks.<task>.retry_delay` | Base delay between retries. |

Built-in task IDs and default schedules follow the AI.md scheduler specification. Tor and I2P health tasks are registered only when their corresponding network mode is active.

## CLI release distribution

Server-side CLI distribution settings belong in the `cli` section of `yaml.d/maintenance.yml`. The default binary directory is `{data_dir}/cli`; operators provision the release binaries and the `version.txt` and `sha256.txt` metadata there. The server advertises only files whose recomputed SHA-256 matches the metadata. Downloads are public by default; setting `require_auth` to `true` requires a valid Bearer token.

```yaml
cli:
  binary_download:
    dir: "{data_dir}/cli"
    require_auth: false
```

`server.update.cli_min_version` sets the minimum CLI version required for API requests. The client's `update.auto`, `update.check_interval`, and `update.channel` settings live in its user-only `cli.yml`, not on the server.

## Backup and restore settings

Backup policy is stored in `yaml.d/maintenance.yml`; compliance settings are stored in `yaml.d/privacy.yml` under the `server.compliance.*` keys. The backup encryption password belongs only in `secrets.yml`.

| YAML key | Default | Behavior |
|---|---:|---|
| `server.backup.encryption.enabled` | `false` | Enables AES-256-GCM archive encryption when a password is configured. |
| `server.backup.encryption_password` | unset | Argon2id key material; never expose it through APIs or logs. |
| `server.backup.retention.max_backups` | `1` | Number of daily full backups retained. |
| `server.backup.retention.keep_weekly` | `0` | Additional Sunday full backups retained. |
| `server.backup.retention.keep_monthly` | `0` | Additional first-of-month full backups retained. |
| `server.backup.retention.keep_yearly` | `0` | Additional January 1 full backups retained. |
| `server.backup.retention.max_total_size` | `10%` | Maximum backup storage as a volume percentage or byte size. |
| `server.backup.disk_threshold` | `90` | Disk-usage percentage above which a scheduled backup is skipped. |
| `server.restore.max_archive_size` | `1G` | Maximum archive size accepted for restore. |
| `server.restore.max_expanded_size` | `4G` | Maximum combined extracted size. |
| `server.restore.max_files` | `10000` | Maximum number of archive entries. |
| `server.restore.max_file_size` | `512M` | Maximum size of one extracted file. |
| `server.restore.max_ratio` | `100` | Maximum expanded-to-compressed size ratio. |
| `server.compliance.gdpr` | `false` | Enables GDPR-specific compliance behavior; any enabled standard requires encrypted backups. |
| `server.compliance.ccpa` | `false` | Enables CCPA-specific compliance behavior. |
| `server.compliance.hipaa` | `false` | Enables HIPAA-specific compliance behavior. |
| `server.compliance.soc2` | `false` | Enables SOC 2-specific compliance behavior. |
| `server.compliance.pci_dss` | `false` | Enables PCI DSS-specific compliance behavior. |
| `server.compliance.iso27001` | `false` | Enables ISO 27001-specific compliance behavior. |
| `server.compliance.fedramp` | `false` | Enables FedRAMP-specific compliance behavior. |
| `server.compliance.lgpd` | `false` | Enables LGPD-specific compliance behavior. |
| `server.compliance.pipeda` | `false` | Enables PIPEDA-specific compliance behavior. |
| `server.compliance.appi` | `false` | Enables APPI-specific compliance behavior. |
| `server.compliance.pdpa` | `false` | Enables PDPA-specific compliance behavior. |

All standards are disabled by default and configured independently. If any is enabled, backups require `server.backup.encryption_password` in `secrets.yml`. Run `gitmessages --maintenance compliance report` for aggregate compliance status and `gitmessages --maintenance data export|delete --owner <token-prefix>` for owner data requests.

## Web and browser settings

| YAML key | Accepted values and behavior |
|---|---|
| `web.ui.theme` | Default `dark`, `light`, or `auto` theme; a visitor's `theme` cookie takes precedence. |
| `web.ui.accent` | UI accent color. |
| `web.robots.allow` | Paths listed as allowed in generated `robots.txt`. |
| `web.robots.deny` | Paths listed as disallowed in generated `robots.txt`. |
| `web.security.contact` | Optional security contact value. |
| `web.security.policy` | Security policy text/configuration value. |
| `web.security.report_url` | External security report destination. |
| `web.security.publish_pgp_key` | Defaults to `true`; publishes a valid configured public PGP key at `/.well-known/pgp-key.asc`, adds its `Encryption:` URL and fingerprint comment to `security.txt`, and exposes its URL in security metadata. `false` disables public publication while retaining the local key. |
| `web.security.keyservers` | Keyserver host names receiving the public key on generation, rotation, import, or `--maintenance pgp publish`; transient failures retry through the scheduled `keyserver_retry` task. |

`robots.txt` rules must start with `/`; invalid and duplicate rules are ignored. When no valid allow rule remains, `/` is allowed. Deny rules take precedence over allow rules, and the sitemap uses the same rules when selecting pages. Defaults allow `/` and `/api`, and deny `/debug`.

| YAML key | Default | Behavior |
|---|---:|---|
| `server.seo.sitemap.max_urls` | `50000` | Maximum sitemap URLs; values outside 1–50000 use 50000. |
| `server.seo.sitemap.include_images` | `false` | Accepted setting; currently has no effect because the sitemap contains page URLs only. |
| `server.seo.feed.max_items` | `50` | Accepted setting; currently has no effect because Git Messages has no public item resource and both feeds are empty. |
| `server.seo.feed.title` | empty | Optional feed title; defaults to `server.branding.title`, then `gitmessages`. |

The server always serves `/robots.txt`, `/sitemap.xml`, `/feed.xml` (Atom), and `/rss.xml` (RSS). The sitemap contains only the configured public HTML page list and omits any page denied by the crawler rules; it never lists API routes, feeds, or `/server/contact`. Feed pages are valid but empty because the product has no public per-item pages and creation timestamps. Feed responses have five-minute public caching and ETag/Last-Modified validators. `robots.txt` and sitemap/feed URLs are generated for the current request host and scheme, so configure trusted proxies before forwarding external host, scheme, port, or prefix headers. See [Browser application](browser.md) and [Integrations](integrations.md) for route behavior.

The server also generates `/.well-known/security.txt`, `/.well-known/llms.txt`, and `/llms.txt`. See [Security](security.md) and [Integrations](integrations.md) for public discovery and reporting behavior.

### Branding and page metadata

Branding and SEO settings belong in `{config_dir}/yaml.d/branding.yml`; the dotted keys below remain under `server.branding` and `server.seo` in the merged configuration.

| YAML key | Default | Behavior |
|---|---|---|
| `server.branding.title` | `Git Messages` | Site name used in page titles, the header, email sender name, manifest, API discovery, and OpenAPI title. |
| `server.branding.tagline` | empty | Homepage hero text and fallback page description. |
| `server.branding.description` | empty | Site description used in page metadata; falls back to tagline, localized about text, then `gitmessages`. |
| `server.branding.logo` | empty | Header logo. Accepts an HTTPS URL, supported image data URI, or a filename under `{config_dir}/assets/`; invalid or unavailable sources use the embedded logo. |
| `server.branding.favicon` | empty | Browser favicon. Accepts an HTTPS URL, supported image data URI, or a filename under `{config_dir}/assets/`; invalid or unavailable sources use the embedded icon. |
| `server.seo.keywords` | `[]` | Values joined into the keywords meta tag. |
| `server.seo.author` | empty | Emits an author meta tag when set. |
| `server.seo.og_image` | empty | OpenGraph and Twitter image. Accepts an HTTPS URL, supported image data URI, or a filename under `{config_dir}/assets/`; invalid or unavailable sources use the embedded image. |
| `server.seo.twitter_handle` | empty | Twitter site attribution. |
| `server.seo.title_separator` | ` - ` | Separator between the page title and tagline; an empty tagline falls back to the site title, empty separators use the default, and values longer than 8 characters are ignored. |
| `server.seo.meta_tags` | `{}` | Extra name/content meta tags, sorted by name and capped at 20 emitted entries. |
| `server.seo.canonical` | empty | Optional HTTPS canonical base URL; the current request path is appended. Empty or invalid values use the request-specific public URL. |
| `server.seo.noindex` | `false` | Marks every route `noindex,nofollow` in HTML metadata and the `X-Robots-Tag` response header. The sitemap and feeds remain served. |
| `server.seo.opengraph.enabled` | `true` | Enables OpenGraph tags. |
| `server.seo.opengraph.type` | `website` | OpenGraph type. |
| `server.seo.opengraph.site_name` | empty | OpenGraph site name; defaults to `server.branding.title`. |
| `server.seo.twitter.enabled` | `true` | Enables Twitter card tags. |
| `server.seo.twitter.card` | `summary_large_image` | `summary` or `summary_large_image`; unsupported values use `summary_large_image`. |
| `server.seo.twitter.creator` | empty | Twitter creator attribution. |
| `server.branding.content.header`, `footer` | Optional operator HTML fragments. `header` is appended after the built-in header actions; `footer` replaces the branding block above the application footer. Content is sanitized before rendering. |
| `server.branding.content.about`, `privacy` | Optional Markdown, text, or sanitized HTML additions appended to the built-in pages. |
| `server.branding.content.terms` | Optional Markdown, text, or sanitized HTML that replaces the built-in terms page. |
| `server.branding.content.<page>.format` | `md` (default), `txt`, or `html`; `header` and `footer` accept only `html`. |
| `server.branding.content.<page>.body` | A string used for every language, or a language-to-string map with optional `default`; maximum 256 KiB per body. |

The page title combines the route title and tagline with `title_separator`; when the tagline is empty, the site title is used, and a repeated title is not duplicated. The canonical link and `og:url` use the configured HTTPS base plus the request path, or the resolved request URL when unset. Configured image files must be regular files directly in the private `assets/` directory, use `.svg`, `.png`, `.ico`, `.webp`, or `.jpg`, and stay under 2 MiB. Image data URIs are limited to 256 KiB and PNG, JPEG, WebP, ICO, or SVG. Remote images require HTTPS, a public destination address, and an allowed image content type; downloads are limited to 10 MiB and cached for up to 24 hours under the config cache directory. OpenGraph and Twitter tags are omitted when their respective `enabled` setting is false. The standard site-verification settings are `server.seo.verification.google`, `.bing`, `.yandex`, `.baidu`, `.pinterest`, and `.facebook`; invalid codes are omitted. Google accepts up to 43 letters, digits, `_` or `-`; Bing accepts up to 32 uppercase hexadecimal characters; Yandex and Pinterest accept up to 32 lowercase hexadecimal characters; Baidu accepts up to 32 letters or digits; Facebook accepts up to 64 lowercase letters or digits.

`server.seo.meta_tags` accepts keys matching `[a-z0-9:_.-]+` and non-empty values up to 256 characters. Built-in names `description`, `keywords`, `author`, `robots`, and `viewport` are ignored to prevent duplicate or conflicting tags. For `server.seo.verification.custom`, each item must contain exactly one of `name` or `property` plus non-empty `content` of at most 256 characters; names/properties may contain letters, digits, `_`, `:`, and `-`. Values are HTML-escaped before rendering. The `robots` directive defaults to `index,follow` only for public HTML pages; other routes default to `noindex,nofollow` and receive `X-Robots-Tag: noindex, nofollow`.

## Privacy, consent, and disclosure

Privacy settings fill the deployment-specific sections of `/server/privacy` and configure the consent banner. Built-in privacy text remains part of the page; operator additions use `server.branding.content.privacy`. Retention descriptions and availability flags describe the published policy and do not change operational retention jobs.

| YAML key | Default and behavior |
|---|---|
| `server.privacy.data.sold` | `false`; selects the privacy and consent wording for whether data is sold or shared. |
| `server.privacy.data.stored_on_server` | `true`; states whether visitor data is stored on this server. |
| `server.privacy.data.sharing[]` | Deployment-specific disclosures, each with `condition`, `when`, and `data` text. |
| `server.privacy.retention.period` | Retention description shown on the privacy page; may be a string or language map. Empty uses the built-in localized text. |
| `server.privacy.retention.export_available` | `true`; shows the owner data export right. Operators export an owner's data with `--maintenance data export --owner <prefix>`. |
| `server.privacy.retention.deletion_available` | `true`; shows the owner data deletion right. Operators delete an owner's data with `--maintenance data delete --owner <prefix>`; privacy requests can also be sent through the configured contact flow. |
| `server.tracking.{type,id,url}` | Optional analytics provider. Supported types: `none`, `google`, `matomo`/`piwik`, `owa`, `fathom`, `plausible`, `umami`, `simple`, and `cloudflare`; provider-specific IDs and URLs are validated at load time. Tags and CSP hosts are added only after analytics consent, and are never sent on Tor/I2P or when a privacy signal applies. |
| `web.headers.honor_dnt` | `false`; when enabled, `DNT: 1` is treated like `Sec-GPC: 1` and disables analytics and nonessential preference writes. |
| `server.privacy.consent.show_until_acknowledged` | `true`; keeps the banner visible until the visitor records a consent choice. |
| `server.privacy.consent.message`, `message_if_sold` | Banner text selected according to `server.privacy.data.sold`. |
| `server.privacy.consent.policy.{text,url,version}` | Privacy-policy link and version used for consent records; default version is `1`. |
| `server.privacy.consent.buttons.{decline,accept}` | Labels for the consent actions. |
| `server.privacy.consent.show_preferences`, `preferences_text` | Controls the manage-preferences link and its label. |
| `server.privacy.cookies.{essential,preferences,analytics}.{enabled,description}` | Describes and offers the three cookie categories. Essential cookies are always required; preference and analytics choices are consent-controlled. |
| `server.privacy.cookies.analytics.description_suffix_{sold,not_sold}` | Additional analytics disclosure selected by the `sold` setting. |
| `server.privacy.third_party.services[]` | Optional third-party disclosures with `name`, `purpose`, `data_sent`, and `policy_url`; defaults to an empty list. |

## Notifications

`server.notifications.disk_low` controls the scheduler's disk-space alerts for the filesystems containing the data, log, and backup directories.

| YAML key | Default | Behavior |
|---|---:|---|
| `server.notifications.disk_low.enabled` | `true` | Enables disk-space checks and notifications. |
| `server.notifications.disk_low.threshold_percent` | `85` | Used-space percentage that triggers an alert. |
| `server.notifications.disk_low.check_interval` | `5m` | Scheduler interval for checks. |
| `server.notifications.disk_low.repeat` | `24h` | Minimum interval between repeated alerts while usage remains above threshold. |

The scheduler deduplicates alerts by filesystem volume and reports recovery after usage drops 5 percentage points below the threshold. The backup skip threshold is separate (`server.backup.disk_threshold`).

## Metrics

| YAML key | Behavior |
|---|---|
| `server.metrics.enabled` | Enables internal metrics endpoints; defaults to `false`. |
| `server.metrics.root.enabled` | Enables root aliases under `/metrics`; defaults to `true` when metrics are enabled. |
| `server.metrics.auth.allow_unauthenticated` | Skips metrics bearer checks. Keep disabled unless network controls protect the endpoint. |
| `server.metrics.auth.tokens.prometheus` | Prometheus bearer token. |
| `server.metrics.auth.tokens.grafana` | Grafana bearer token. |
| `server.metrics.auth.tokens.loki` | Loki bearer token. |
| `server.metrics.include_system` | Includes available host system measurements. |
| `server.metrics.include_runtime` | Includes Go runtime measurements. |
| `server.metrics.include_app` | Includes application measurements. |
| `server.metrics.loki.max_entries` | Maximum retained Loki-compatible request events; default 1000. |
| `server.metrics.loki.max_age` | Retention duration; default `1h`. |
| `server.metrics.duration_buckets` | HTTP duration histogram buckets in seconds. |
| `server.metrics.size_buckets` | Request/response size histogram buckets in bytes. |

Missing metrics tokens are generated and stored in `secrets.yml`. See [Metrics](metrics.md) for endpoints and authorization.

## Owner tokens and security

| YAML key | Behavior |
|---|---|
| `server.tokens.max_linked` | Maximum linked tokens per owner; default 25. |
| `server.tokens.cookie_max_age` | Owner-cookie maximum age; default `1y`. |
| `server.tokens.idle_purge_days` | Idle-owner purge interval when no resources remain; `0` disables purging. |
| `server.security.encryption_key` | Generated 32-byte encryption key used for encrypted application data. Keep it in `secrets.yml`. |

See [Tokens](tokens.md) for issuance and management, and [Security](security.md) for the security model and report routes.

## Contact, abuse reports, and email

| YAML key | Behavior |
|---|---|
| `server.contact.admin.email` | Administrative contact recipient. |
| `server.contact.security.email` | Security report recipient. |
| `server.contact.abuse.email` | Abuse report recipient. |
| `server.contact.general.email` | General/privacy contact recipient and fallback where documented. |
| `server.contact.dpo.{name,email}` | Data protection contact details. |
| `server.pages.contact.enabled` | Enables the contact/report form; defaults to `true`. |
| `server.pages.contact.captcha` | `simple` (default), `recaptcha`, or `hcaptcha`; overlay requests always use `simple`. |
| `server.pages.contact.captcha_site_key` | Public site key rendered for the selected external CAPTCHA provider. |
| `server.pages.contact.captcha_secret_key` | External CAPTCHA secret; stored in `secrets.yml` and never returned by the API. |
| `server.pages.contact.success_message` | Confirmation message shown after an accepted report. |
| `server.pages.contact.antispam.honeypot` | Enables the hidden-field bot check. |
| `server.pages.contact.antispam.min_fill_seconds` | Minimum time before a report can be submitted. |
| `server.pages.contact.antispam.max_age_seconds` | Maximum form age. |
| `server.pages.contact.antispam.per_ip` | Per-IP submission threshold. |
| `server.pages.contact.antispam.per_email` | Per-email submission threshold. |
| `server.pages.contact.antispam.global` | Global submission threshold. |
| `server.pages.contact.antispam.window` | Rate-limit window duration. |
| `server.pages.contact.antispam.max_links` | Maximum links accepted in a report. |
| `server.pages.contact.antispam.reject_markup` | Reject markup in submitted text. |
| `server.pages.contact.antispam.blocked_words` | Terms rejected by spam filtering. |
| `server.pages.contact.antispam.blocked_email_domains` | Email domains rejected by spam filtering. |
| `server.pages.contact.antispam.strikes` | Strike count before a temporary block. |
| `server.pages.contact.antispam.ban_hours` | Temporary block duration. |
| `server.pages.contact.antispam.review_min_seconds` | Minimum delay before report confirmation. |
| `server.pages.contact.antispam.review_max_seconds` | Maximum report review-token age. |
| `server.pages.contact.antispam.duplicate_window_hours` | Duplicate-report detection interval. |
| `server.security.encryption_key` | Encryption key for sensitive report data; generated when absent and stored in `secrets.yml`. |

Email settings use `server.notifications.email`:

| YAML key | Behavior |
|---|---|
| `server.notifications.email.enabled` | Runtime state; enabled only after the startup SMTP handshake succeeds. |
| `server.notifications.email.smtp.host` | SMTP relay hostname. |
| `server.notifications.email.smtp.port` | TCP port, 1–65535; defaults to 587. |
| `server.notifications.email.smtp.username` | Optional SMTP username. |
| `server.notifications.email.smtp.password` | Optional SMTP password; put it in `secrets.yml`. |
| `server.notifications.email.smtp.tls` | `auto`, `starttls`, `tls`, or `none`; defaults to `auto`. |
| `server.notifications.email.from.name` | Sender display name. |
| `server.notifications.email.from.email` | Sender email address. |
| `server.notifications.email.reply_to` | Optional RFC email address used as Reply-To and the default recipient for `gitmessages email test`; invalid values are warned about and cleared. Provide `--to ADDRESS` to override the test recipient. |
| `server.notifications.email.events.*` | Per-event operator email switches; event names include `startup`, `shutdown`, `backup_complete`, `backup_failed`, `ssl_expiring`, `ssl_renewed`, `ssl_renewal_failed`, `security_alert`, `scheduler_error`, `update_available`, `update_installed`, `update_failed`, and `update_staged`. |

Operator email language uses `server.i18n.default_language`. The supported runtime locale registry also includes `zh` and `ja`; if the configured default is unavailable, rendering falls back to English. Email templates live in `{config_dir}/template/email/`, override embedded templates by filename, and reload on each render.

SMTP environment overrides are `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `SMTP_FROM_NAME`, and `SMTP_FROM_EMAIL`. A configured host is checked with an SMTP greeting at startup and on reload; an unavailable relay disables email until the next check. When `smtp.host` is empty and `SMTP_HOST` is unset, startup probes `127.0.0.1`, `172.17.0.1`, the default gateway, the server FQDN, a global IPv4 address, `mail.{fqdn}`, and `smtp.{fqdn}` in that order, using ports 25, 465, then 587 for each host. The first working relay is saved to `yaml.d/notifications.yml`. If no relay answers, email remains disabled and the server continues.

## Tor and I2P

Tor settings are under `server.tor`:

| YAML key | Behavior |
|---|---|
| `binary` | Tor executable path; otherwise an available system binary is used. |
| `onion_address` | Configured onion host used for request-aware public URLs. |
| `use_network` | Enables Tor network behavior. |
| `max_circuits`, `circuit_timeout`, `max_streams_per_circuit` | Circuit and stream limits. |
| `startup_timeout`, `bootstrap_timeout` | Startup and bootstrap timeouts in seconds. |
| `safe_logging` | Redacts sensitive Tor logs. |
| `close_circuit_on_stream_limit` | Closes a circuit when it reaches the stream limit. |
| `bandwidth_rate`, `bandwidth_burst`, `max_monthly_bandwidth` | Tor bandwidth limits. |
| `num_intro_points` | Hidden-service introduction point count. |
| `virtual_port` | Onion service HTTP port; default 80. |

I2P settings are under `server.i2p`:

| YAML key | Behavior |
|---|---|
| `enabled` | Enables I2P; defaults to enabled in a detected container and disabled otherwise. `I2P_ENABLED` and `--i2p` can override it. |
| `b32_address` | Configured I2P hostname used for request-aware URLs. |
| `binary` | `i2pd` executable path. |
| `sam_address` | SAM bridge address; default `127.0.0.1:7656`. |
| `virtual_port` | I2P service port; default 80. |
| `inbound_length`, `outbound_length` | Tunnel hop counts. |
| `inbound_quantity`, `outbound_quantity` | Number of inbound and outbound tunnels. |
| `signature_type` | I2P destination signature type. |
| `bootstrap_timeout` | I2P startup timeout in seconds. |

## CLI configuration

The CLI stores `cli.yml` and profile files in the platform user configuration directory under `apimgr/gitmessages`. Select a profile with `--config`. Files are created with owner-only permissions. The server selection order is `--server`, `GITMESSAGES_SERVER_PRIMARY`, then `server.primary`. A valid `--server` argument is saved only when no valid server is already configured.

The CLI config supports `language`, `display.mode` (`auto`, `gui`, or `tui`), `server.primary`, `server.token`, the discovered `server.api_version`, and `update.auto`, `update.check_interval`, and `update.channel`. The CLI queries `/api/autodiscover` on startup and records the server's active API version before using versioned routes. Language precedence is `--lang`, CLI config `language`, `LC_ALL`, `LANG`, then English (`en`); `language: auto` uses the environment. The default static CLI uses the TUI; optional `gitmessages-cli-gui-{os}-{arch}` builds enable GUI mode on Linux, macOS, and Windows. BSD builds use the terminal interface.

The server's CLI binary distribution settings are `cli.binary_download.dir` (defaults to `{data_dir}/cli`) and `cli.binary_download.require_auth` (defaults to `false`), stored in `yaml.d/maintenance.yml`. Release binaries are provisioned by the operator, image, or package; the server does not build or download them. Downloads are public by default and require a valid Bearer token when `require_auth` is enabled. See [Integrations](integrations.md) for autodiscovery and download behavior.

## Contact CAPTCHA behavior

The `simple` CAPTCHA is self-hosted. `recaptcha` and `hcaptcha` render their provider widget and keep a simple challenge in a `<noscript>` fallback. Their API descriptor includes that same fallback challenge. Provider verification fails closed, and a failed API submission includes a fresh CAPTCHA descriptor under `details.captcha`. Requests served on `.onion` or `.i2p` always use the simple challenge and do not call a clearnet provider.

## SMTP availability

The server checks a configured SMTP relay with an EHLO handshake at startup and after configuration reload. If no SMTP host is configured, it probes the local relay candidates from AI.md in priority order and saves a successful host and port in `yaml.d/notifications.yml`. Email delivery stays disabled when no relay responds. Credentials are sent only after the configured TLS policy has been satisfied.
