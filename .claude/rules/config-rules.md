# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never store operator secrets in ordinary configuration, drop-ins, source code, or logs.
- Never silently accept invalid configuration values or let empty values accidentally override defaults.
- Never enable debug mode implicitly from development mode or deployment environment.

## CRITICAL - ALWAYS DO

- Use canonical `server.*` keys and the specified file-only configuration layout.
- Validate values at load time, report invalid values safely, and apply documented defaults.
- Preserve the precedence and restart/live-reload rules defined in AI.md.

## Key rules

- Keep secrets in `secrets.yml`; ordinary settings belong in `server.yml` or their documented `yaml.d/` area file.
- Mode and debug flag are independent; explicit flags and environment values follow the specified precedence.
- Config changes must be atomic, validated, and safely reloaded according to the setting's contract.

For complete details, see AI.md PART 5, 6, 12.
