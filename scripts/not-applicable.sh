#!/usr/bin/env bash
set -euo pipefail

name="${1:?name required}"
reason="${2:?reason required}"

printf '%s: not applicable yet. %s\n' "$name" "$reason"
