#!/usr/bin/env bash
set -euo pipefail

project_dir=$(git rev-parse --show-toplevel)
project_name=$(basename "$project_dir")
project_org=$(awk -F ': *' '$1 == "project_org" {print $2; exit}' "$project_dir/IDEA.md")
internal_name=$(awk -F ': *' '$1 == "internal_name" {print $2; exit}' "$project_dir/IDEA.md")
if [[ -z $project_org || -z $internal_name ]]; then
  printf '%s\n' 'IDEA.md must define project_org and internal_name.' >&2
  exit 78
fi

if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'Docker is required for the on-demand browser E2E suite.' >&2
  exit 69
fi

tmp_parent=${TMPDIR:-/tmp}/$project_org
mkdir -p "$tmp_parent"
test_dir=$(mktemp -d "$tmp_parent/${internal_name}-XXXXXX")
chmod 0700 "$test_dir"
mkdir -p "$test_dir/source" "$test_dir/binaries" "$test_dir/runtime"
# The server drops to its service account, which must traverse this mount point.
chmod 0755 "$test_dir/runtime"
mkdir -p "$test_dir/runtime/artifacts"
chmod 0700 "$test_dir/runtime/artifacts"
tar -C "$project_dir" --exclude=.git --exclude=binaries --exclude=releases -cf - . | tar -C "$test_dir/source" -xf -
module_cache=${GO_CACHE:-${GO_MODULE_CACHE:-"$HOME/go/pkg/mod"}}
build_cache=${GO_BUILD:-${GO_BUILD_CACHE:-"$HOME/.cache/go-build/$project_name"}}
mkdir -p "$module_cache" "$build_cache"
docker_timeout=${DOCKER_TIMEOUT:-3600}
docker_mem=${DOCKER_MEM:-4g}
docker_cpus=${DOCKER_CPUS:-2}
random_suffix() { printf '%04x%04x' "$RANDOM" "$RANDOM"; }
network="${project_name}-$(random_suffix)"
chrome="${project_name}-$(random_suffix)"
runner="${project_name}-$(random_suffix)"
build_container="${project_name}-$(random_suffix)"
compile_container="${project_name}-$(random_suffix)"
preserve_artifacts=false
cleanup() {
  timeout 60 docker stop --time 30 "$runner" >/dev/null 2>&1 || true
  timeout 60 docker rm -f "$runner" >/dev/null 2>&1 || true
  timeout 60 docker rm -f "$chrome" >/dev/null 2>&1 || true
  timeout 60 docker network rm "$network" >/dev/null 2>&1 || true
  if [[ $preserve_artifacts == true && -d "$test_dir/runtime/artifacts" ]]; then
    retained_artifacts=$(mktemp -d "$tmp_parent/${internal_name}-e2e-artifacts-XXXXXX")
    mv "$test_dir/runtime/artifacts" "$retained_artifacts/artifacts"
    printf 'Redacted E2E failure artifacts: %s\n' "$retained_artifacts" >&2
  fi
  rm -rf "$test_dir"
}
trap cleanup EXIT INT TERM

timeout -k 30 "$docker_timeout" docker run --rm --no-healthcheck \
  --name "$build_container" --memory="$docker_mem" --cpus="$docker_cpus" \
  -v "$test_dir/source:/workspace:ro" \
  -v "$test_dir/binaries:/out" \
  -v "$module_cache:/usr/local/share/go/pkg/mod" \
  -v "$build_cache:/usr/local/share/go/cache" \
  -w /workspace \
  -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  -e GOMODCACHE=/usr/local/share/go/pkg/mod \
  -e GOCACHE=/usr/local/share/go/cache \
  casjaysdev/go:latest go build -mod=readonly -buildvcs=false -trimpath -ldflags '-s -w' -o /out/gitmessages ./src

timeout -k 30 "$docker_timeout" docker run --rm --no-healthcheck \
  --name "$compile_container" --memory="$docker_mem" --cpus="$docker_cpus" \
  -v "$test_dir/source:/workspace:ro" \
  -v "$module_cache:/usr/local/share/go/pkg/mod" \
  -v "$build_cache:/usr/local/share/go/cache" \
  -w /workspace \
  -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  -e GOMODCACHE=/usr/local/share/go/pkg/mod \
  -e GOCACHE=/usr/local/share/go/cache \
  -e E2E_COMPILE_ONLY=1 \
  casjaysdev/go:latest go test -mod=readonly -run '^$' -tags e2e ./tests/e2e/...

timeout 60 docker network create --internal "$network" >/dev/null
timeout -k 30 "$docker_timeout" docker run -d --rm --no-healthcheck \
  --name "$chrome" \
  --memory=2g --cpus=2 \
  --network "$network" \
  chromedp/headless-shell:latest >/dev/null
chrome_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$chrome")
if [[ -z $chrome_ip ]]; then
  printf '%s\n' 'Unable to determine the isolated Chromium sidecar address.' >&2
  exit 1
fi

server_port=$((64000 + $$ % 1000))
timeout -k 30 "$docker_timeout" docker run -d --no-healthcheck \
  --name "$runner" \
  --memory="$docker_mem" --cpus="$docker_cpus" \
  --network "$network" \
  -v "$test_dir/source:/workspace:ro" \
  -v "$test_dir/binaries/gitmessages:/app/gitmessages:ro" \
  -v "$test_dir/runtime:/runtime" \
  -v "$module_cache:/usr/local/share/go/pkg/mod" \
  -v "$build_cache:/usr/local/share/go/cache" \
  -w /workspace \
  -e GOMODCACHE=/usr/local/share/go/pkg/mod \
  -e GOCACHE=/usr/local/share/go/cache \
  casjaysdev/go:latest /app/gitmessages \
  --config /runtime/config \
  --data /runtime/data \
  --cache /runtime/cache \
  --log /runtime/log \
  --backup /runtime/backup \
  --pid /runtime/gitmessages.pid \
  --port "$server_port" \
  --address 0.0.0.0 \
  --color no \
  --debug >/dev/null
runner_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$runner")
if [[ -z $runner_ip ]]; then
  printf '%s\n' 'Unable to determine the isolated E2E server address.' >&2
  exit 1
fi

if ! timeout -k 30 "$docker_timeout" docker exec \
  -e CHROME_CDP_ENDPOINT="ws://$chrome_ip:9222" \
  -e E2E_SERVER_HOST="$runner_ip" \
  -e E2E_SERVER_PORT="$server_port" \
  -e E2E_CONFIG_DIR=/runtime/config \
  -e E2E_ARTIFACT_DIR=/runtime/artifacts \
  "$runner" go test -mod=readonly -tags e2e ./tests/e2e/...; then
  timeout 30 docker logs "$runner" 2>&1 | sed -E 's/([Oo]perator token[^:]*: ).*/\1[redacted]/; s/(([Tt]oken|[Pp]assword|[Ss]ecret|[Aa]pi[_-]?key|[Aa]uthorization)[=: ]+)[^ ,;]+/\1[redacted]/g; s/(adm_|own_|mtr_)[A-Za-z0-9_-]+/\1[redacted]/g' | tee "$test_dir/runtime/artifacts/server.log" >&2 || true
  preserve_artifacts=true
  exit 1
fi
