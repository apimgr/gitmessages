#!/usr/bin/env bash
set -eo pipefail

APP_NAME=gitmessages
APP_BIN="/usr/local/bin/${APP_NAME}"

export TZ="${TZ:-America/New_York}"
export CONFIG_DIR="${CONFIG_DIR:-/config/${APP_NAME}}"
export DATA_DIR="${DATA_DIR:-/data/${APP_NAME}}"

flags=(--port "${PORT:-80}")
if [[ ${DEBUG:-false} == true ]]; then
  flags+=(--debug)
fi

exec "$APP_BIN" --address "${LISTEN:-[::]}" "${flags[@]}" "$@"
