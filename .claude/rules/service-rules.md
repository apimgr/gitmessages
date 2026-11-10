# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never write service units to host paths during tests or builds.
- Never create unsafe privileged service behavior.

## CRITICAL - ALWAYS DO

- Keep service templates platform-specific and use least privilege.
- Ensure service lifecycle operations report failures clearly.

## Key rules

The server is a single static binary. For complete details, see AI.md PART 23 and PART 24.
