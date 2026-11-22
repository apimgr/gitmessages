# Configuration

## Server configuration

The server reads `{config_dir}/server.yml`; choose the directory with `--config DIR` or `CONFIG_DIR`. The merged configuration order is built-in defaults, `server.yml`, regular `yaml.d/*.yml` and `yaml.d/*.yaml` files in byte-wise filename order, then `secrets.yml`. Maps merge recursively; later scalar and list values replace earlier values. Symlinks are followed only when their targets stay inside `yaml.d/`.

`server.yml` holds node-local settings: `server.port`, `server.address`, `server.fqdn`, and `server.mode`. Other settings belong in area files under `yaml.d/`; the server migrates legacy combined configuration after a successful load and keeps a private `server.yml.bak` during migration. The `yaml.d/` directory and config files use owner-only permissions. Runtime secrets belong in `secrets.yml`.

### Supported server settings

| YAML key | Purpose and accepted values |
|---|---|
| `server.port` | TCP port from 1–65535. Defaults to 80 in a container or an available port in 64000–64999 on a host. `--port` overrides `PORT`, which overrides this value. |
| `server.address` | IP address to bind. Defaults to `[::]`; `--address` overrides `LISTEN`, which overrides this value. |
| `server.fqdn` | Public hostname used for MCP origin validation. |
| `server.mode` | `production`, `development`, or `debug`. `--mode` and `MODE` can override it. |
| `server.update.branch` | `stable`, `beta`, or `devel` release channel. |
| `server.update.auto_install` | Whether eligible updates are installed automatically. |
| `server.update.defer_days` | Delay automatic installation by 0–365 days. |
| `server.update.cli_min_version` | Minimum CLI version advertised to clients. |
| `server.cors.allowed_origins` | `*` or a list of origin URLs. MCP origin checks accept only the request origin, `server.fqdn`, or an explicit origin; `*` does not authorize MCP origins. |
| `server.metrics.enabled` | Enable the internal metrics endpoints. Defaults to `false`. |
| `server.metrics.root.enabled` | Enable root aliases under `/metrics`; defaults to `true` when metrics are enabled. |
| `server.metrics.auth.allow_unauthenticated` | Skip metrics bearer-token checks. Defaults to `false`; use only behind network access controls. |
| `server.metrics.auth.tokens.{prometheus,grafana,loki}` | Per-service bearer tokens. Generated into owner-only `secrets.yml` when missing. |
| `server.metrics.include_system` | Include available host system measurements. |
| `server.metrics.include_runtime` | Include Go runtime measurements. |
| `server.metrics.loki.{max_entries,max_age}` | Bound recent request events returned by the Loki-compatible endpoint. Defaults to 1000 entries and one hour. |
| `server.metrics.duration_buckets` | Prometheus HTTP duration histogram buckets, in seconds. |
| `server.metrics.size_buckets` | Prometheus request and response size histogram buckets, in bytes. |
| `server.logs.access_format` | Access log format; defaults to `apache`. |
| `server.logs.level` | Log level; defaults to `info`. |
| `server.tor.*` | Configure the Tor binary, networking, startup/bootstrap timeouts, circuit and stream limits, bandwidth limits, introduction points, and virtual HTTP port. Tor starts automatically when available. |
| `server.i2p.*` | Configure enablement, i2pd binary or SAM bridge, virtual HTTP port, tunnel lengths and quantities, signature type, and bootstrap timeout. |
| `web.ui.theme` | Default theme: `dark`, `light`, or `auto`. A visitor's `theme` cookie takes precedence. |
| `web.ui.accent` | UI accent color. |
| `web.robots.allow` | Paths published as allowed in `robots.txt`. |
| `web.robots.deny` | Paths published as disallowed in `robots.txt`. |
| `web.security.contact` | Optional security contact setting. |

Tor v3 hosting starts automatically when a Tor binary is available. I2P is enabled by default in containers and is opt-in elsewhere; use `server.i2p.enabled`, `I2P_ENABLED`, or `--i2p`. The server prefers its own `i2pd` process and can use an available local SAM bridge. Both overlays serve the same local public reference service.

Legacy `server.logging.*`, `web-ui.*`, `web-robots.*`, and `web-security.*` keys are migrated where recognized. The generated sample config and this table describe the settings currently accepted by the binary; unrecognized YAML keys do not enable features.

Mode selection is `--mode`, `MODE`, `server.mode`, then `production`. Debug selection is `--debug`, `DEBUG`, then the mode default; `debug` mode enables debug output unless explicitly disabled. `--port` overrides `PORT`, which overrides the saved port. `--address` overrides `LISTEN`, which overrides the saved address. Containers default to port 80; native installs select an available port in 64000–64999.

## CLI configuration

The CLI reads `cli.yml` from the platform user config directory under `apimgr/gitmessages`. Select a profile with `--config`; profile files and the default config are created with owner-only permissions. The server selection order is `--server`, `GITMESSAGES_SERVER_PRIMARY`, then `server.primary` in the CLI config. A valid `--server` value is saved only when no valid server is already configured.

The CLI config also supports a `language` preference and `display.mode` (`auto`, `gui`, or `tui`). An explicit `--lang` applies to the current invocation; otherwise language selection uses the config and system locale.
