#!/usr/bin/env bash
set -euo pipefail

if ! command -v incus >/dev/null 2>&1; then
  printf '%s\n' 'Incus is required for this integration suite.' >&2
  exit 69
fi

mode=${1:-all}
case "$mode" in
  systemd|openrc) modes=$mode ;;
  all) modes='systemd openrc' ;;
  *) printf 'Usage: %s [systemd|openrc|all]\n' "$0" >&2; exit 2 ;;
esac

project_dir=$(git rev-parse --show-toplevel)
project_name=$(basename "$project_dir")
go_cache=${GO_CACHE:-$HOME/go/pkg/mod}
go_build=${GO_BUILD:-$HOME/.cache/go-build/$project_name}
mkdir -p "$go_cache" "$go_build"
make -C "$project_dir" build

prefix="test-${project_name}-$$"
created=()
cleanup() {
  local instance
  for instance in "${created[@]}"; do
    incus delete "$instance" --force >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT INT TERM

check_mode() {
  local init=$1 image instance
  case "$init" in
    systemd) image=images:debian/trixie ;;
    openrc) image=images:alpine/3.24 ;;
  esac
  instance="${prefix}-${init}"
  created+=("$instance")
  printf 'Starting %s instance %s\n' "$init" "$instance"
  incus launch "$image" "$instance"
  for attempt in $(seq 1 60); do
    if incus exec "$instance" -- true >/dev/null 2>&1; then break; fi
    sleep 1
  done

  if [[ $init == systemd ]]; then
    incus exec "$instance" -- bash -lc 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq file ca-certificates'
  else
    incus exec "$instance" -- sh -c 'apk add --no-cache bash curl jq file ca-certificates'
  fi
  incus file push "$project_dir/binaries/$project_name" "$instance/usr/local/bin/$project_name"
  incus exec "$instance" -- chmod 0755 "/usr/local/bin/$project_name"
  incus exec "$instance" -- "$project_name" --version
  incus exec "$instance" -- "$project_name" --help
  incus exec "$instance" -- bash -lc "cp /usr/local/bin/$project_name /tmp/renamed-server && chmod 0755 /tmp/renamed-server && /tmp/renamed-server --help | grep -F renamed-server"
  incus exec "$instance" -- bash -lc "mkdir -p /tmp/apimgr && /usr/local/bin/$project_name --config /tmp/apimgr --port 64580 --address 127.0.0.1 >/tmp/gitmessages-test.log 2>&1 &"
  ready=false
  for ((attempt = 0; attempt < 30; attempt += 1)); do
    if incus exec "$instance" -- curl -q -LSsf --max-time 2 http://127.0.0.1:64580/api/v1/server/healthz >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
  if [[ $ready != true ]]; then
    incus exec "$instance" -- cat /tmp/gitmessages-test.log >&2 || true
    return 1
  fi
  for route in / /types /templates /builder /server/about /server/help /server/help/mcp /server/privacy /server/terms /api/v1/types /api/v1/conventions /api/v1/templates /api/v1/server/help/mcp /server/docs/swagger /server/docs/graphql; do
    incus exec "$instance" -- curl -q -LSsf --max-time 10 "http://127.0.0.1:64580$route" >/dev/null
  done
  if incus exec "$instance" -- test -x "/usr/local/bin/${project_name}-cli"; then
    incus exec "$instance" -- "${project_name}-cli" --version
    incus exec "$instance" -- "${project_name}-cli" --server http://127.0.0.1:64580 types >/dev/null
  fi
  incus exec "$instance" -- pkill -TERM -x "$project_name" || true
}

for init in $modes; do
  check_mode "$init"
done

printf 'Incus binary checks passed (%s).\n' "$modes"
