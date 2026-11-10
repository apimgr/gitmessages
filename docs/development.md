# Development

The source is organized under `src/`. Curated catalog data lives in `src/messages/data/` and is embedded into the binaries. The `src/web/` package serves the SSR site, REST API, and OpenAPI/PWA routes. `src/graphql/` defines the GraphQL schema and resolver. `src/client/` contains the companion CLI.

Use Docker-backed Make targets; do not install or invoke Go directly on the host:

```sh
make dev
make local
make build
make test
```

When changing catalog fields or API behavior, keep the REST API, GraphQL schema, OpenAPI output, browser pages, CLI output, and this documentation aligned. Add tests for behavior changes and maintain the CI coverage threshold.
