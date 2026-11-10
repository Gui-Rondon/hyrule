#!/usr/bin/env bash
# Read-only load test of a resource over HTTP, to compare before/after a
# change in the same environment.
#
#   loadtest/run.sh <base url> <resource> <lookup value> [duration] [concurrency]
#   loadtest/run.sh http://localhost:8081 tags go 30s 50
#
# Scenarios: list (GET /<resource>), item by lookup (GET /<resource>/<lookup>)
# and status (GET /status). Writes are left out on purpose: run them only
# against a disposable database.
set -euo pipefail
base=${1:?base url}; resource=${2:?resource}; lookup=${3:?lookup value}
duration=${4:-30s}; concurrency=${5:-50}
HEY=${HEY:-go run github.com/rakyll/hey@v0.1.5}

printf "%-8s %10s %9s %9s %9s  %s\n" scenario "req/s" p50 p95 p99 status
for sc in "list|/$resource" "item|/$resource/$lookup" "status|/status"; do
  name=${sc%%|*}; url=$base${sc#*|}
  out=$($HEY -z "$duration" -c "$concurrency" "$url" 2>&1)
  rps=$(echo "$out" | awk '/Requests\/sec/ {printf "%.0f", $2}')
  p50=$(echo "$out" | awk '$1 ~ /^50%+$/ {printf "%.1fms", $3 * 1000}')
  p95=$(echo "$out" | awk '$1 ~ /^95%+$/ {printf "%.1fms", $3 * 1000}')
  p99=$(echo "$out" | awk '$1 ~ /^99%+$/ {printf "%.1fms", $3 * 1000}')
  codes=$(echo "$out" | sed -n '/Status code distribution/,/^$/p' | grep -oE '\[[0-9]+\][[:space:]]+[0-9]+' | tr -s ' \t' ' ' | paste -sd' ' -)
  printf "%-8s %10s %9s %9s %9s  %s\n" "$name" "$rps" "$p50" "$p95" "$p99" "$codes"
done
