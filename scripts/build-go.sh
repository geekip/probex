#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/probexd-linux-amd64 ./cmd/probexd
for target in darwin-arm64 linux-amd64 linux-arm64 linux-mipsle-softfloat windows-amd64; do
  IFS=- read -r target_os target_arch target_float <<< "$target"
  suffix=''
  if [[ "$target_os" == windows ]]; then suffix='.exe'; fi
  if [[ "$target_arch" == mipsle ]]; then
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOMIPS=softfloat go build -trimpath -ldflags='-s -w' -o "dist/probex-$target$suffix" ./cmd/probex
  else
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags='-s -w' -o "dist/probex-$target$suffix" ./cmd/probex
  fi
done
cp README.md THIRD_PARTY_NOTICES dist/
(cd dist && sha256sum probex* README.md THIRD_PARTY_NOTICES > SHA256SUMS)
file dist/probex*
