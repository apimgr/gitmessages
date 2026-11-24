#!/usr/bin/env bash
set -euo pipefail

if ! command -v incus >/dev/null 2>&1; then
  printf '%s\n' 'Incus is required for the systemd and OpenRC integration suite.' >&2
  exit 69
fi
if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'Docker is required to build the integration binaries.' >&2
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
project_org=$(awk -F ': *' '$1 == "project_org" {print $2; exit}' "$project_dir/IDEA.md")
internal_name=$(awk -F ': *' '$1 == "internal_name" {print $2; exit}' "$project_dir/IDEA.md")
internal_org=$(awk -F ': *' '$1 == "internal_org" {print $2; exit}' "$project_dir/IDEA.md")
if [[ -z $project_org || -z $internal_name || -z $internal_org ]]; then
  printf '%s\n' 'IDEA.md must define project_org, internal_name and internal_org.' >&2
  exit 78
fi

tmp_parent=${TMPDIR:-/tmp}/$project_org
mkdir -p "$tmp_parent"
test_dir=$(mktemp -d "$tmp_parent/${internal_name}-XXXXXX")
chmod 0700 "$test_dir"
go_cache=${GO_CACHE:-$HOME/go/pkg/mod}
go_build=${GO_BUILD:-$HOME/.cache/go-build/$project_name}
mkdir -p "$test_dir/binaries" "$go_cache" "$go_build"
task_suffix=$(printf '%04x%04x' "$RANDOM" "$RANDOM")
prefix="${project_name}-${task_suffix}"
incus_cpus=${INCUS_CPUS:-2}
incus_mem=${INCUS_MEM:-4GiB}
docker_mem=${DOCKER_MEM:-4g}
docker_cpus=${DOCKER_CPUS:-2}
created=()
incus_exec() { timeout "${INCUS_EXEC_TIMEOUT:-30}" incus exec "$@"; }
incus_file() { timeout "${INCUS_EXEC_TIMEOUT:-30}" incus file "$@"; }
cleanup() {
  local instance=
  for instance in "${created[@]:-}"; do
    timeout "${INCUS_COMMAND_TIMEOUT:-120}" incus delete "$instance" --force >/dev/null 2>&1 || true
  done
  python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$test_dir"
}
trap cleanup EXIT INT TERM

timeout -k 30 "${DOCKER_TIMEOUT:-3600}" docker run --rm --no-healthcheck \
  --name "${project_name}-${task_suffix}" \
  --memory="$docker_mem" --cpus="$docker_cpus" \
  -v "$project_dir:/workspace:ro" \
  -v "$test_dir/binaries:/out" \
  -v "$go_cache:/usr/local/share/go/pkg/mod" \
  -v "$go_build:/usr/local/share/go/cache" \
  -w /workspace \
  -e CGO_ENABLED=0 \
  -e GOFLAGS=-buildvcs=false \
  -e GOMODCACHE=/usr/local/share/go/pkg/mod \
  -e GOCACHE=/usr/local/share/go/cache \
  -e PROJECT_NAME="$project_name" \
  casjaysdev/go:latest sh -c \
  "go build -mod=readonly -buildvcs=false -trimpath -ldflags \"-s -w\" -o \"/out/\$PROJECT_NAME\" ./src && go build -mod=readonly -buildvcs=false -trimpath -ldflags \"-s -w\" -o \"/out/\$PROJECT_NAME-cli\" ./src/client"

cat >"$test_dir/remote.sh" <<'REMOTE_SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

failures=0
fail() { printf 'FAIL: %s\n' "$*"; failures=$((failures + 1)); }
pass() { printf 'PASS: %s\n' "$*"; }
check() { local label=$1; shift; if "$@" >/dev/null 2>&1; then pass "$label"; else fail "$label"; fi; }
check_not() { local label=$1; shift; if "$@" >/dev/null 2>&1; then fail "$label"; else pass "$label"; fi; }
wait_for() { local attempts=$1; shift; local i; for ((i=0; i<attempts; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

config_file="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}/server.yml"
data_root="/var/lib/${INTERNAL_ORG}/${INTERNAL_NAME}"
config_root="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}"
port() { sed -n 's/^    port:[[:space:]]*"\{0,1\}\([0-9][0-9]*\).*/\1/p' "$config_file" 2>/dev/null | head -1; }
api() { printf 'http://127.0.0.1:%s/api/v1/server/healthz' "$(port)"; }
healthz() { curl -q -LSsf --max-time 10 -H 'Accept: application/json' "$(api)"; }
healthz_up() { healthz | jq -e '.status' >/dev/null; }
overlay() { healthz | jq -r ".features.$1.$2"; }
i2p_enabled() { [[ $(overlay i2p enabled) == true ]]; }
i2p_disabled() { [[ $(overlay i2p enabled) == false ]]; }
tor_disabled() { [[ $(overlay tor enabled) == false && $(overlay tor status) == disabled ]]; }
if [[ $INIT_MODE == systemd ]]; then
  unit_file="/etc/systemd/system/${INTERNAL_NAME}.service"
  svc_active() { systemctl is-active --quiet "$INTERNAL_NAME"; }
  svc_enabled() { systemctl is-enabled --quiet "$INTERNAL_NAME"; }
  svc_pid() { systemctl show -p MainPID --value "$INTERNAL_NAME"; }
else
  unit_file="/etc/init.d/${INTERNAL_NAME}"
  svc_active() { rc-service "$INTERNAL_NAME" status 2>&1 | grep -q started; }
  svc_enabled() { rc-update show default | grep -qw "$INTERNAL_NAME"; }
  svc_pid() { cat "/var/run/${INTERNAL_ORG}/${INTERNAL_NAME}/${INTERNAL_NAME}.service.pid"; }
fi
svc_stopped() { ! svc_active; }

run_overlay_checks() {
  printf '=== Tor checks ===\n'
  check 'Tor binary installed' command -v tor
  check 'Tor enabled' test "$(overlay tor enabled)" = true
  case "$(overlay tor status)" in disabled|starting|healthy|unavailable) pass 'Tor status is valid' ;; *) fail 'Tor status is invalid' ;; esac
  for _ in $(seq 1 "${TOR_WAIT:-300}"); do [[ $(overlay tor status) == healthy ]] && break; sleep 1; done
  if [[ $(overlay tor status) == healthy ]]; then
    [[ $(overlay tor hostname) =~ ^[a-z2-7]{56}\.onion$ ]] && pass 'Tor v3 hostname' || fail 'Tor hostname is not v3'
    check 'Tor is running' test "$(overlay tor running)" = true
    check 'Tor health check passes' test "$(healthz | jq -r '.checks.tor')" = ok
    check 'Tor hostname persisted' test -s "$data_root/tor/site/hostname"
    for path in "$config_root/tor" "$data_root/tor" "$data_root/tor/site"; do check "$path mode 700" test "$(stat -c %a "$path")" = 700; done
  elif [[ ${REQUIRE_OVERLAY:-0} == 1 ]]; then
    fail "Tor did not bootstrap within ${TOR_WAIT:-300}s"
  else
    printf 'SKIP: Tor bootstrap unavailable; current status=%s\n' "$(overlay tor status)"
  fi
  if healthz | grep -Eq '/(etc|var|home|config|data)/'; then fail 'health response leaks a filesystem path'; else pass 'health response has no filesystem path'; fi
  mapfile -t tor_bins < <(type -aP tor | while IFS= read -r binary; do readlink -f "$binary"; done | sort -u)
  for tor_bin in "${tor_bins[@]}"; do mv "$tor_bin" "${tor_bin}.hidden"; done
  "$PROJECT_NAME" --service restart
  check 'service restarts without Tor binary' wait_for 60 tor_disabled
  check 'Tor disabled without binary' test "$(overlay tor enabled)" = false
  check 'Tor status disabled without binary' test "$(overlay tor status)" = disabled
  check 'Tor hostname empty without binary' test -z "$(overlay tor hostname)"
  check 'Tor health check omitted without binary' test "$(healthz | jq '(.checks | has("tor"))')" = false
  for tor_bin in "${tor_bins[@]}"; do mv "${tor_bin}.hidden" "$tor_bin"; done
  "$PROJECT_NAME" --service restart
  check 'service recovers with Tor binary' wait_for 60 healthz_up

  printf '=== I2P checks ===\n'
  check 'i2pd binary installed' command -v i2pd
  check 'server config mode 600' test "$(stat -c %a "$config_root/server.yml")" = 600
  check 'I2P defaults off in a system container' test "$(overlay i2p enabled)" = false
  check 'I2P status disabled by default' test "$(overlay i2p status)" = disabled
  check 'I2P check omitted by default' test "$(healthz | jq '(.checks | has("i2p"))')" = false
  mkdir -p "$config_root/yaml.d"
  printf 'server:\n  i2p:\n    enabled: true\n' > "$config_root/yaml.d/50-i2p.yml"
  chown "$INTERNAL_NAME:$INTERNAL_NAME" "$config_root/yaml.d" "$config_root/yaml.d/50-i2p.yml"
  chmod 0700 "$config_root/yaml.d"
  chmod 0600 "$config_root/yaml.d/50-i2p.yml"
  "$PROJECT_NAME" --service restart
  check 'I2P enabled from drop-in' wait_for 60 i2p_enabled
  check 'I2P provider is i2pd' test "$(overlay i2p provider)" = i2pd
  case "$(overlay i2p status)" in starting|healthy|unavailable) pass 'I2P status is valid' ;; *) fail 'I2P status is invalid' ;; esac
  for _ in $(seq 1 "${I2P_WAIT:-600}"); do [[ $(overlay i2p status) == healthy ]] && break; sleep 1; done
  if [[ $(overlay i2p status) == healthy ]]; then
    [[ $(overlay i2p hostname) =~ ^[a-z2-7]{52}\.b32\.i2p$ ]] && pass 'I2P hostname' || fail 'I2P hostname is invalid'
    check 'I2P is running' test "$(overlay i2p running)" = true
    check 'I2P health check passes' test "$(healthz | jq -r '.checks.i2p')" = ok
    check 'I2P destination key persisted' sh -c "find '$data_root/i2p/site' -type f -size +0 | grep -q ."
  elif [[ ${REQUIRE_OVERLAY:-0} == 1 ]]; then
    fail "I2P bootstrap did not complete within ${I2P_WAIT:-600}s"
  else
    printf 'SKIP: I2P bootstrap unavailable; current status=%s\n' "$(overlay i2p status)"
  fi
  if healthz | grep -Eq '/(etc|var|home|config|data)/'; then fail 'health response leaks a filesystem path'; else pass 'health response has no filesystem path'; fi
  printf 'server:\n  i2p:\n    enabled: false\n' > "$config_root/yaml.d/50-i2p.yml"
  chown "$INTERNAL_NAME:$INTERNAL_NAME" "$config_root/yaml.d/50-i2p.yml"
  chmod 0600 "$config_root/yaml.d/50-i2p.yml"
  "$PROJECT_NAME" --service restart
  check 'I2P opt-out takes effect' wait_for 60 i2p_disabled
  check 'I2P status disabled after opt-out' test "$(overlay i2p status)" = disabled
  check 'I2P health check omitted after opt-out' test "$(healthz | jq '(.checks | has("i2p"))')" = false
  rm -f "$config_root/yaml.d/50-i2p.yml"
  "$PROJECT_NAME" --service restart
  check 'I2P returns to host default' wait_for 60 healthz_up
  mapfile -t i2pd_bins < <(type -aP i2pd | while IFS= read -r binary; do readlink -f "$binary"; done | sort -u)
  for i2pd_bin in "${i2pd_bins[@]}"; do mv "$i2pd_bin" "${i2pd_bin}.hidden"; done
  "$PROJECT_NAME" --service restart
  check 'service restarts without i2pd' wait_for 60 i2p_disabled
  check 'I2P disabled without provider' test "$(overlay i2p enabled)" = false
  check 'I2P status disabled without provider' test "$(overlay i2p status)" = disabled
  check 'I2P provider none without binary' test "$(overlay i2p provider)" = none
  check 'I2P health check omitted without provider' test "$(healthz | jq '(.checks | has("i2p"))')" = false
  for i2pd_bin in "${i2pd_bins[@]}"; do mv "${i2pd_bin}.hidden" "$i2pd_bin"; done
  "$PROJECT_NAME" --service restart
  check 'service recovers with i2pd' wait_for 60 healthz_up

}

if [[ $PHASE == initial ]]; then
  printf '=== %s init system ===\n' "$INIT_MODE"
  if [[ $INIT_MODE == systemd ]]; then
    check 'PID 1 is systemd' test "$(cat /proc/1/comm)" = systemd
  else
    check 'OpenRC is running' test -d /run/openrc
    check 'systemd is absent' test ! -d /run/systemd/system
  fi
  "$PROJECT_NAME" --version
  "$PROJECT_NAME" --help >/dev/null
  file "/usr/local/bin/$PROJECT_NAME"

  printf '=== %s service install ===\n' "$INIT_MODE"
  "$PROJECT_NAME" --service --install
  check 'service definition installed' test -f "$unit_file"
  if [[ $INIT_MODE == openrc ]]; then
    check 'OpenRC script uses openrc-run' grep -q '^#!/sbin/openrc-run' "$unit_file"
    check 'OpenRC script is executable' test -x "$unit_file"
    check_not 'OpenRC script is not SysV format' grep -q 'BEGIN INIT INFO' "$unit_file"
  fi
  check 'service enabled at boot' svc_enabled
  check 'service active after install' wait_for 30 svc_active
  check 'health endpoint answers after install' wait_for 60 healthz_up
  check 'service account exists' getent passwd "$INTERNAL_NAME"
  check 'server runs as service account' test "$(stat -c %U "/proc/$(svc_pid)")" = "$INTERNAL_NAME"

  printf '=== %s service restart ===\n' "$INIT_MODE"
  old_pid=$(svc_pid)
  "$PROJECT_NAME" --service restart
  check 'service active after restart' wait_for 30 svc_active
  check 'health answers after restart' wait_for 60 healthz_up
  check 'restart replaced the process' test "$(svc_pid)" != "$old_pid"

  printf '=== %s stop and start ===\n' "$INIT_MODE"
  "$PROJECT_NAME" --service stop
  check 'service stopped' wait_for 30 svc_stopped
  check_not 'health endpoint is down while stopped' curl -q -LSsf --max-time 3 "$(api)"
  "$PROJECT_NAME" --service start
  check 'service active after start' wait_for 30 svc_active
  check 'health answers after start' wait_for 60 healthz_up

  printf '=== API and server page checks ===\n'
  check 'health API JSON' curl -q -LSsf --max-time 10 "$(api)"
  check 'health API text extension' curl -q -LSsf --max-time 10 "$(api).txt"
  check 'health alias JSON negotiation' curl -q -LSsf --max-time 10 -H 'Accept: application/json' "http://127.0.0.1:$(port)/server/healthz"
  check 'health alias plain text negotiation' curl -q -LSsf --max-time 10 -H 'Accept: text/plain' "http://127.0.0.1:$(port)/server/healthz"
  for route in / /types /templates /builder /server/about /server/help /server/help/mcp /server/privacy /server/terms /server/security /server/healthz /server/preferences /api/v1 /api/v1/types /api/v1/conventions /api/v1/templates /api/v1/templates/random /api/v1/messages/random /api/v1/server/about /api/v1/server/help /api/v1/server/privacy /api/v1/server/terms /api/v1/server/security /api/v1/server/preferences /server/docs/swagger /server/docs/graphql; do
    check "GET $route" curl -q -LSsf --max-time 10 "http://127.0.0.1:$(port)$route"
  done
  check_not 'removed root GraphQL route' curl -q -LSsf --max-time 10 "http://127.0.0.1:$(port)/graphql"
  check 'server binary rename' sh -c 'cp "/usr/local/bin/$1" /tmp/renamed-server && chmod 0755 /tmp/renamed-server && /tmp/renamed-server --help 2>&1 | grep -F renamed-server' _ "$PROJECT_NAME"

  [[ $failures -eq 0 ]] || { printf '%s check(s) failed\n' "$failures"; exit 1; }
  printf '=== %s initial tests passed ===\n' "$INIT_MODE"
elif [[ $PHASE == overlays ]]; then
  run_overlay_checks
  [[ $failures -eq 0 ]] || { printf '%s check(s) failed\n' "$failures"; exit 1; }
  printf '=== %s overlay tests passed ===\n' "$INIT_MODE"
else
  printf '=== %s boot persistence ===\n' "$INIT_MODE"
  check 'service still enabled after reboot' svc_enabled
  check 'service active after reboot' wait_for 120 svc_active
  check 'health answers after reboot' wait_for 120 healthz_up
  printf '=== %s disable service ===\n' "$INIT_MODE"
  "$PROJECT_NAME" --service --disable
  check_not 'service not enabled after disable' svc_enabled
  check 'service stopped after disable' wait_for 30 svc_stopped
  check 'service definition remains after disable' test -f "$unit_file"
  printf '=== %s reinstall and uninstall ===\n' "$INIT_MODE"
  "$PROJECT_NAME" --service --install
  check 'service enabled after reinstall' svc_enabled
  check 'service active after reinstall' wait_for 30 svc_active
  check 'health answers after reinstall' wait_for 60 healthz_up
  printf 'y\n' | "$PROJECT_NAME" --service --uninstall
  check_not 'service definition removed' test -e "$unit_file"
  check_not 'service disabled after uninstall' svc_enabled
  check 'service stopped after uninstall' wait_for 30 svc_stopped
  check_not 'data directory removed' test -d "$data_root"
  [[ $failures -eq 0 ]] || { printf '%s check(s) failed\n' "$failures"; exit 1; }
  printf '=== %s lifecycle tests passed ===\n' "$INIT_MODE"
fi
REMOTE_SCRIPT
chmod 0600 "$test_dir/remote.sh"

wait_boot() {
  local instance=$1 init=$2 state
  for _ in $(seq 1 60); do
    if incus_exec "$instance" -- true >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if [[ $init == systemd ]]; then
    for _ in $(seq 1 90); do
      state=$(incus_exec "$instance" -- systemctl is-system-running 2>/dev/null || true)
      [[ -n $state && $state != starting ]] && break
      sleep 1
    done
  else
    for _ in $(seq 1 60); do
      incus_exec "$instance" -- test -d /run/openrc 2>/dev/null && break
      sleep 1
    done
  fi
  for _ in $(seq 1 60); do
    incus_exec "$instance" -- sh -c 'ip route | grep -q "^default"' 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

remote() {
  local instance=$1 init=$2 phase=$3
  timeout "${REMOTE_TIMEOUT:-1200}" incus exec "$instance" -- env INIT_MODE="$init" PHASE="$phase" PROJECT_NAME="$project_name" \
    INTERNAL_NAME="$internal_name" INTERNAL_ORG="$internal_org" \
    REQUIRE_OVERLAY="${REQUIRE_OVERLAY:-0}" TOR_WAIT="${TOR_WAIT:-300}" I2P_WAIT="${I2P_WAIT:-600}" \
    bash -s < "$test_dir/remote.sh"
}

run_client_tests() {
  local instance=$1 server_port client_bin="/usr/local/bin/${project_name}-cli"
  server_port=$(incus_exec "$instance" -- sed -n 's/^    port: "\{0,1\}\([0-9][0-9]*\).*/\1/p' "/etc/$internal_org/$internal_name/server.yml" | head -1)
  if [[ -z $server_port ]]; then
    printf 'FAIL: could not discover the system-container server port\n' >&2
    return 1
  fi
  printf '=== [%s] CLI integration ===\n' "$instance"
  printf 'Pushing CLI binary to %s:%s\n' "$instance" "$client_bin"
  incus_file push "$test_dir/binaries/${project_name}-cli" "$instance$client_bin"
  printf 'Setting CLI binary permissions\n'
  incus_exec "$instance" -- chmod 0755 "$client_bin"
  incus_exec "$instance" -- ls -l "$client_bin"
  incus_exec "$instance" -- sh -c "file '$client_bin' | grep -q 'statically linked'"
  printf 'PASS: CLI is statically linked\n'
  printf 'Checking CLI version\n'
  incus_exec "$instance" -- "$client_bin" --version >/dev/null
  printf 'PASS: CLI version\n'
  incus_exec "$instance" -- "$client_bin" --help >/dev/null
  printf 'PASS: CLI help\n'
  incus_exec "$instance" -- "$client_bin" --server "http://127.0.0.1:$server_port" types >/dev/null
  printf 'PASS: CLI catalog\n'
  incus_exec "$instance" -- "$client_bin" --server "http://127.0.0.1:$server_port" build --type feat --scope incus --description integration >/dev/null
  printf 'PASS: CLI message builder\n'
}

run_mode() {
  local init=$1 image='' instance=''
  if [[ $init == systemd ]]; then image=images:debian/trixie; else image=images:alpine/3.24; fi
  instance="${prefix}-${init}"
  created+=("$instance")
  printf '=== [%s] Launching %s ===\n' "$init" "$image"
  timeout "${INCUS_COMMAND_TIMEOUT:-120}" incus launch "$image" "$instance" -c "limits.cpu=$incus_cpus" -c "limits.memory=$incus_mem"
  wait_boot "$instance" "$init"
  if [[ $init == systemd ]]; then
    timeout "${INCUS_INSTALL_TIMEOUT:-600}" incus exec "$instance" -- bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq file tor i2pd ca-certificates' >/dev/null
    incus_exec "$instance" -- systemctl disable --now tor.service tor@default.service i2pd.service >/dev/null 2>&1 || true
  else
    timeout "${INCUS_INSTALL_TIMEOUT:-600}" incus exec "$instance" -- sh -c 'apk add --no-cache bash curl jq file shadow tor i2pd tzdata ca-certificates' >/dev/null
    incus_exec "$instance" -- sh -c 'rc-service tor stop >/dev/null 2>&1 || true; rc-service i2pd stop >/dev/null 2>&1 || true; rc-update del tor >/dev/null 2>&1 || true; rc-update del i2pd >/dev/null 2>&1 || true'
  fi
  incus_file push "$test_dir/binaries/$project_name" "$instance/usr/local/bin/$project_name"
  incus_exec "$instance" -- chmod 0755 "/usr/local/bin/$project_name"
  remote "$instance" "$init" initial
  run_client_tests "$instance"
  remote "$instance" "$init" overlays
  printf '=== [%s] Reboot persistence ===\n' "$init"
  timeout "${INCUS_COMMAND_TIMEOUT:-120}" incus restart "$instance"
  wait_boot "$instance" "$init"
  remote "$instance" "$init" after-reboot
}

for init in $modes; do
  run_mode "$init"
done
printf 'Incus integration checks passed (%s).\n' "$modes"
