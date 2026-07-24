#!/usr/bin/env bash
set -euo pipefail

go_bin="${GOBIN:-$(go env GOPATH)/bin}"
if [ -d "$go_bin" ]; then
  PATH="$go_bin:$PATH"
fi

go_packages=(./cmd/... ./internal/...)

go vet "${go_packages[@]}"

if command -v staticcheck >/dev/null 2>&1; then
  staticcheck "${go_packages[@]}"
else
  echo "staticcheck: not installed; install honnef.co/go/tools/cmd/staticcheck@v0.6.1"
fi

if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run "${go_packages[@]}"
else
  echo "golangci-lint: not installed; install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"
fi

if command -v govulncheck >/dev/null 2>&1; then
  govulncheck "${go_packages[@]}"
else
  echo "govulncheck: not installed; install golang.org/x/vuln/cmd/govulncheck@v1.1.4"
fi

npm run typecheck --prefix web
npm run knip --prefix web
