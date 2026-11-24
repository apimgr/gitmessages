# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never create a web administration panel or runtime settings mutation API.
- Never trust unvalidated paths, forwarded headers, or environment values.

## CRITICAL - ALWAYS DO

- Keep server settings in `server.yml`; document defaults and environment/flag precedence.
- Treat `server.yml`, `yaml.d/`, `secrets.yml`, and `assets/` as watched inputs; persist generated secrets only in `secrets.yml`.
- Apply safe production defaults and make development mode explicit.
- Reload safe settings live and report restart-required changes for listener, TLS, database, daemonize, PID, and Tor configuration.

## Key rules

IDEA.md prohibits runtime administration. For complete details, see AI.md PART 5, PART 6, and PART 12.
