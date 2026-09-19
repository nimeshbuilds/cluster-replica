#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .cache
archive=.cache/vcluster-0.37.1.tgz
expected=afb57fb5f2e3088519ffa9112fa0bfc3543bc3465f86f7e7e655ba30aea0d969
if [[ ! -f "$archive" ]]; then
  curl --fail --location --max-time 60 https://charts.loft.sh/charts/vcluster-0.37.1.tgz --output "$archive.tmp"
  mv "$archive.tmp" "$archive"
fi
actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
if [[ "$actual" != "$expected" ]]; then
  echo "Pinned chart checksum mismatch; remove $archive and investigate." >&2
  exit 1
fi
