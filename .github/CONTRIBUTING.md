# Contributing

Git Messages is a public, curated reference. Contributions should improve the
catalog, API, CLI, server-rendered pages, accessibility, or documentation while
preserving its read-only, stateless behavior.

## Local workflow

1. Fork `apimgr/gitmessages` and create a focused branch from `main`.
2. Use Go 1.26 or the project build container, `casjaysdev/go:latest`.
3. Run `make build` and `make test` in the supported Go container. Run
   `./tests/run_tests.sh` when Docker or Incus is available. The browser suite
   is on demand with `./tests/e2e.sh`.
4. Preview documentation with `mkdocs serve` and check it with `mkdocs build`
   when changing docs; MkDocs and its plugins are declared in the repository.
5. Open a pull request against `main` with a clear summary and test evidence.

Update tests and user or operator documentation whenever behavior, routes,
configuration, or commands change. Keep REST, GraphQL, CLI, OpenAPI, and web
behavior aligned. Do not add accounts, user submissions, credentials, or
runtime dependencies.

For a vulnerability, do not open a public issue. Use
https://github.com/apimgr/gitmessages/security/advisories/new as described in
[the security policy](SECURITY.md).
