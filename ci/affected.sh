#!/usr/bin/env bash
# Services whose binary changed since <base>, one per line (docs/ci.md).
#
#   ci/affected.sh origin/develop
#   ci/affected.sh -changed internal/outbox/relay.go
set -euo pipefail
cd "$(dirname "$0")/.."
exec go run ./ci/affected "$@"
