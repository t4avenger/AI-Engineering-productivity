#!/usr/bin/env bash
set -euo pipefail

go test ./internal/... -coverprofile=coverage.out
coverage_output="$(go tool cover -func=coverage.out)"
printf '%s\n' "$coverage_output"
backend_total="$(printf '%s\n' "$coverage_output" | awk '/^total:/ { sub(/%/, "", $3); print $3 }')"
awk -v coverage="$backend_total" 'BEGIN { if (coverage < 80) exit 1 }'
printf 'backend coverage threshold: %s%% >= 80%%\n' "$backend_total"

npm run test:coverage --prefix web
