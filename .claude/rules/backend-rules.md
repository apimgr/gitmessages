# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never concatenate untrusted values into SQL, commands, paths, or HTML.
- Never use `SELECT *` in application code or log credentials, secrets, private content, or raw tokens.
- Never weaken auth, ownership, rate-limit, encryption, or network-boundary checks to simplify a flow.

## CRITICAL - ALWAYS DO

- Return structured errors, use bounded caches and I/O, and handle every meaningful error.
- Use SQLite through the approved pure-Go driver and support the configured remote libSQL mode.
- Hash bearer tokens before storage; encrypt recoverable token copies and sensitive report data at rest.
- Keep security events auditable and redact sensitive fields before logging.

## Key rules

- Database migrations are additive and preserve existing data; shared and local cache schemas stay aligned.
- App secrets and rotation bookkeeping follow PART 11 and are restored with backups.
- Tor and I2P expose the same service without weakening request validation or leaking visitor identity.

For complete details, see AI.md PART 9, 10, 11, 31.
