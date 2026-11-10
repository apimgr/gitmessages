# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never build with CGO enabled or derive project identity from the invoked binary name.
- Never make the CLI execute shell commands using user-provided values.

## CRITICAL - ALWAYS DO

- Build static server and companion CLI binaries with matching release versions.
- Respect the actual invoked binary name in help/version output; use `NO_COLOR` where color is added.
- Keep API clients unauthenticated because the catalog is public.

## Key rules

The CLI reads its server from flag, environment, then user config. For complete details, see AI.md PART 7, PART 8, and PART 32.
