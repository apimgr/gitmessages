# Binary & Mode Rules (PART 6)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never tie debug endpoint availability directly to `development` mode — debug
  endpoints are gated ONLY by the separate `--debug`/`DEBUG=true` flag
- Never let `--debug`/`DEBUG=true` bypass authentication or security checks, in
  any mode, including production
- Never default mode to anything other than `production`

## CRITICAL - ALWAYS DO
- Mode priority: `--mode` flag > `MODE` env var > default `production`
- Debug priority: `--debug` flag > `DEBUG` env var (truthy) > `--mode debug` alias > default `false`
- Support four operational states: production, production+debug, development,
  development+debug — debug is an orthogonal flag, not a mode value
- Support mode shortcuts: `dev`, `devel`, `development`, `prod`, `production`, `debug`
  (`debug` expands to development + debug on)
- `/debug/*` and `/debug/pprof/*` return 404 unless debug is explicitly enabled

## Summary
Mode and Debug are two independent axes. This project's `src/mode/mode.go`
currently only implements Mode (production/development) and ties debug
endpoints to Development mode directly — missing the independent Debug flag
and the `devel`/`debug` shortcuts. Flagged as a gap.

For complete details, see AI.md PART 6.
