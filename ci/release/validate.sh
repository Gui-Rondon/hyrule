#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}
# Final and prerelease SemVer; no mutable branch or build metadata.
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.[1-9][0-9]*)?$ ]]; then
  echo 'Use vX.Y.Z or vX.Y.Z-(alpha|beta|rc).N' >&2
  exit 1
fi
if [[ "$version" == v1.* ]]; then
  echo 'v1 requires the separate maturity review in COMPATIBILITY.md; this workflow currently publishes v0 only.' >&2
  exit 1
fi
if [[ "$version" != v0.* ]]; then
  echo 'This module has no v2+ module path; only v0 releases are currently allowed.' >&2
  exit 1
fi
if [[ "${HYRULE_INDEX_ONLY:-false}" != true ]] && git rev-parse --verify "refs/tags/$version" >/dev/null 2>&1; then
  test "$(git rev-parse "$version^{commit}")" = "$(git rev-parse HEAD)" || {
    echo 'Existing tag points at a different commit; never move published tags.' >&2; exit 1;
  }
fi
