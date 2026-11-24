# Development

The source is organized under `src/`. Curated catalog data lives in `src/messages/data/` and is embedded into the binaries. The `src/web/` package serves the SSR site, REST API, and OpenAPI/PWA routes. `src/graphql/` defines the GraphQL schema and resolver. `src/client/` contains the companion CLI.

Use Docker-backed Make targets; do not install or invoke Go directly on the host:

```sh
make dev
make local
make build
make test
```

The CI lint gate runs `go vet` and `staticcheck` in the same Go toolchain container. To run it locally:

```sh
docker run --rm --name "gitmessages-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" --no-healthcheck -v "$PWD:/app" -w /app casjaysdev/go:latest sh -c 'go vet ./... && staticcheck ./...'
```

## Binary integration checks

The `make test` target is the Phase 1 commit gate for source-level Go tests. Phase 2 checks are manual and run compiled binaries inside isolated containers. They build from a temporary source copy and clean up their containers and temporary files on exit; E2E preserves redacted failure artifacts in a project-scoped temporary directory.

```sh
./tests/run_tests.sh
./tests/docker.sh
./tests/incus.sh [systemd|openrc|all]
./tests/e2e.sh
```

`run_tests.sh` runs the basic Docker checks and exits with status 69 when Docker is unavailable; there is no Incus fallback because the Incus runner also needs Docker to build its test binaries. `docker.sh` validates both binaries and exercises the server routes and CLI in Alpine. `incus.sh` checks service behavior under systemd and OpenRC; it runs both modes by default. The separate `e2e.sh` suite runs the browser tests through an isolated Docker network with a Chromium sidecar. It covers server-rendered, no-JavaScript, and full-browser behavior. E2E is on demand for beta testing and is not part of the commit gate.

When changing catalog fields or API behavior, keep the REST API, GraphQL schema, OpenAPI output, browser pages, CLI output, and this documentation aligned. Add tests for behavior changes and maintain the CI coverage threshold.
