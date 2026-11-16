#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}
bash ci/release/validate.sh "$version"
# Keep this independent of the checkout module graph and use the public proxy.
for attempt in 1 2 3 4 5; do
  if GOPROXY=https://proxy.golang.org go mod download -json "github.com/Gui-Rondon/hyrule@$version"; then
    exit 0
  fi
  sleep "$((attempt * 5))"
done
echo "Proxy indexing failed for $version; rerun Index release after investigating. Never move the tag." >&2
exit 1
