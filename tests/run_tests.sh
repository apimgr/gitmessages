#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

if command -v incus >/dev/null 2>&1; then
  exec "$SCRIPT_DIR/incus.sh" "${@:-all}"
fi

if command -v docker >/dev/null 2>&1; then
  exec "$SCRIPT_DIR/docker.sh"
fi

printf '%s\n' 'No supported container runtime found. Install Incus or Docker.' >&2
exit 69
