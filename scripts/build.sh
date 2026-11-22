#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm --prefix web run format:check
npm --prefix web run check
npm --prefix web run build
bash scripts/build-go.sh
