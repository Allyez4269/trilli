#!/usr/bin/env bash
# Build the Trilli CMX service (cmx.trilli.com).
#   go build -> bin/trilli-cmx
#
# Re-run this after any code change, then `sudo systemctl restart trilli-cmx`.
set -euo pipefail
cd "$(dirname "$0")"
ROOT="$(pwd)"

echo "==> Go build"
go build -o bin/trilli-cmx ./cmd/trilli-cmx

echo "==> Done. Binary: ${ROOT}/bin/trilli-cmx"
