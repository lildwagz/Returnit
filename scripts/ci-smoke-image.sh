#!/usr/bin/env bash
# Usage: ci-smoke-image.sh <image>
# Both programs must be present and must refuse to start without configuration.
set -euo pipefail
image="${1:?usage: ci-smoke-image.sh <image>}"
for bin in /web /worker; do
  out=$(docker run --rm "$image" "$bin" 2>&1 || true)
  echo "$bin -> $out"
  echo "$out" | grep -q "config: DATABASE_URL is required" || { echo "unexpected output from $bin"; exit 1; }
done
