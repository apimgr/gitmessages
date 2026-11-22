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

Area names are conventions; the loader accepts supported settings in any merged YAML file. Do not put credentials in a drop-in. Unknown keys are ignored by the current loader.

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
| `server.logs.level` | Log level; defaults to `info`. |

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
| `web.security.publish_pgp_key` | Whether the configured public PGP key is published at `/.well-known/pgp-key.asc`. |

The server generates `/.well-known/security.txt`, `/.well-known/llms.txt`, and `/llms.txt`. See [Security](security.md) and [Integrations](integrations.md) for public discovery and reporting behavior. These generated endpoints use request-specific URLs; configure the proxy trust list before forwarding external host, scheme, port, or prefix headers.

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

The CLI config supports `language`, `display.mode` (`auto`, `gui`, or `tui`), `server.primary`, and the discovered `server.api_version`. The CLI queries `/api/autodiscover` on startup and records the server's active API version before using versioned routes. An explicit `--lang` applies to the current invocation; otherwise language selection uses the config and system locale. GUI mode is available on Linux, macOS, and Windows when a local display is available. FreeBSD builds use the terminal interface; automatic mode selects it when a terminal is available.

## Contact CAPTCHA behavior

The `simple` CAPTCHA is self-hosted. `recaptcha` and `hcaptcha` render their provider widget and keep a simple challenge in a `<noscript>` fallback. Their API descriptor includes that same fallback challenge. Provider verification fails closed, and a failed API submission includes a fresh CAPTCHA descriptor under `details.captcha`. Requests served on `.onion` or `.i2p` always use the simple challenge and do not call a clearnet provider.

## SMTP availability

The server checks a configured SMTP relay with an EHLO handshake at startup and after configuration reload. If no SMTP host is configured, it probes the local relay candidates from AI.md in priority order and saves a successful host and port in `yaml.d/notifications.yml`. Email delivery stays disabled when no relay responds. Credentials are sent only after the configured TLS policy has been satisfied.
