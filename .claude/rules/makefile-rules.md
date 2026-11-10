# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Go on the host or mutate release versions as a build side effect.
- Never make CI invoke Makefile targets.

## CRITICAL - ALWAYS DO

- Use the `casjaysdev/go:latest` container for all Go commands and mount module/build caches.
- Keep the required targets focused on dev, local, build, test, release, docker, and clean.

## Key rules

For complete details, see AI.md PART 25.
