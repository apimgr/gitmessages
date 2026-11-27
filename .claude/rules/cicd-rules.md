# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never use unpinned third-party workflow actions, expose secrets to untrusted PR code, or add Dependabot.
- Never install toolchains inline in workflow jobs or invoke Make targets from CI.
- Never treat local workflow parsing as a substitute for checking the real post-push run.

## CRITICAL - ALWAYS DO

- Provide equivalent required security, test, lint, build, and release workflows for the detected provider.
- Pin actions to full commit SHAs and use least-privilege job permissions.
- Run secret and vulnerability scanning; fail on critical/high vulnerabilities as specified.
- Verify all triggered provider CI runs after each push and fix failures before completion.

## Key rules

- Go builds use `casjaysdev/go:latest`; workflow jobs explicitly set artifact identity variables where required.
- Keep provider workflows, release flows, and local gates aligned with files and targets that actually exist.

For complete details, see AI.md PART 27.
