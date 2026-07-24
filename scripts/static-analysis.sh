#!/usr/bin/env bash
set -euo pipefail

go vet ./...

if command -v staticcheck >/dev/null 2>&1; then
  staticcheck ./...
else
  echo "staticcheck: not installed; install honnef.co/go/tools/cmd/staticcheck@v0.6.1"
fi

if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run
else
  echo "golangci-lint: not installed; install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.2.2"
fi

if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./...
else
  echo "govulncheck: not installed; install golang.org/x/vuln/cmd/govulncheck@v1.1.4"
fi

npm run typecheck --prefix web
npm run knip --prefix web
