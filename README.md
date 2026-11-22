# Git Messages

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE.md)

Git Messages is a curated reference and builder for git commit message conventions. Browse commit types, emoji mappings, templates, and examples, or build a message using Conventional Commits or Angular formatting. The catalog is embedded in the Go binaries. The service has no accounts, user submissions, or catalog database; it can provide alternate access through Tor and optional I2P when configured and available.

## Features

- Commit types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`, `build`, and `revert`
- Conventional Commits and Angular convention references
- Feature, bug-fix, release, hotfix, and documentation templates
- Random example messages
- Server-rendered, mobile-friendly web UI and installable PWA
- Eight embedded interface languages: English, Spanish, French, German, Italian, Arabic, Chinese, and Japanese
- Versioned REST API, OpenAPI JSON, GraphQL, and public MCP tools
- Companion `gitmessages-cli` for shell use
- Interactive terminal menu when `gitmessages-cli` starts without a command in a TTY
- Optional Tor v3 onion service and I2P eepsite support

## Install

### Linux, macOS, Windows, and FreeBSD

Download the server and CLI binaries for your operating system and architecture from the [GitHub Releases page](https://github.com/apimgr/gitmessages/releases). Keep the matching server and CLI release versions together. On Unix, mark both files executable and place them in a directory on `PATH`; on Windows, place the `.exe` files in a directory on `PATH`.

### Docker

```sh
docker run --detach --name gitmessages \
  --publish 64580:80 \
  --volume gitmessages-config:/config \
  --volume gitmessages-data:/data \
  --env PORT=80 \
  ghcr.io/apimgr/gitmessages:latest
```

The included Compose deployment is in `docker/docker-compose.yml`. Run it from
the `docker/` directory with `docker compose up -d`; review its host port
binding before exposing the service on a network.

## Run the server

```sh
gitmessages --port 8080 --address 127.0.0.1
```

Open `http://127.0.0.1:8080/`. The application uses a read-only embedded catalog and has no operator web panel. Server settings are read from `server.yml` in the configuration directory selected by `--config` or `CONFIG_DIR`.

The server supports `-h`/`--help`, `-v`/`--version`, `--port`, `--address`, `--config`, `--mode`, `--update`, `--service`, `--maintenance`, `--debug`, `--color auto|yes|no`, `--lang CODE`, and shell completions/init with `--shell`. The CLI supports command mode and an interactive terminal menu when launched without a command in a TTY. See the [CLI reference](docs/cli.md) and [configuration reference](docs/configuration.md) for details.

## Web pages

- `/` — product home
- `/types` — commit type reference
- `/templates` — message templates
- `/builder` — interactive commit message builder
- `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`, `/server/security`, `/server/preferences` — server information and display preference pages
- `/.well-known/security.txt` — security reporting metadata
- `/server/docs/swagger` — REST API reference
- `/server/docs/graphql` — GraphQL query guide
- `/server/help/mcp` — MCP connection guide for the public catalog and stateless message builder

## REST API

The API is public and read-only except for its stateless message builder. Responses use JSON envelopes with `ok` and `data` fields.

```sh
curl -q -LSsf http://127.0.0.1:8080/api/v1/types
curl -q -LSsf http://127.0.0.1:8080/api/v1/conventions
curl -q -LSsf http://127.0.0.1:8080/api/v1/templates
curl -q -LSsf http://127.0.0.1:8080/api/v1/messages/random
curl -q -LSsf http://127.0.0.1:8080/api/v1/types \
  -H 'Accept-Language: es'
curl -q -LSsf http://127.0.0.1:8080/api/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{"type":"feat","scope":"api","description":"add message search"}'
```

OpenAPI JSON is served at `/api/v1/server/swagger` (also `/api/swagger`). GraphQL accepts GET and POST requests at `/graphql`, `/api/v1/server/graphql`, and `/api/graphql`. MCP Streamable HTTP accepts JSON-RPC POST requests at `/api/v1/server/mcp` (also `/api/mcp` and `/mcp`). API responses support JSON and plain text through `.txt` suffixes or `Accept` headers.

## CLI

The CLI uses `--server`, `GITMESSAGES_SERVER_PRIMARY`, or its user configuration at `~/.config/apimgr/gitmessages/cli.yml`.

```sh
gitmessages-cli --server https://gitmessages.example types
gitmessages-cli --server https://gitmessages.example random
git commit -m "$(gitmessages-cli random)"
gitmessages-cli --server https://gitmessages.example build --type feat --scope api --description 'add message search'
```

Use `--json` before the command to print JSON output. Run `gitmessages-cli --shell bash` (or `zsh`, `fish`, or `powershell`) to print completions. Select a language with `--lang`, `GITMESSAGES_LANG`, the `language` setting in `cli.yml`, or `LANG`.

## Build and test

Go commands run through the `casjaysdev/go:latest` container using Make targets:

```sh
make dev
make local
make build
make test
```

## Configuration

Configuration is optional. Server defaults are documented in `src/config/config.go`; the product has no account settings. The CLI creates its config directory with user-only permissions and writes `cli.yml` with mode `0600`.

## Security and privacy

The catalog is embedded and no user-submitted content is stored. All builder fields are validated on the server. Public endpoints include security headers, CORS, request size limits, and per-client rate limiting. Tor and I2P provide alternate access; the service does not query third-party content or data providers. Tor starts when its binary is available. I2P is opt-in on native systems and enabled by default in containers.

## Development

See [the developer documentation](docs/development.md) for repository structure and contribution guidance.
See the [translation guide](docs/translating.md) to update or add a locale.

## Disclaimer

This software is provided "as is" without warranty of any kind. Use it at your own risk. The authors are not responsible for damages, data loss, or issues resulting from its use. The catalog is a reference and does not replace your repository's contribution rules. Evaluate the software before production use.

## License

MIT. See [LICENSE.md](LICENSE.md).
