# Testing, Docs, and i18n Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Go checks on the host or place documentation outside `docs/`.
- Never hardcode human-facing strings in production UI without translation keys.

## CRITICAL - ALWAYS DO

- Keep Go unit tests beside their source; run integration and browser E2E checks in isolated environments.
- Document browser, CLI, API, config, security, integrations, and development surfaces.
- Keep translations complete and in parity across binaries.

## Key rules

For complete details, see AI.md PART 28, PART 29, and PART 30.
