#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR=$(git rev-parse --show-toplevel)
PROJECT_NAME=$(basename "$PROJECT_DIR")
GO_CACHE=${GO_CACHE:-$HOME/go/pkg/mod}
GO_BUILD=${GO_BUILD:-$HOME/.cache/go-build/$PROJECT_NAME}

mkdir -p "$GO_CACHE" "$GO_BUILD"
make -C "$PROJECT_DIR" build

docker run --rm \
  --name "${PROJECT_NAME}-test-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
  -e PROJECT_NAME="$PROJECT_NAME" \
  -v "$PROJECT_DIR/binaries:/app:ro" \
  alpine:latest sh -s <<'CONTAINER_TEST'
set -eu
apk add --no-cache bash ca-certificates curl file jq tor i2pd tzdata

server="/app/${PROJECT_NAME}"
client="/app/${PROJECT_NAME}-cli"
test -x "$server"
test -x "$client"

echo '=== Binary version and help ==='
"$server" --version
"$server" --help | grep -F -- '--port'
"$client" --version
"$client" --help
file "$server" | grep -F 'statically linked'

echo '=== Server and route coverage ==='
mkdir -p "${TMPDIR:-/tmp}/apimgr"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/apimgr/gitmessages-XXXXXX")
chmod 0700 "$test_dir"
trap 'if [ -n "${server_pid:-}" ]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi; rm -rf "$test_dir"' EXIT
"$server" --config "$test_dir/config" --port 64580 --address 127.0.0.1 >"$test_dir/server.log" 2>&1 &
server_pid=$!
base=http://127.0.0.1:64580
ready=false
for attempt in $(seq 1 60); do
  if curl -q -LSsf --max-time 2 "$base/api/v1/server/healthz" >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  cat "$test_dir/server.log" >&2
  exit 1
fi

for route in / /types /templates /builder /server/about /server/help /server/help/mcp /server/privacy /server/terms /server/healthz /server/docs/swagger /server/docs/graphql; do
  curl -q -LSsf --max-time 10 "$base$route" >/dev/null
done
for route in /api/v1 /api/v1/types /api/v1/conventions /api/v1/templates /api/v1/templates/random /api/v1/messages/random /api/v1/server/healthz /api/v1/server/about /api/v1/server/help /api/v1/server/help/mcp /api/v1/server/swagger /api/swagger; do
  curl -q -LSsf --max-time 10 "$base$route" >/dev/null
done
curl -q -LSsf -H 'Accept: text/plain' "$base/api/v1/types" >/dev/null
curl -q -LSsf "$base/api/v1/server/healthz.txt" >/dev/null
curl -q -LSsf -H 'Accept: application/json' "$base/server/healthz" >/dev/null
curl -q -LSsf -H 'Accept: text/plain' "$base/server/healthz" >/dev/null
curl -q -LSsf -H 'Content-Type: application/json' -d '{"query":"{ types { type description emoji example breaking } }"}' "$base/graphql" | jq -e '.data.types | length > 0' >/dev/null
curl -q -LSsf -H 'Content-Type: application/json' -d '{"type":"feat","scope":"test","description":"exercise builder"}' "$base/api/v1/messages" | jq -e '.ok == true' >/dev/null
curl -q -LSsf -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: tools/list' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}' "$base/mcp" | jq -e '.result.tools | length > 0' >/dev/null
code=$(curl -q -LSs -o /dev/null -w '%{http_code}' "$base/missing-route")
test "$code" = 404
code=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X DELETE "$base/api/v1/types")
test "$code" = 405

echo '=== Client against server ==='
"$client" --server "$base" types >/dev/null
"$client" --server "$base" conventions >/dev/null
"$client" --server "$base" templates >/dev/null
"$client" --server "$base" random >/dev/null
"$client" --server "$base" build --type feat --scope test --description 'exercise cli' >/dev/null

echo '=== Rate limit ==='
limited=false
for attempt in $(seq 1 140); do
  code=$(curl -q -LSs -o /dev/null -w '%{http_code}' "$base/api/v1/types")
  if [ "$code" = 429 ]; then
    limited=true
    break
  fi
done
test "$limited" = true

echo 'Docker binary integration checks passed.'
CONTAINER_TEST
