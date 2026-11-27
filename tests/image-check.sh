#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: tests/image-check.sh IMAGE}
project_dir=$(git rev-parse --show-toplevel)
project_org=$(awk -F ': *' '$1 == "project_org" {print $2; exit}' "$project_dir/IDEA.md")
internal_name=$(awk -F ': *' '$1 == "internal_name" {print $2; exit}' "$project_dir/IDEA.md")
[[ -n $project_org && -n $internal_name ]] || {
  echo 'IDEA.md must define project_org and internal_name.' >&2
  exit 78
}

temp_parent=${TMPDIR:-/tmp}/$project_org
mkdir -p "$temp_parent"
chmod 0700 "$temp_parent"
temp_dir=$(mktemp -d "$temp_parent/${internal_name}-image-check-XXXXXX")
chmod 0700 "$temp_dir"
container_name=${internal_name}-image-check-$$
cleanup() {
  docker stop "$container_name" >/dev/null 2>&1 || true
  docker rm "$container_name" >/dev/null 2>&1 || true
  docker run --rm --name "${container_name}-cleanup" --volume "$temp_dir:/cleanup" alpine:latest sh -c 'chmod -R u+rwx /cleanup' >/dev/null 2>&1 || true
  rm -rf "$temp_dir"
}
trap cleanup EXIT INT TERM

print_tor_failures() {
  docker logs "$container_name" 2>&1 | grep 'WARN Tor unavailable:' | tail -5 >&2 || true
}

for command_name in bash curl tor i2pd tini; do
  docker run --rm --name "${container_name}-${command_name}" --entrypoint sh "$image" -c "command -v $command_name" >/dev/null || {
    printf 'Image is missing required command: %s\n' "$command_name" >&2
    exit 1
  }
done

docker run --detach --name "$container_name" \
  --volume "$temp_dir/config:/config" \
  --volume "$temp_dir/data:/data" \
  "$image" >/dev/null

deadline=$((SECONDS + 180))
tor_status=
while ((SECONDS < deadline)); do
  if [[ $(docker inspect --format '{{.State.Running}}' "$container_name") != true ]]; then
    echo 'Image container exited before its health endpoint became ready.' >&2
    exit 1
  fi
  tor_status=$(docker exec "$container_name" sh -c \
    'curl -q -fsS --max-time 3 -H "Accept: application/json" http://127.0.0.1:80/api/v1/server/healthz 2>/dev/null' |
    tr '\n' ' ' |
    grep -oE '"tor"[[:space:]]*:[[:space:]]*\{[^}]*\}' |
    sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)
  if [[ $tor_status == healthy ]]; then
    break
  fi
  if [[ $tor_status == error ]]; then
    echo 'Image health reports Tor error.' >&2
    print_tor_failures
    exit 1
  fi
  sleep 2
done

if [[ $tor_status != healthy ]]; then
  if [[ ${REQUIRE_OVERLAY:-0} == 1 ]]; then
    printf 'Tor did not become healthy before timeout (status: %s).\n' "${tor_status:-no health response}" >&2
    print_tor_failures
    exit 1
  fi
  case "$tor_status" in
    starting | unavailable)
      if [[ ${REQUIRE_OVERLAY:-0} == 1 ]] || docker exec "$container_name" \
        curl -q -LSsf --max-time 5 https://example.com/ >/dev/null 2>&1; then
        printf 'Tor did not become healthy despite available outbound network (status: %s).\n' "$tor_status" >&2
        print_tor_failures
        exit 1
      fi
      echo 'SKIP: no outbound network' >&2
      ;;
    *)
      printf 'Image health check failed (Tor status: %s).\n' "${tor_status:-no health response}" >&2
      print_tor_failures
      exit 1
      ;;
  esac
fi

uid=$(docker exec "$container_name" sh -c '
for status in /proc/[0-9]*/status; do
  [ -r "$status" ] || continue
  name=$(sed -n "s/^Name:[[:space:]]*//p" "$status")
  if [ "$name" = gitmessages ]; then
    sed -n "s/^Uid:[[:space:]]*\\([0-9]*\\).*/\\1/p" "$status"
    exit
  fi
done
')
if [[ -z $uid || $uid == 0 ]]; then
  echo 'Server is still running as root.' >&2
  exit 1
fi

echo "Image check passed: $image (service uid $uid; Tor $tor_status)."
