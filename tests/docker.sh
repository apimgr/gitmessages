#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR=$(git rev-parse --show-toplevel)
PROJECT_NAME=$(basename "$PROJECT_DIR")
PROJECT_ORG=$(awk -F ': *' '$1 == "project_org" {print $2; exit}' "$PROJECT_DIR/IDEA.md")
INTERNAL_NAME=$(awk -F ': *' '$1 == "internal_name" {print $2; exit}' "$PROJECT_DIR/IDEA.md")
DOCKER_TIMEOUT=${DOCKER_TIMEOUT:-3600}
DOCKER_MEM=${DOCKER_MEM:-4g}
DOCKER_CPUS=${DOCKER_CPUS:-2}
if [ -z "$PROJECT_ORG" ] || [ -z "$INTERNAL_NAME" ]; then
  printf '%s\n' 'IDEA.md must define project_org and internal_name.' >&2
  exit 78
fi
TMP_PARENT=${TMPDIR:-/tmp}/$PROJECT_ORG
mkdir -p "$TMP_PARENT"
TEST_TMP_DIR=$(mktemp -d "$TMP_PARENT/${INTERNAL_NAME}-XXXXXX")
chmod 0700 "$TEST_TMP_DIR"
cleanup() { python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$TEST_TMP_DIR"; }
trap cleanup EXIT INT TERM
MODULE_CACHE=${GO_CACHE:-${GO_MODULE_CACHE:-"$HOME/go/pkg/mod"}}
BUILD_CACHE=${GO_BUILD:-${GO_BUILD_CACHE:-"$HOME/.cache/go-build/$PROJECT_NAME"}}
mkdir -p "$TEST_TMP_DIR/source" "$TEST_TMP_DIR/binaries" "$MODULE_CACHE" "$BUILD_CACHE"
random_suffix() { printf '%04x%04x' "$RANDOM" "$RANDOM"; }
tar -C "$PROJECT_DIR" --exclude=.git --exclude=binaries --exclude=releases -cf - . | tar -C "$TEST_TMP_DIR/source" -xf -

timeout -k 30 "$DOCKER_TIMEOUT" docker run --rm --no-healthcheck \
  --name "${PROJECT_NAME}-$(random_suffix)" --memory="$DOCKER_MEM" --cpus="$DOCKER_CPUS" \
  -e CGO_ENABLED=0 \
  -e GOFLAGS=-buildvcs=false \
  -e GOMODCACHE=/usr/local/share/go/pkg/mod \
  -e GOCACHE=/usr/local/share/go/cache \
  -e PROJECT_NAME="$PROJECT_NAME" \
  -e PROJECT_ORG="$PROJECT_ORG" \
  -v "$TEST_TMP_DIR/source:/workspace" \
  -v "$TEST_TMP_DIR/binaries:/out" \
  -v "$MODULE_CACHE:/usr/local/share/go/pkg/mod" \
  -v "$BUILD_CACHE:/usr/local/share/go/cache" \
  -w /workspace \
  casjaysdev/go:latest sh -c "go build -mod=readonly -buildvcs=false -trimpath -ldflags \"-s -w\" -o \"/out/\$PROJECT_NAME\" ./src && go build -mod=readonly -buildvcs=false -trimpath -ldflags \"-s -w\" -o \"/out/\$PROJECT_NAME-cli\" ./src/client"

timeout -k 30 "$DOCKER_TIMEOUT" docker run --rm --no-healthcheck \
  --name "${PROJECT_NAME}-$(random_suffix)" --memory="$DOCKER_MEM" --cpus="$DOCKER_CPUS" \
  -e PROJECT_NAME="$PROJECT_NAME" \
  -e PROJECT_ORG="$PROJECT_ORG" \
  -e INTERNAL_NAME="$INTERNAL_NAME" \
  -v "$TEST_TMP_DIR/binaries:/app:ro" \
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
mkdir -p "${TMPDIR:-/tmp}/${PROJECT_ORG}"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/${PROJECT_ORG}/${PROJECT_NAME}-XXXXXX")
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
  sed -E 's/([Oo]perator token[^:]*: ).*/\1[redacted]/; s/(([Tt]oken|[Pp]assword|[Ss]ecret|[Aa]pi[_-]?key|[Aa]uthorization)[=: ]+)[^ ,;]+/\1[redacted]/g; s/(adm_|own_|mtr_)[A-Za-z0-9_-]+/\1[redacted]/g' "$test_dir/server.log" >&2
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
curl -q -LSsf -H 'Content-Type: application/json' -d '{"query":"{ types { type description emoji example breaking } }"}' "$base/api/graphql" | jq -e '.data.types | length > 0' >/dev/null
curl -q -LSsf -H 'Content-Type: application/json' -d '{"type":"feat","scope":"test","description":"exercise builder"}' "$base/api/v1/messages" | jq -e '.ok == true' >/dev/null
code=$(curl -q -LSs -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: tools/list' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}' "$base/mcp")
test "$code" = 401
owner_token=$(curl -q -LSsf -H 'Content-Type: application/json' -d '{}' "$base/api/v1/server/preferences/tokens/primary" | jq -er '.data.owner_token')
curl -q -LSsf -H "Authorization: Bearer $owner_token" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: tools/list' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}' "$base/mcp" | jq -e '.result.tools | length > 0' >/dev/null
unset owner_token
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
