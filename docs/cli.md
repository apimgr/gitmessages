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

The server also accepts `--service {start|stop|restart|reload|status|--install|--uninstall|--disable}` and `--maintenance {backup|restore|update|mode|setup|token}`. Update commands are `--update [check|yes|branch {stable|beta|devel}]`.

`--lang CODE` selects the default interface language for requests served by this process. A request's supported `?lang=`, language cookie, and `Accept-Language` preference take precedence over the configured process default. `--color no` sets `NO_COLOR`; `--color yes` clears it.

`--baseurl PATH` serves routes and generated links under a path prefix, overriding `server.baseurl` for this process. For example, `--baseurl /gitmessages` serves the app below `/gitmessages/`. Trusted reverse-proxy prefix headers take precedence.

```sh
gitmessages --shell completions bash
gitmessages --shell init zsh
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
