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

The server also accepts `--service {start|stop|restart|reload|status|--install|--uninstall|--disable}` and `--maintenance {backup|restore|update|mode|setup|token|data|compliance}`. Update commands are `--update [check|yes|branch {stable|beta|devel}]`.

Email commands validate or send operator email using the configured SMTP relay. `email test --dry-run` previews and validates every embedded or overridden template without sending. `email test` sends the test template to `server.notifications.email.reply_to`; `--to ADDRESS` selects a one-off recipient. A real test send requires a working SMTP relay.

```sh
gitmessages email test --dry-run
gitmessages email test --to operator@example.com
```

Maintenance commands that read or change owner or compliance data require root or the operator token from `server.token`. Token prefixes can be found with `--maintenance token list`.

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

## Client binary

`gitmessages-cli` connects to the public API. Set a server with `--server`, `GITMESSAGES_SERVER_PRIMARY`, or the CLI config file. `--lang CODE`, `GITMESSAGES_LANG`, the CLI config, and `LANG` select output language in that order; supported languages are English (`en`), Spanish (`es`), French (`fr`), German (`de`), Italian (`it`), Arabic (`ar`), Chinese (`zh`), and Japanese (`ja`). The CLI sends the selected language with API requests. Unsupported values fall back to English. `--json` prints the wrapped JSON result. The `random` command prints only the message, so it can be used in command substitution.

```sh
GITMESSAGES_SERVER=http://127.0.0.1:8080
gitmessages-cli --server "$GITMESSAGES_SERVER" types
gitmessages-cli --server "$GITMESSAGES_SERVER" conventions
gitmessages-cli --server "$GITMESSAGES_SERVER" templates
git commit -m "$(gitmessages-cli --server "$GITMESSAGES_SERVER" random)"
gitmessages-cli --server "$GITMESSAGES_SERVER" build --type fix --scope parser --description 'handle empty input'
```

No official server URL is built into the release. Supply `--server`, set `GITMESSAGES_SERVER_PRIMARY`, or configure `server.primary` in `cli.yml` before running a command.

Build options are `--type`, `--scope`, `--description`, `--body`, `--breaking`, and `--convention`. Supported conventions are `conventional-commits` and `angular`. `--help` and `--version` exit immediately.

The default CLI is built without GUI dependencies and opens the TUI when stdin and stdout are interactive. Optional `gitmessages-cli-gui-{os}-{arch}` builds include the native GUI for Linux, macOS, and Windows; those binaries require the system loader used by the build toolchain and are separate from the static CLI. Set `display.mode` to `auto`, `gui`, or `tui` in `cli.yml` to override automatic selection. Without GUI support, `auto` selects the TUI when a terminal is available; explicitly selecting `gui` reports that GUI support is unavailable. The TUI reflows when the terminal is resized and supports `↑`/`↓` or `j`/`k` navigation, `/` to filter actions, `?` for help, Enter to open an action, and `q` or Ctrl+C to exit. Page Up and Page Down scroll long results on small screens.

Shell integration supports bash, zsh, fish, sh, dash, ksh, PowerShell, and pwsh. The optional shell argument is detected from `SHELL` or `ComSpec` when omitted.

```sh
gitmessages-cli --shell completions [SHELL]
gitmessages-cli --shell init [SHELL]
gitmessages-cli --shell help
```
