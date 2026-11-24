# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Makefile targets in Actions or install tools inline.
- Never use an unpinned third-party action or Dependabot.

## CRITICAL - ALWAYS DO

- Run Go checks in `casjaysdev/go:latest`, pin Actions by full SHA, and scan dependencies and images.
- Keep CI behavior equivalent across repository providers and verify remote runs after a push.
- Ship equivalent CI, release, and security gates for GitHub, GitLab, Gitea, Forgejo, and Jenkins; Go projects use the maintained Go image without a project toolchain image.
- Include checksums, SBOM, release notes, and platform-supported provenance for public releases.

## Key rules

For complete details, see AI.md PART 27.
