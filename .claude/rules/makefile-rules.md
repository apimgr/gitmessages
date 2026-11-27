# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never compile Go on the host or install a host Go toolchain as a workaround.
- Never use Make targets from CI workflows where AI.md requires explicit commands.
- Never let cleanup delete user data or runtime directories not created by the target.

## CRITICAL - ALWAYS DO

- Provide the required local development, build, test, lint, Docker, release, and clean targets.
- Run Go targets in `casjaysdev/go:latest` with `CGO_ENABLED=0` and the prescribed cache mounts.
- Derive project variables from IDEA.md, the Git remote, then the documented path fallback.

## Key rules

- Keep targets bounded, reproducible, and safe; document variables and target behavior in `make help`.
- Preserve the distinction between local Makefile workflows and provider-native CI.

For complete details, see AI.md PART 25.
