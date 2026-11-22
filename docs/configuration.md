# Configuration

## Server configuration

The server reads `{config_dir}/server.yml`; choose the directory with `--config DIR` or `CONFIG_DIR`. The merged configuration order is built-in defaults, `server.yml`, regular `yaml.d/*.yml` and `yaml.d/*.yaml` files in byte-wise filename order, then `secrets.yml`. Maps merge recursively; later scalar and list values replace earlier values. Symlinks are followed only when their targets stay inside `yaml.d/`.

`server.yml` holds node-local settings: `server.port`, `server.address`, `server.fqdn`, and `server.mode`. Other settings belong in area files under `yaml.d/`; the server migrates legacy combined configuration after a successful load and keeps a private `server.yml.bak` during migration. The `yaml.d/` directory and config files use owner-only permissions. Runtime secrets belong in `secrets.yml`.

The current implementation supports `server.update.*`, `server.cors.allowed_origins`, `server.metrics.*`, `server.logs.*`, `server.tor.*`, `server.i2p.*`, `web.ui.theme`, `web.robots.*`, and `web.security.contact`. The default theme is dark; a visitor's `theme` cookie takes precedence. MCP origin validation accepts the request's own origin, the configured `server.fqdn`, and explicitly listed CORS origins; `*` does not authorize MCP origins. Older `server.logging.*`, `web-ui.*`, `web-robots.*`, and `web-security.*` keys are migrated where recognized. The server creates a default `server.yml` on first start. Unsupported configuration sections from the full AI.md schema are still being brought into alignment; do not treat this list as a complete implementation of that schema.

Mode selection is `--mode`, `MODE`, `server.mode`, then `production`. Debug selection is `--debug`, `DEBUG`, then the mode default; `debug` mode enables debug output unless explicitly disabled. `--port` overrides `PORT`, which overrides the saved port. `--address` overrides `LISTEN`, which overrides the saved address. Containers default to port 80; native installs select an available port in 64000–64999.

Tor v3 hosting starts automatically when a Tor binary is available. I2P is enabled by default in containers and is opt-in elsewhere; use `server.i2p.enabled`, `I2P_ENABLED`, or `--i2p`. The server prefers a dedicated `i2pd` process and falls back to an available local SAM bridge. Both overlays serve the same local public reference service.

## CLI configuration

The CLI reads `cli.yml` from the platform user config directory under `apimgr/gitmessages`. Select a profile with `--config`; profile files and the default config are created with owner-only permissions. The server selection order is `--server`, `GITMESSAGES_SERVER_PRIMARY`, then `server.primary` in the CLI config. A valid `--server` value is saved only when no valid server is already configured.

The CLI config also supports a `language` preference and `display.mode` (`auto`, `gui`, or `tui`). An explicit `--lang` applies to the current invocation; otherwise language selection uses the config and system locale.
