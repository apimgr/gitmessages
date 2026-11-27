# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never place the production Dockerfile or compose files at the repository root.
- Never bake secrets, mutable runtime data, or CI-only files into the image.
- Do not set a Dockerfile `USER` or create the runtime account in the Dockerfile. The container starts as root so the entrypoint can prepare ownership and permissions; the application then drops to its dedicated account after privileged initialization.

## CRITICAL - ALWAYS DO

- Keep Docker definitions under `docker/` and use the required multi-stage build and rootfs overlay.
- Apply OCI metadata, permissions, tini/entrypoint, health checks, and runtime user as specified.
- Use the prescribed network, port binding, volume, logging, and compose profile conventions.

## Key rules

- Release and development images remain distinct; build-only tools do not ship in runtime layers.
- `.dockerignore` excludes build/CI/runtime artifacts while retaining modules and required Docker assets.

For complete details, see AI.md PART 26.
