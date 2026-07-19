# Project Rules (PART 1, 2, 3)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never commit AI attribution (no "Generated with", no Co-Authored-By AI trailers)
- Never use a license other than MIT for this project
- Never invent a project structure that deviates from PART 3's tree without a documented gap
- Never place required root files outside their prescribed location

## CRITICAL - ALWAYS DO
- LICENSE.md must be full MIT text plus a third-party license attribution table
- CLAUDE.md must exist at project root as the primary loader (not gitignored)
- Required root dirs: `.github/`, `.claude/` (gitignored), `docs/`, `src/`,
  `scripts/`, `tests/`, `docker/`, `volumes/` (gitignored), `binaries/` (gitignored),
  `releases/` (gitignored)
- `tests/run_tests.sh`, `tests/docker.sh`, `tests/incus.sh` are all REQUIRED
- `.gitignore` must start with `# gitignore created on MM/DD/YY at HH:MM` then `ignoredirmessage`
- `.dockerignore` must exclude `.git/`, `volumes/`, `binaries/`, `releases/`,
  `tests/`, `docs/`, `*.md`, `Makefile` — but never exclude `src/`, `go.mod/go.sum`, `docker/`

## Summary
Project identity: project_name=gitmessages, org=apimgr, binary=gitmessages,
client_binary=gitmessages-cli, license=MIT. All paths resolve from project
root (`git rev-parse --show-toplevel`), never cwd.

For complete details, see AI.md PART 1, 2, 3.
