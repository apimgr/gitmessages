# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never copy or symlink host-built binaries into an image.
- Never bake runtime configuration or secrets into the image.

## CRITICAL - ALWAYS DO

- Compile in a multi-stage container build and run through tini with a health check.
- Keep Compose production, development, and test modes distinct.
- Publish container ports on `172.17.0.1`; use `/config` and `/data` volumes, and keep persistent state aligned with each compose mode.

## Key rules

The production image contains only the server runtime. For complete details, see AI.md PART 26.
