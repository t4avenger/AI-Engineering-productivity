# Cursor Enterprise OpenTelemetry Export (org/SaaS path)

Supported **organisation / SaaS** Cursor ingest is **Cursor Enterprise
OpenTelemetry Export** only.

## Honest constraints

| Claim | Reality |
| --- | --- |
| MDM pushes Cursor usage telemetry from each laptop | **False.** Cursor MDM/policies cover app controls (team ID, updates, Workspace Trust, etc.), not usage telemetry. |
| Zero developer CLI for org fleets | **True only on Cursor Enterprise**, via admin **Team Settings → OpenTelemetry Export** (server-side push). |
| Local `cursor-agent` stream-json wrapper | **Not** the org/SaaS path. Local-dev evidence only (`POST /v1/cursor-agent`). |
| TelemetryIQ can receive Cursor Enterprise export | Requires **OTLP/HTTP binary protobuf** on `/v1/logs` and `/v1/metrics` (`application/x-protobuf`). JSON-only collectors will not work. |

**Non-Enterprise Cursor:** there is no genuine MDM-friendly, zero-CLI event
stream TelemetryIQ can promise for org coverage.

## Admin setup (Team Settings)

> **Protected gateway required.** TelemetryIQ's `/v1/logs` and `/v1/metrics`
> handlers do **not** authenticate Cursor's `Authorization` (or other) headers
> today. Do **not** put the daemon itself on the public internet. Terminate TLS
> and authenticate at a gateway / collector in front of TelemetryIQ (or wait for
> the cross-cutting ingest auth / tenant-binding epic). The Cursor destination
> auth header protects the **gateway**, not the unauthenticated daemon routes.

1. Deploy a **public HTTPS gateway** that accepts OTLP/HTTP protobuf, enforces
   auth (and optionally Cursor egress IP allowlisting), then forwards privately
   to TelemetryIQ.
2. Ensure the path that reaches TelemetryIQ serves `/v1/logs` and `/v1/metrics`
   with `Content-Type: application/x-protobuf` (supported as of issue #129).
   `Content-Encoding: gzip` is also accepted.
3. In Cursor **Team Settings → OpenTelemetry Export**, create a destination:
   - Base URL: `https://otel.example.com` (no `/v1` suffix; Cursor appends
     `/v1/metrics` and `/v1/logs`)
   - Auth header Cursor should send (for example `Authorization: Bearer <token>`)
     — validated by the **gateway**, not by TelemetryIQ yet
4. Use **Test connection**, then enable export.

Cursor documents fixed egress source IPs and the full attribute surface in:

- https://cursor.com/docs/enterprise/opentelemetry-export
- https://cursor.com/docs/enterprise/opentelemetry-export/wire

## What this repository does today

- **Accepted:** OTLP/HTTP JSON and binary protobuf on `/v1/logs` and `/v1/metrics`
  (optional `Content-Encoding: gzip`).
- **Normalised (#130):** Cursor `cursor.telemetry` subset —
  `cursor.token.usage` metrics and `cursor.api.request` logs — into canonical
  events via `cursor.NormalizeMetrics` / `cursor.NormalizeLogs`. Other wire
  families are accepted without mapping until fixtures prove them. Synthetic
  wire fixtures live under `fixtures/cursor/observed-sanitised/cursor-otel-0.1.0-*.json`.
- **Not yet:** live Enterprise capture (synthetic fixtures only); full wire
  surface (tool calls, cost, skills, cloud_agent, grok_bot); Integrations UX
  (#132).
- **Local-dev only:** `POST /v1/cursor-agent` and `scripts/cursor-agent-tiq`
  (see [cursor-agent-auto-reporting.md](cursor-agent-auto-reporting.md)).

## Auth / multi-tenant note

Ingest auth and tenant binding for shared HTTPS ingress are cross-cutting and
not Cursor-specific. Until that epic lands, Enterprise destination wiring is
**forward-looking against a protected gateway only**.
