#!/usr/bin/env bash
# Illustrative stub — the configurable deploy seam (ADR-0006,
# website/docs/reference/architecture-decisions.md). ci/affected only
# decides whether this runs, never how: replace the echo below with your
# provider's real deploy command (e.g. kubectl apply -f manifest.yml,
# adapted from the manifest in this same directory). Hyrule never sees
# what you put here.
set -euo pipefail
echo "would deploy posts-api here (see examples/blog-apis/deploy/posts-api/deploy.sh and manifest.yml)"
