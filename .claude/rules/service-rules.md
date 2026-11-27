# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run privileged operations with unchecked arguments or silently fall back to an unsafe escalation path.
- Never claim a service is installed, running, or healthy without querying the operating system's actual state.
- Never place credentials in service unit arguments or world-readable files.

## CRITICAL - ALWAYS DO

- Separate unprivileged runtime work from narrowly scoped privileged installation and lifecycle work.
- Use the platform-specific service manager and documented service names/paths.
- Verify service changes and return actionable errors with the required exit status.

## Key rules

- Service support must be complete on the operating systems and init systems specified by AI.md.
- Preserve user data/configuration on uninstall unless the contract explicitly says otherwise.

For complete details, see AI.md PART 23, 24.
