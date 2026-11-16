#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}
bash ci/release/validate.sh "$version"
mkdir -p .local/release
export CGO_ENABLED=0 GOTOOLCHAIN=local
# Avoid carrying artifacts from a different invocation into a signed release.
find .local/release -maxdepth 1 -type f -delete
for command in hyrule hyrule-serve; do
  version_symbol=main.releaseVersion
  if [ "$command" = hyrule-serve ]; then version_symbol=main.version; fi
  for target_os in linux windows; do
    for target_arch in amd64 arm64; do
      suffix=''; if [ "$target_os" = windows ]; then suffix='.exe'; fi
      GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags "-s -w -X ${version_symbol}=$version" \
        -o ".local/release/${command}_${version}_${target_os}_${target_arch}${suffix}" "./cmd/$command"
    done
  done
done
# This source archive has provenance independently of the Go proxy's module zip.
git archive --format=tar --prefix="hyrule-${version}/" HEAD | gzip -n > ".local/release/hyrule_${version}_source.tar.gz"
(cd .local/release && sha256sum hyrule_* hyrule-serve_* > SHA256SUMS)
