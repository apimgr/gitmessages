# Testing Rules (PART 43+, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never commit with a failing test
- Never skip tests to "save time"
- Never leave `tests/run_tests.sh`, `tests/docker.sh`, `tests/incus.sh` unimplemented

## CRITICAL - ALWAYS DO
- `make test` must pass before every commit
- `tests/` requires `run_tests.sh`, `docker.sh`, `incus.sh` — all REQUIRED, not optional
- Add a test for new behavior that fails before and passes after the change

## Summary
This project's `tests/` directory currently contains only `.gitkeep` — all
three required test scripts are missing. Flagged as a gap requiring real
implementation, not a stub.

For complete details, see AI.md PART 43+.
