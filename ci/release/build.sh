#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}
bash ci/release/validate.sh "$version"
mkdir -p .local/release
export CGO_ENABLED=0 GOTOOLCHAIN=local
for target_os in linux windows; do
  for target_arch in amd64 arm64; do
    suffix=''; if [ "$target_os" = windows ]; then suffix='.exe'; fi
    GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags "-s -w -X main.releaseVersion=$version" \
      -o ".local/release/hyrule_${version}_${target_os}_${target_arch}${suffix}" ./cmd/hyrule
  done
done
# This source archive has provenance independently of the Go proxy's module zip.
git archive --format=tar --prefix="hyrule-${version}/" HEAD | gzip -n > ".local/release/hyrule_${version}_source.tar.gz"
(cd .local/release && sha256sum hyrule_* > SHA256SUMS)
