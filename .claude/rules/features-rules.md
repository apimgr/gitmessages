# Feature Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add email, GeoIP, analytics, backup, update, or scheduling behavior not defined by IDEA.md.

## CRITICAL - ALWAYS DO

- Treat the embedded catalog as immutable release data and document any future update path.
- Expose only operational metrics that are safe for public viewing.

## Key rules

No external runtime services or persisted product data are in scope. For complete details, see AI.md PART 17 through PART 22.
