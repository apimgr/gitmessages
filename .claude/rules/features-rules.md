# Feature Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add email, GeoIP, analytics, backup, update, or scheduling behavior outside AI.md's shared server requirements.

## CRITICAL - ALWAYS DO

- Treat the embedded catalog as immutable release data and document any future update path.
- Expose only operational metrics that are safe for public viewing.
- Keep metrics internal and per-service token-protected; do not expose metrics as unauthenticated MCP tools.
- Back up applicable operational data with required encryption and integrity checks; never back up catalog submissions or user accounts.

## Key rules

The embedded catalog remains immutable; implement shared server features defined by AI.md. For complete details, see AI.md PART 17 through PART 22.
