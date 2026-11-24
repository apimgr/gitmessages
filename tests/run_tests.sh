#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "${BASH_SOURCE[0]%/*}" && pwd)

if command -v docker >/dev/null 2>&1; then
  exec "$SCRIPT_DIR/docker.sh"
fi

printf '%s\n' 'Docker is required: every test script builds in Docker. Install Docker from https://docker.com/.' >&2
exit 69
