#!/usr/bin/env bash
set -euo pipefail

npm audit --prefix web --audit-level=moderate

if command -v gitleaks >/dev/null 2>&1; then
  gitleaks detect --source . --config .gitleaks.toml --no-banner
else
  echo "gitleaks: not installed; install github.com/gitleaks/gitleaks/v8@v8.24.3"
fi

if command -v osv-scanner >/dev/null 2>&1; then
  osv-scanner scan source --recursive .
else
  echo "osv-scanner: not installed; install github.com/google/osv-scanner/cmd/osv-scanner@v1.9.2"
fi

if command -v semgrep >/dev/null 2>&1; then
  semgrep scan --config auto --error --metrics=off
else
  echo "semgrep: not installed; install semgrep==1.125.0"
fi

if command -v trivy >/dev/null 2>&1; then
  trivy fs --scanners vuln,secret,misconfig --exit-code 1 --severity HIGH,CRITICAL .
else
  echo "trivy: not installed; install aquasecurity/trivy@v0.64.1"
fi

if find . -name '*.sh' -not -path './.git/*' | grep -q .; then
  if command -v shellcheck >/dev/null 2>&1; then
    find . -name '*.sh' -not -path './.git/*' -print0 | xargs -0 shellcheck
  else
    echo "shellcheck: not installed; install koalaman/shellcheck@v0.10.0"
  fi
fi

if command -v actionlint >/dev/null 2>&1; then
  actionlint
else
  echo "actionlint: not installed; install rhysd/actionlint@v1.7.7"
fi
