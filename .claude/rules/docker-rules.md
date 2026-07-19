# Docker Rules (PART 39-40, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never maintain two conflicting `docker-compose.yml` files — only
  `docker/docker-compose.yml` is canonical per the PART 3 structure tree
- Never exclude `src/`, `go.mod`/`go.sum`, or `docker/` in `.dockerignore`

## CRITICAL - ALWAYS DO
- `docker/` holds `Dockerfile`, `Dockerfile.dev`, `docker-compose.yml`,
  `docker-compose.dev.yml`, `docker-compose.test.yml`, `rootfs/`
- Container paths are always `/config/{project_name}/` and `/data/{project_name}/`
- Internal container port is `80`

## Summary
This project currently has a duplicate, conflicting root-level
`docker-compose.yml` (different ports/volumes/env than `docker/docker-compose.yml`,
plus a stray `DB_TYPE=sqlite` reference) — flagged as a gap requiring a decision.

For complete details, see AI.md PART 39-40.
