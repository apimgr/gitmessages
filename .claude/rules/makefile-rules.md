# Makefile Rules (PART 37-38, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run `go build`/`go test` directly on the host
- Never hardcode a Go version in the Makefile — always latest stable via the
  toolchain image

## CRITICAL - ALWAYS DO
- `make dev` / `make build` / `make test` are the only sanctioned entry points
- All targets shell out to Docker using `casjaysdev/go:latest`
- Mount `.git` with `-e GOFLAGS=-buildvcs=false` to avoid UID mismatch failures

## Summary
The Makefile is the sole interface between the developer and the Docker-based
Go toolchain — no direct host Go invocations anywhere.

For complete details, see AI.md PART 37-38.
