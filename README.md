# Git Messages

Git Messages is a curated reference and builder for git commit message conventions. Browse commit types, emoji mappings, templates, and examples, or build a message using Conventional Commits or Angular formatting. The catalog is embedded in the Go binaries; the service has no accounts, submissions, or runtime data store.

## Features

- Commit types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`, `build`, and `revert`
- Conventional Commits and Angular convention references
- Feature, bug-fix, release, hotfix, and documentation templates
- Random example messages
- Server-rendered, mobile-friendly web UI and installable PWA
- English, Spanish, Chinese, French, Arabic, German, and Japanese interface translations
- Versioned REST API, OpenAPI JSON, and GraphQL
- Companion `gitmessages-cli` for shell use

## Install

Download the server and CLI binaries for your operating system from the [GitHub Releases page](https://github.com/apimgr/gitmessages/releases). For containers, use `ghcr.io/apimgr/gitmessages:latest`.

## Run the server

```sh
gitmessages --port 8080 --address 127.0.0.1
```

Open `http://127.0.0.1:8080/`. The application uses a read-only embedded catalog; it does not need a database or operator web panel. Server settings are read from `server.yml` in the configuration directory selected by `--config` or `CONFIG_DIR`.

The server supports `-h`/`--help`, `-v`/`--version`, `--debug`, `--color auto|yes|no`, `--lang CODE`, and shell completions/init with `--shell`. See the [CLI reference](docs/cli.md) for details.

## Web pages

- `/` — product home
- `/types` — commit type reference
- `/templates` — message templates
- `/builder` — interactive commit message builder
- `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms` — server information pages
- `/server/docs/swagger` — REST API reference
- `/server/docs/graphql` — GraphQL query guide

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

OpenAPI JSON is served at `/api/v1/server/swagger` (also `/api/swagger`). GraphQL accepts GET and POST requests at `/graphql`, `/api/v1/server/graphql`, and `/api/graphql`.

## CLI

The CLI uses `--server`, `GITMESSAGES_SERVER_PRIMARY`, or its user configuration at `~/.config/apimgr/gitmessages/cli.yml`.

```sh
gitmessages-cli --server https://gitmessages.example types
gitmessages-cli --server https://gitmessages.example random
git commit -m "$(gitmessages-cli random)"
gitmessages-cli --server https://gitmessages.example build --type feat --scope api --description 'add message search'
```

Use `--json` before the command to print JSON output. Select a language with `--lang`, `GITMESSAGES_LANG`, the `language` setting in `cli.yml`, or `LANG`.

## Build and test

Go commands run through the `casjaysdev/go:latest` container using Make targets:

```sh
make dev
make local
make build
make test
```

## Configuration

Configuration is optional. Server defaults are documented in `src/config/config.go`; no database or account settings are used by this product. The CLI creates its config directory with user-only permissions and writes `cli.yml` with mode `0600`.

## Security and privacy

The catalog is embedded and no user-submitted content is stored. All builder fields are validated on the server. Public endpoints include security headers, CORS, request size limits, and per-client rate limiting. The service does not call external services at runtime.

## Development

See [the developer documentation](docs/development.md) for repository structure and contribution guidance.

## Disclaimer

This software is provided "as is" without warranty of any kind. Use it at your own risk. The authors are not responsible for damages, data loss, or issues resulting from its use. The catalog is a reference and does not replace your repository's contribution rules. Evaluate the software before production use.

## License

MIT. See [LICENSE.md](LICENSE.md).
