#!/bin/sh
set -eu
dir="${DLOG_DATA_DIR:-/var/lib/dlog}"
mkdir -p "$dir"
chown -R 65532:65532 "$dir"
if [ -n "${DLOG_AGENT_CERT:-}" ]; then
  cred=$(dirname "$DLOG_AGENT_CERT")
  mkdir -p "$cred"
  chown -R 65532:65532 "$cred"
fi
exec su-exec 65532:65532 /usr/local/bin/dlog-server "$@"
