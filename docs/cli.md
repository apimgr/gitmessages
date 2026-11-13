# Command-line reference

## Server binary

The `gitmessages` server accepts `-h`/`--help` and `-v`/`--version`. Its shared output flags are `--debug`, `--color auto|yes|no`, `--lang CODE`, and `--shell completions|init [SHELL]`. Both binaries print a stable version line followed by build date, Go version, and OS/architecture metadata. Shell integration supports bash, zsh, fish, sh, dash, ksh, PowerShell, and pwsh; when omitted, the shell is detected from the environment. Use `--shell help` for usage.

`--lang CODE` selects the default interface language for requests served by this process. A request's supported `?lang=`, language cookie, and `Accept-Language` preference take precedence over the configured process default. `--color no` sets `NO_COLOR`; `--color yes` clears it.

```sh
gitmessages --shell completions bash
gitmessages --shell init zsh
gitmessages --mode debug --debug
```

## Client binary

`gitmessages-cli` connects to the public API. Set a server with `--server`, `GITMESSAGES_SERVER_PRIMARY`, or the CLI config file. `--lang CODE`, `GITMESSAGES_LANG`, the CLI config, and `LANG` select output language in that order; supported languages are English (`en`), Spanish (`es`), Chinese (`zh`), French (`fr`), Arabic (`ar`), German (`de`), and Japanese (`ja`). The CLI sends the selected language with API requests. Unsupported values fall back to English. `--json` prints the wrapped JSON result. The `random` command prints only the message, so it can be used in command substitution.

```sh
gitmessages-cli --server https://gitmessages.example types
gitmessages-cli --server https://gitmessages.example conventions
gitmessages-cli --server https://gitmessages.example templates
git commit -m "$(gitmessages-cli random)"
gitmessages-cli --server https://gitmessages.example build --type fix --scope parser --description 'handle empty input'
```

Build options are `--type`, `--scope`, `--description`, `--body`, `--breaking`, and `--convention`. Supported conventions are `conventional-commits` and `angular`. `--help` and `--version` exit immediately.
