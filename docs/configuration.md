# Configuration

This reference documents settings currently read by the Git Messages server. The complete implementation specification is [AI.md](../AI.md); see its configuration section for requirements not yet exposed by the current binary.

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
| `server.port` | TCP port from 1–65535. Defaults to `PORT`, port 80 in a detected container, or an available port in 64000–64999 on a host. `--port` overrides the environment and file. |
| `server.address` | IP address to bind. Defaults to `LISTEN`, then `[::]`. `--address` has the highest priority. |
| `server.fqdn` | Configured public host name; used by URL and MCP origin handling. |
| `server.baseurl` | Optional path prefix for public URLs, such as `/messages`. |
| `server.mode` | `production`, `development`, or `debug`; `--mode` and `MODE` can override it. |
| `server.token` | Operator bearer token. Keep it in `secrets.yml`; the server creates one when absent. |
| `server.trusted_proxies.additional` | List of proxy IP addresses or CIDRs permitted to supply forwarded headers. Private and loopback peers are trusted automatically. |
| `server.trusted_proxies.client_ip_header` | Configures the client-IP header selection policy. |
| `server.cors.allowed_origins` | `*` or a list of HTTP/HTTPS origin URLs. A wildcard does not authorize MCP origins. |
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
| `server.pages.contact.captcha` | `simple`, `recaptcha`, or `hcaptcha`; defaults to `simple`. |
| `server.pages.contact.captcha_site_key` | Site key for an external CAPTCHA provider. |
| `server.pages.contact.captcha_secret_key` | External CAPTCHA secret; store in `secrets.yml`. |
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
| `server.notifications.email.enabled` | Runtime email enablement; the current loader enables it when an SMTP host is configured. |
| `server.notifications.email.smtp.host` | SMTP relay hostname. |
| `server.notifications.email.smtp.port` | TCP port, 1–65535; defaults to 587. |
| `server.notifications.email.smtp.username` | Optional SMTP username. |
| `server.notifications.email.smtp.password` | Optional SMTP password; put it in `secrets.yml`. |
| `server.notifications.email.smtp.tls` | `auto`, `starttls`, `tls`, or `none`; defaults to `auto`. |
| `server.notifications.email.from.name` | Sender display name. |
| `server.notifications.email.from.email` | Sender email address. |

SMTP environment overrides are `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `SMTP_FROM_NAME`, and `SMTP_FROM_EMAIL`. An empty SMTP host leaves email delivery disabled.

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

The CLI config supports `language`, `display.mode` (`auto`, `gui`, or `tui`), and `server.primary`. An explicit `--lang` applies to the current invocation; otherwise language selection uses the config and system locale.

## Settings not yet exposed by the current loader

AI.md also specifies settings that are not represented by the current Go config structs or runtime loader, including configurable `web.llms` and `web.well_known` switches, SMTP discovery/probing and email templates, DNS refresh for trusted proxies, URL-domain learning, and several general server/database/backup sections. Do not rely on those settings being active until the implementation supports them. This implementation gap is tracked for alignment with AI.md.
