#!/usr/bin/env bash
set -euo pipefail

cc_bin="${CC:-}"
if [ -n "$cc_bin" ]; then
  if ! command -v "$cc_bin" >/dev/null 2>&1; then
    echo "test-race: skipped because CC=$cc_bin is not available."
    exit 0
  fi
elif command -v cc >/dev/null 2>&1; then
  cc_bin="cc"
elif command -v gcc >/dev/null 2>&1; then
  cc_bin="gcc"
elif command -v clang >/dev/null 2>&1; then
  cc_bin="clang"
else
  echo "test-race: skipped because Go race detector requires cgo and no C compiler was found."
  exit 0
fi

CC="$cc_bin" CGO_ENABLED=1 go test -race ./cmd/... ./internal/...
