# Configuration

## Server

The server reads `server.yml` from the configuration directory. Select the directory with `--config DIR` or `CONFIG_DIR`; the file name is `server.yml`. When no file exists, the application writes defaults.

Supported settings are `server.port`, `server.address`, `server.mode`, `server.fqdn`, `server.update_branch`, `server.metrics.*`, `server.logging.*`, `web-ui.*`, `web-robots.*`, and `web-security.*`. Defaults are defined by `src/config/config.go`. This product has no database settings, account configuration, or web administration panel.

Command-line `--port` and `--address` override the configuration for the current run. `PORT` and `ADDRESS` environment variables are used when their corresponding flags are absent.

## CLI

The CLI reads `~/.config/apimgr/gitmessages/cli.yml` on Unix-like systems (the platform config directory on Windows). It creates the file with user-only permissions when saving a server first specified with `--server`.

The server selection order is `--server`, `GITMESSAGES_SERVER_PRIMARY`, then `server.primary` in `cli.yml`. No official server URL is compiled into the client.
