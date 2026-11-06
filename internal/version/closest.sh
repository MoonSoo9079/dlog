#!/bin/sh
# Prints the closest version tag, without a leading v.
# Prints 0.1.0 when this checkout has no version tag.
# The same rules live in closest() in version.go.
set -eu
root=$(git -c safe.directory='*' rev-parse --show-toplevel 2>/dev/null || true)
if [ -z "$root" ]; then
  printf '%s' "0.1.0"
  exit 0
fi
tag=$(git -C "$root" -c safe.directory='*' describe --tags --abbrev=0 \
  --match 'v[0-9]*.[0-9]*.[0-9]*' \
  --match '[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)
tag=${tag#v}
case $tag in
  [0-9]*.[0-9]*.[0-9]*) printf '%s' "$tag" ;;
  *) printf '%s' "0.1.0" ;;
esac
