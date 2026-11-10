# CLI reference

`gitmessages-cli` connects to the public API. Set a server with `--server`, `GITMESSAGES_SERVER_PRIMARY`, or the CLI config file. `--lang CODE`, `GITMESSAGES_LANG`, the CLI config, and `LANG` select output language in that order; supported languages are English (`en`), Spanish (`es`), Chinese (`zh`), French (`fr`), Arabic (`ar`), German (`de`), and Japanese (`ja`). The CLI sends the selected language with API requests. Unsupported values fall back to English. `--json` prints the wrapped JSON result. The `random` command prints only the message, so it can be used in command substitution.

```sh
gitmessages-cli --server https://gitmessages.example types
gitmessages-cli --server https://gitmessages.example conventions
gitmessages-cli --server https://gitmessages.example templates
git commit -m "$(gitmessages-cli random)"
gitmessages-cli --server https://gitmessages.example build --type fix --scope parser --description 'handle empty input'
```

Build options are `--type`, `--scope`, `--description`, `--body`, `--breaking`, and `--convention`. Supported conventions are `conventional-commits` and `angular`. `--help` and `--version` exit immediately.
