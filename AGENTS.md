# Project SPEC

`AI.md` is the complete implementation specification and is read-only. `IDEA.md` defines Git Messages product behavior. Read both before changing implementation and keep the browser, REST, GraphQL, CLI, OpenAPI, and documentation surfaces aligned.

Go builds, tests, and linters run only in `casjaysdev/go:latest` containers. Never commit credentials, weaken public API security, or add user accounts/submissions: the product is a public, read-only curated reference with a stateless message builder.

Read the applicable `.claude/rules/` summary, then follow the referenced AI.md PART in full. Never guess product behavior. Tests live beside Go source; executable integration checks live under `tests/`.
