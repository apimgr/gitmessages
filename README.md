# Git Messages

[![Release](https://img.shields.io/github/v/release/apimgr/gitmessages)](https://github.com/apimgr/gitmessages/releases)
[![License](https://img.shields.io/github/license/apimgr/gitmessages)](LICENSE.md)
[![CI](https://github.com/apimgr/gitmessages/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/gitmessages/actions/workflows/ci.yml)
[![Documentation Status](https://readthedocs.org/projects/apimgr-gitmessages/badge/?version=latest)](https://apimgr-gitmessages.readthedocs.io/en/latest/?badge=latest)

## About

Git Messages is a curated reference and builder for git commit message conventions. Browse commit types, emoji mappings, templates, and examples, or build a message using Conventional Commits or Angular formatting. The catalog is embedded in the Go binaries. There are no user accounts or submitted catalog content. Anonymous owner tokens support protected API operations, and configured contact/security forms accept reports required by the server specification. The service can provide alternate access through Tor and optional I2P when configured and available.

## Features

- Commit types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`, `build`, and `revert`
- Conventional Commits and Angular convention references
- Feature, bug-fix, release, hotfix, and documentation templates
- Random example messages
- Server-rendered, mobile-friendly web UI and installable PWA
- Eight embedded interface languages: English, Spanish, French, German, Italian, Arabic, Chinese, and Japanese
- Versioned REST API, OpenAPI JSON, GraphQL, and Bearer-authenticated MCP tools
- Owner tokens for MCP access and owner-scoped API resources, with secure browser storage and transparent migration for existing browsers
- Contact and coordinated security-report pages
- Internal Prometheus, Grafana, and Loki-compatible metrics endpoints
- Companion `gitmessages-cli` for shell use
- Interactive terminal interface in the default static CLI; optional GUI builds for desktop platforms
- First-run server connection flow and saved client profiles
- Optional Tor v3 onion service and I2P eepsite support

## Production

### Linux, macOS, Windows, and FreeBSD

Download the server and CLI binaries for your operating system and architecture from the [GitHub Releases page](https://github.com/apimgr/gitmessages/releases). Keep the matching server and CLI release versions together. On Unix, mark both files executable and place them in a directory on `PATH`; on Windows, place the `.exe` files in a directory on `PATH`.

### Docker

```sh
docker run --detach --name gitmessages \
  --publish 172.17.0.1:64580:80 \
  --volume gitmessages-config:/config \
  --volume gitmessages-data:/data \
  --env PORT=80 \
  ghcr.io/apimgr/gitmessages:latest
```

The included Compose deployment is in `docker/docker-compose.yml`. Run it from
the `docker/` directory with `docker compose up -d`; review its host port
binding before exposing the service on a network.

### Binary and systemd

Download the server binary for your operating system from [GitHub Releases](https://github.com/apimgr/gitmessages/releases). On Linux, macOS, and FreeBSD, install the executable on `PATH`; Windows releases use `.exe` files. For Linux systemd, create the `gitmessages` service account and private configuration directory, then run `gitmessages --config /etc/apimgr/gitmessages --service --install` as root. See the [installation guide](docs/installation.md#linux-systemd) for all steps.

### Run the server

```sh
gitmessages --port 8080 --address 127.0.0.1
```

Open `http://127.0.0.1:8080/`. The application uses a read-only embedded catalog and has no operator web panel. Server settings are read from `server.yml` in the configuration directory selected by `--config` or `CONFIG_DIR`.

The server supports `-h`/`--help`, `-v`/`--version`, `--port`, `--address`, `--config`, `--mode`, `--update`, `--service`, `--maintenance`, `--debug`, `--color auto|yes|no`, `--lang CODE`, shell completions/init with `--shell`, and `email test` commands. Use `gitmessages email test --dry-run` to preview and validate templates, or `gitmessages email test --to ADDRESS` to send a test message through the configured SMTP relay. Server settings are documented in the [configuration reference](docs/configuration.md).

## Client

Install `gitmessages-cli` from the same release as the server. The default CLI is a static command-line and terminal interface; with no configured server, it provides a first-run connection flow. Optional `gitmessages-cli-gui-{os}-{arch}` release artifacts enable the native desktop GUI on Linux, macOS, and Windows and require the toolchain's system loader. BSD builds use the terminal interface. Select `display.mode` as `auto`, `gui`, or `tui` in `cli.yml`; without the optional GUI build, automatic mode selects the TUI when a terminal is available.

The CLI uses `--server`, `GITMESSAGES_SERVER_PRIMARY`, or its user configuration at `~/.config/apimgr/gitmessages/cli.yml`.

```sh
gitmessages-cli --server http://127.0.0.1:8080 types
gitmessages-cli --server http://127.0.0.1:8080 random
git commit -m "$(gitmessages-cli --server http://127.0.0.1:8080 random)"
gitmessages-cli --server http://127.0.0.1:8080 build --type feat --scope api --description 'add message search'
```

Use `--json` before the command to print JSON output. Run `gitmessages-cli --shell completions [SHELL]` or `gitmessages-cli --shell init [SHELL]`; omit `SHELL` to detect it from the environment. Completions support bash, zsh, fish, sh, dash, ksh, PowerShell, and pwsh. Select a language with `--lang`, `GITMESSAGES_LANG`, the `language` setting in `cli.yml`, or `LANG`. See the [CLI reference](docs/cli.md) for all options.

## Configuration

Server configuration is optional. Settings are read from `server.yml`, `yaml.d/` area files, and `secrets.yml`; the [configuration reference](docs/configuration.md) describes precedence, supported settings, and defaults. The CLI creates its config directory with user-only permissions and writes `cli.yml` with mode `0600`.

## API

Catalog reads are public and read-only except for the stateless message builder. Protected routes such as MCP, metrics, and owner-token management require their specified Bearer token. Responses use JSON envelopes with `ok` and `data` fields.

```sh
SERVER_URL="${GITMESSAGES_SERVER:?set GITMESSAGES_SERVER}"
API_VERSION="${API_VERSION:-v1}"
curl -q -LSsf "$SERVER_URL/api/$API_VERSION/types"
curl -q -LSsf "$SERVER_URL/api/$API_VERSION/conventions"
curl -q -LSsf "$SERVER_URL/api/$API_VERSION/templates"
curl -q -LSsf "$SERVER_URL/api/$API_VERSION/messages/random"
curl -q -LSsf "$SERVER_URL/api/$API_VERSION/messages" \
  -H 'Content-Type: application/json' \
  -d '{"type":"feat","scope":"api","description":"add message search"}'
```



The active API version is available from `GET /api/autodiscover` (default `v1`). OpenAPI JSON is served at `/api/{api_version}/server/swagger` (also `/api/swagger`). GraphQL accepts POST requests at `/api/{api_version}/server/graphql` and `/api/graphql`. MCP Streamable HTTP accepts JSON-RPC POST requests at `/api/{api_version}/server/mcp` (also `/api/mcp` and `/mcp`) and requires an owner or operator Bearer token. See the [API reference](docs/api.md).

## Other

### Web pages

- `/` — product home
- `/types` — commit type reference
- `/templates` — message templates
- `/builder` — interactive commit message builder
- `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`, `/server/security`, `/server/security/policy`, `/server/security/thanks`, `/server/dpo` — server information and reporting pages
- `/server/contact` and `/.well-known/security.txt` — configured contact and security-report entry points
- `/server/preferences/tokens` — anonymous owner token management
- `/server/preferences/sync` — export and import browser preferences between devices
- `/server/metrics[/{service}]` — internal service metrics, protected by per-service Bearer tokens
- `/server/docs/swagger` — interactive REST API explorer with an OpenAPI JSON link
- `/server/docs/graphql` — interactive GraphiQL UI with a no-JavaScript query form
- `/server/help/mcp` — MCP connection guide for the public catalog and stateless message builder
- `/robots.txt` and `/sitemap.xml` — crawler rules and public HTML page discovery
- `/feed.xml` (Atom) and `/rss.xml` (RSS) — valid empty feeds; the product has no public item resource

## Security and privacy

The catalog is embedded and user-submitted commit messages or conventions are not accepted. Contact and security reports follow the configured validation, encryption, access, and retention rules. Builder fields are validated on the server. Public endpoints include security headers, CORS, request size limits, and per-client rate limiting. Tor and I2P provide alternate access. The service does not query third-party providers for catalog or message data; it can fetch operational security and GeoIP datasets from configured sources. Tor starts when its binary is available. I2P is opt-in on bare metal and in VMs and enabled by default in containers.

## Development

See [the developer documentation](docs/development.md) for repository structure, contribution guidance, and isolated binary integration checks.
See the [translation guide](docs/translating.md) to update or add a locale.

Go development commands run through the `casjaysdev/go:latest` container using Make targets:

```sh
make dev
make local
make build
make test
```

## Disclaimer

This software is provided "as is" without warranty of any kind. Use it at your own risk. The authors are not responsible for damages, data loss, or issues resulting from its use. The catalog is a reference and does not replace your repository's contribution rules. Evaluate the software before production use.

## License

MIT. See [LICENSE.md](LICENSE.md).
