# Feature Rules (PART 17–22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never attempt email delivery when SMTP is unconfigured or unavailable.
- Never add external cron/system timers for application maintenance.
- Never store backup passwords in archives, manifests, logs, or database rows.
- Never trust a staged update checksum writable by the service account during privileged installation.

## CRITICAL - ALWAYS DO

- Use customizable embedded email templates and the documented SMTP security and message format.
- Persist and audit scheduler execution; apply bounded retries, timeouts, and retention.
- Attribute and validate GeoIP datasets; expose metrics only behind their configured auth.
- Encrypt backups when configured or required, validate archives before restore, and preserve incremental bases.
- Verify updates against official release metadata in the required privilege phase.

## Key rules

- Email, GeoIP, metrics, backup, restore, and update behavior is configuration-driven and documented.
- Keep operational datasets, generated output, and runtime artifacts in their specified directories.

For complete details, see AI.md PART 17, 18, 19, 20, 21, 22.
