#!/bin/sh
set -eu
dir="${DLOG_DATA_DIR:-/var/lib/dlog}"
mkdir -p "$dir"
chown -R 65532:65532 "$dir"
exec su-exec 65532:65532 /usr/local/bin/dlog-server "$@"
