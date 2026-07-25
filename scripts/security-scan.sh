#!/usr/bin/env bash
set -euo pipefail

go_bin="${GOBIN:-$(go env GOPATH)/bin}"
PATH="$go_bin:$HOME/.local/bin:$PATH"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required security scanner is not installed: $1" >&2
    exit 127
  }
}

for scanner in gitleaks osv-scanner semgrep trivy shellcheck actionlint; do
  require_command "$scanner"
done

npm audit --prefix web --audit-level=moderate
gitleaks detect --source . --config .gitleaks.toml --no-banner
osv-scanner scan --recursive .
semgrep scan --config p/default --error --metrics=off
trivy fs --scanners vuln,secret,misconfig --exit-code 1 --severity HIGH,CRITICAL .
find . -name '*.sh' -not -path './.git/*' -print0 | xargs -0 shellcheck
actionlint
