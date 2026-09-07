#!/usr/bin/env python3
"""
Ingest Cursor Agent `--output-format stream-json` output into a local TelemetryIQ daemon.

This script reads Cursor Agent stream-json from stdin, extracts ONLY the
privacy-safe `init` (model/session) and `result` (tokens/outcome) records, and
POSTs a minimal Cursor ingest envelope to TelemetryIQ:

  POST /v1/cursor-agent

It intentionally ignores `user` and `assistant` message records so prompt/response
content is not sent to TelemetryIQ by default.
"""

from __future__ import annotations

import argparse
import datetime as dt
import http.client
import json
import os
import subprocess
import sys
import urllib.parse


def utc_now_rfc3339() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def cursor_agent_version() -> str:
    try:
        out = subprocess.check_output(["cursor-agent", "--version"], stderr=subprocess.DEVNULL, text=True).strip()
        return out or "unknown"
    except Exception:
        return "unknown"


def extract_safe_init(obj: dict) -> dict | None:
    if obj.get("type") != "system" or obj.get("subtype") != "init":
        return None
    out: dict = {"type": "system", "subtype": "init"}
    if isinstance(obj.get("model"), str) and obj["model"].strip():
        out["model"] = obj["model"].strip()
    if isinstance(obj.get("session_id"), str) and obj["session_id"].strip():
        out["session_id"] = obj["session_id"].strip()
    return out


def extract_safe_result(obj: dict) -> dict | None:
    if obj.get("type") != "result":
        return None
    usage = obj.get("usage") if isinstance(obj.get("usage"), dict) else {}
    out: dict = {
        "type": "result",
        "subtype": obj.get("subtype"),
        "is_error": bool(obj.get("is_error", False)),
        "duration_ms": obj.get("duration_ms"),
        "duration_api_ms": obj.get("duration_api_ms"),
        "session_id": obj.get("session_id"),
        "request_id": obj.get("request_id"),
        "usage": {
            "inputTokens": usage.get("inputTokens"),
            "outputTokens": usage.get("outputTokens"),
            "cacheReadTokens": usage.get("cacheReadTokens"),
            "cacheWriteTokens": usage.get("cacheWriteTokens"),
        },
    }
    # Drop keys whose values are None so the daemon's validator doesn't receive junk.
    out = {k: v for (k, v) in out.items() if v is not None}
    out["usage"] = {k: v for (k, v) in out.get("usage", {}).items() if v is not None}
    return out


def parse_daemon_origin(origin: str) -> tuple[str, str, int]:
    """
    Parse a daemon origin like "http://localhost:8080" into (scheme, host, port).

    This intentionally does NOT accept file:// or arbitrary URL paths.
    """
    parsed = urllib.parse.urlparse(origin.strip())
    if parsed.scheme not in ("http", "https"):
        raise ValueError("daemon origin must be http:// or https://")
    if parsed.path not in ("", "/") or parsed.params or parsed.query or parsed.fragment:
        raise ValueError("daemon origin must not include a path, query, or fragment")
    if not parsed.hostname:
        raise ValueError("daemon origin must include a hostname")
    port = parsed.port
    if port is None:
        port = 443 if parsed.scheme == "https" else 80

    # Local-first safety: only allow loopback targets by default.
    # (If you need a different host, set up a loopback proxy rather than widening this script.)
    if parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        raise ValueError("daemon origin host must be loopback (localhost/127.0.0.1/::1)")
    return parsed.scheme, parsed.hostname, int(port)


def post_json(origin: str, path: str, payload: dict) -> int:
    scheme, host, port = parse_daemon_origin(origin)
    body = json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json", "Content-Length": str(len(body))}

    conn_cls = http.client.HTTPSConnection if scheme == "https" else http.client.HTTPConnection
    conn = conn_cls(host, port, timeout=10)
    try:
        conn.request("POST", path, body=body, headers=headers)
        resp = conn.getresponse()
        # Drain body for connection hygiene.
        resp.read()
        return resp.status
    finally:
        conn.close()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--daemon",
        default=os.environ.get("TELEMETRYIQ_DAEMON", "http://localhost:8080"),
        help="TelemetryIQ daemon origin (default: http://localhost:8080 or TELEMETRYIQ_DAEMON)",
    )
    args = parser.parse_args()

    init = None
    result = None
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except Exception:
            continue
        if isinstance(obj, dict):
            if init is None:
                init = extract_safe_init(obj)
            if result is None:
                result = extract_safe_result(obj)
        if init is not None and result is not None:
            break

    if result is None:
        print("No Cursor result record found on stdin.", file=sys.stderr)
        return 2

    envelope = {
        "provider": "cursor",
        "tool": "cursor-agent",
        "tool_version": cursor_agent_version(),
        "captured_at": utc_now_rfc3339(),
        "payload": {
            "source_type": "local_cli_stream_json",
            "init": init,
            "result": result,
        },
    }
    # Drop init when absent; the daemon validator allows it.
    if envelope["payload"]["init"] is None:
        del envelope["payload"]["init"]

    status = post_json(args.daemon.rstrip("/"), "/v1/cursor-agent", envelope)
    if status != 202:
        print(f"Unexpected daemon status {status}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

