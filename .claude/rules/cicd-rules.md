# CI/CD Rules (PART 41-42, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never pin third-party GitHub Actions to a tag — always a full commit SHA
- Never create `ci.yml`/`release.yml` before security-only workflows

## CRITICAL - ALWAYS DO
- `.github/workflows/` requires `release.yml`, `beta.yml`, `daily.yml`, `docker.yml`
- Verify each staged workflow with `act --list -W {file}` before committing
- SHA-pin annotations stay inline (`# vX.Y.Z`) — the one inline-comment exception

## Summary
This project's `.github/` directory currently exists but is empty — no
workflows present. Flagged as a gap.

For complete details, see AI.md PART 41-42.
