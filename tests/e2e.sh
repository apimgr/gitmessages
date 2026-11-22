#!/usr/bin/env bash
set -euo pipefail

project_dir=$(git rev-parse --show-toplevel)
project_name=$(basename "$project_dir")
go_cache=${GO_CACHE:-$HOME/go/pkg/mod}
go_build=${GO_BUILD:-$HOME/.cache/go-build/$project_name}
mkdir -p "$go_cache" "$go_build"

if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'Docker is required for the on-demand browser E2E suite.' >&2
  exit 69
fi

docker run --rm \
  --name "${project_name}-e2e-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
  -v "$project_dir:/app" \
  -v "$go_cache:/go/pkg/mod" \
  -v "$go_build:/root/.cache/go-build" \
  -w /app \
  -e CGO_ENABLED=0 \
  -e GOFLAGS=-buildvcs=false \
  casjaysdev/go:latest \
  go test -tags e2e ./tests/e2e/...
