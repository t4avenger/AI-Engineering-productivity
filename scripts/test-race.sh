#!/usr/bin/env bash
set -euo pipefail

if ! command -v gcc >/dev/null 2>&1; then
  echo "test-race: skipped because Go race detector requires cgo and gcc is not installed in this environment."
  exit 0
fi

CGO_ENABLED=1 go test -race ./cmd/... ./internal/...
