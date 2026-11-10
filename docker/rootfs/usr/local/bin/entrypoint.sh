#!/bin/sh
set -eu

exec /usr/local/bin/gitmessages --port "${PORT:-8080}" "$@"
