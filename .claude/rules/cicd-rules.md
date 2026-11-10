# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Makefile targets in Actions or install tools inline.
- Never use an unpinned third-party action or Dependabot.

## CRITICAL - ALWAYS DO

- Run Go checks in `casjaysdev/go:latest`, pin Actions by full SHA, and scan dependencies and images.
- Keep CI behavior equivalent across repository providers and verify remote runs after a push.

## Key rules

For complete details, see AI.md PART 27.
