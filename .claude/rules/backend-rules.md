# Backend Rules (PART 7-20, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use `mattn/go-sqlite3` (CGO) — use `modernc.org/sqlite` (pure Go)
- Never use `gorilla/mux` — use `github.com/go-chi/chi/v5`
- Never enable CGO — all builds are `CGO_ENABLED=0`
- Never install Go on the host or run `go` directly — always via Docker/Makefile

## CRITICAL - ALWAYS DO
- All builds run through `casjaysdev/go:latest` via `make dev`/`make build`/`make test`
- Use pure-Go libraries only (CGO_ENABLED=0 compatible)
- Follow middleware execution order exactly as specified (URL normalize →
  RequestID → PathSecurity → SecurityHeaders → Allowlist → Blocklist →
  RateLimit → GeoIP → Auth → Logging)
- Self-heal on recoverable errors; only DB connection failure and disk
  write failure trigger maintenance mode

## Summary
Backend must be pure Go, chi-router based, CGO-free, and built exclusively
through the Docker toolchain — never on the host.

For complete details, see AI.md PART 7-20.
