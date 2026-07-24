#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
port="${TELEMETRYIQ_PORT:-18080}"
ready=0

cleanup() {
  if [[ -n "${pid:-}" ]]; then
    kill "$pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

go build -o "$tmpdir/telemetryiq" ./cmd/telemetryiq
TELEMETRYIQ_PORT="$port" "$tmpdir/telemetryiq" >"$tmpdir/daemon.log" 2>&1 &
pid="$!"

for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:$port/api/v1/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.25
done

if [[ "$ready" != "1" ]]; then
  echo "health endpoint did not become ready"
  cat "$tmpdir/daemon.log"
  exit 1
fi

start_ns="$(date +%s%N)"
curl -fsS "http://127.0.0.1:$port/api/v1/health" >/dev/null
end_ns="$(date +%s%N)"
elapsed_ms="$(((end_ns - start_ns) / 1000000))"

printf 'health endpoint smoke latency: %sms\n' "$elapsed_ms"
test "$elapsed_ms" -lt 1000
