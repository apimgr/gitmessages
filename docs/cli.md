# Command-line reference

## Server binary

The `gitmessages` server accepts `-h`/`--help` and `-v`/`--version`. Its shared output flags are `--debug`, `--color auto|yes|no`, `--lang CODE`, and `--shell completions|init [SHELL]`. Both binaries print a stable version line followed by build date, Go version, and OS/architecture metadata. Shell integration supports bash, zsh, fish, sh, dash, ksh, PowerShell, and pwsh; when omitted, the shell is detected from the environment. Use `--shell help` for usage.

Command help accepts either `<command-flag> --help` (or `-h`) or `<command-flag> help`; it runs before normal flag parsing and exits without loading server configuration. For example:

```sh
gitmessages --shell --help
gitmessages --service help
gitmessages --maintenance -h
gitmessages --update help
```

The server also accepts `--service {start|stop|restart|reload|status|--install|--uninstall|--disable}` and `--maintenance {backup|restore|update|mode|setup|pgp|secret|security|token|data|compliance}`. Update commands are `--update [check|yes|branch {stable|beta|devel}]`.

Email commands validate or send operator email using the configured SMTP relay. `email test --dry-run` previews and validates every embedded or overridden template without sending. `email test` sends the test template to `server.notifications.email.reply_to`; `--to ADDRESS` selects a one-off recipient. A real test send requires a working SMTP relay.

```sh
gitmessages email test --dry-run
gitmessages email test --to operator@example.com
```

Maintenance commands that read or change owner or compliance data require root or the operator token from `server.token`. Token prefixes can be found with `--maintenance token list`.

PGP key management is available only through the maintenance CLI. `pgp generate`, `pgp rotate`, `pgp publish`, `pgp export public [path]`, `pgp export private <path>`, `pgp import <file>`, and `pgp delete` manage the project security keypair; private-key import/export and deletion require the sensitive-operation confirmation flow. Keyserver retries run through the scheduled `keyserver_retry` task.

Secret rotation is limited to `installation_secret` and `encryption_key`. Both commands require the sensitive-operation confirmation flow; the old secret remains available for seven days and the old encryption key remains available for 30 days while dependent data is re-encrypted.

Security-report operations are `security list [--status STATUS]`, `security show TRACKING_ID`, `security export TRACKING_ID [--output FILE|-]`, and `security update TRACKING_ID [--status STATUS] [--comment TEXT] [--disclosure-date YYYY-MM-DD] [--severity LEVEL]`. Export writes encrypted report bytes with mode `0600`; update moves status forward, sets researcher-visible updates, and records disclosure credit when applicable.

```sh
gitmessages --maintenance data export --owner own_XXXXXXXX
gitmessages --maintenance data export --owner own_XXXXXXXX --output ./owner-export.json
gitmessages --maintenance data delete --owner own_XXXXXXXX
gitmessages --maintenance data delete --owner own_XXXXXXXX --yes
gitmessages --maintenance compliance report
```

Owner exports contain the owner timestamps, token metadata, and owned resources; they never include token values, token hashes, or audit/security log lines. The default export is created in the current directory with mode `0600`; use `--output -` to write JSON to stdout. Owner deletion asks for the owner ID unless `--yes` is provided. The compliance report shows configured standards, retention settings, aggregate counts, recent compliance-event counts, and the last backup time.

`--lang CODE` selects the default interface language for requests served by this process. A request's supported `?lang=`, language cookie, and `Accept-Language` preference take precedence over the configured process default. `--color no` sets `NO_COLOR`; `--color yes` clears it.

`--baseurl PATH` serves routes and generated links under a path prefix, overriding `server.baseurl` for this process. For example, `--baseurl /gitmessages` serves the app below `/gitmessages/`. Trusted reverse-proxy prefix headers take precedence.

```sh
gitmessages --mode debug --debug
gitmessages --baseurl /gitmessages
```

`--debug` enables diagnostic logging and `/debug/*` for that process. `MODE=debug` enables diagnostics by default, while `DEBUG=false` or `--debug=false` disables them. Debug handlers still require the operator bearer token from `server.token`; they return 404 when diagnostics are disabled. See [Debug diagnostics](operations.md#debug-diagnostics) for the protected endpoints and configuration.

## Client binary

`gitmessages-cli` connects to the public API. Set a server with `--server`, `GITMESSAGES_SERVER_PRIMARY`, or the CLI config file. `--token` supplies a Bearer token and saves it to `cli.yml` only when no token is already configured. The CLI config directory and `cli.yml` use owner-only permissions. Output language precedence is `--lang`, CLI config `language`, `LC_ALL`, `LANG`, then English (`en`); `language: auto` uses the environment. Supported languages are English (`en`), Spanish (`es`), French (`fr`), German (`de`), Italian (`it`), Arabic (`ar`), Chinese (`zh`), and Japanese (`ja`). The CLI sends the selected language with API requests. Unsupported values fall back to English. `--json` prints the wrapped JSON result. The `random` command prints only the message, so it can be used in command substitution.

```sh
GITMESSAGES_SERVER=http://127.0.0.1:8080
gitmessages-cli --server "$GITMESSAGES_SERVER" types
gitmessages-cli --server "$GITMESSAGES_SERVER" conventions
gitmessages-cli --server "$GITMESSAGES_SERVER" templates
git commit -m "$(gitmessages-cli --server "$GITMESSAGES_SERVER" random)"
gitmessages-cli --server "$GITMESSAGES_SERVER" build --type fix --scope parser --description 'handle empty input'
gitmessages-cli --server "$GITMESSAGES_SERVER" --update check
gitmessages-cli --server "$GITMESSAGES_SERVER" --update yes
```

No official server URL is built into the release. Supply `--server`, set `GITMESSAGES_SERVER_PRIMARY`, or configure `server.primary` in `cli.yml` before running a command.

Build options are `--type`, `--scope`, `--description`, `--body`, `--breaking`, and `--convention`. Supported conventions are `conventional-commits` and `angular`. `--help` and `--version` exit immediately.

The CLI checks for a compatible update on every invocation. `--update check` reports an available version without installing it; `--update yes` installs it immediately. In an interactive terminal, an available update prompts before installation. Non-interactive runs install silently only when `update.auto: true` is set in `cli.yml`. Set `update.check_interval: per_invocation` and `update.channel: stable`; these are the supported values. Updates require HTTPS, a matching server-provided SHA-256, and a writable install directory. The CLI atomically replaces and re-executes itself with the original command. If the server sets `cli_min_version`, an older CLI must update before making API requests.

Operators can provision release assets in `{data_dir}/cli/`: `{project_name}-cli-{os}-{arch}[.exe]`, `version.txt`, and `sha256.txt`. The server advertises only binaries whose SHA-256 matches the checksum file. Downloads are public by default; `cli.binary_download.require_auth: true` requires a Bearer token. See [API autodiscovery](api.md#autodiscovery) for server-side configuration and the download endpoint.

The default CLI is built without GUI dependencies and opens the TUI when display detection identifies an interactive terminal. Optional `gitmessages-cli-gui-{os}-{arch}` builds include the native GUI for Linux, macOS, and Windows; those binaries require the system loader used by the build toolchain and are separate from the static CLI. Set `display.mode` to `auto`, `gui`, or `tui` in `cli.yml` to override automatic selection. Without GUI support, `auto` selects the TUI when a terminal is available; explicitly selecting `gui` reports that GUI support is unavailable. `TERM=dumb` disables TUI selection and ANSI output. The TUI reflows when the terminal is resized and supports `↑`/`↓` or `j`/`k` navigation, `/` to filter actions, `?` for help, Enter to open an action, and `q` or Ctrl+C to exit. Page Up and Page Down scroll long results on small screens.

Shell integration supports bash, zsh, fish, sh, dash, ksh, PowerShell, and pwsh. The optional shell argument is detected from `SHELL` or `ComSpec` when omitted.

```sh
gitmessages-cli --shell completions [SHELL]
gitmessages-cli --shell init [SHELL]
gitmessages-cli --shell help
```
