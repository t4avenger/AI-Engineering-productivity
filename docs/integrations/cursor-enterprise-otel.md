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

1. Run TelemetryIQ (or a gateway in front of it) behind **public HTTPS**.
2. Ensure `/v1/logs` and `/v1/metrics` accept `Content-Type: application/x-protobuf`
   (supported as of issue #129).
3. In Cursor **Team Settings → OpenTelemetry Export**, create a destination:
   - Base URL: `https://otel.example.com` (no `/v1` suffix; Cursor appends
     `/v1/metrics` and `/v1/logs`)
   - Auth header Cursor should send (for example `Authorization: Bearer <token>`)
4. Use **Test connection**, then enable export.

Cursor documents fixed egress source IPs and the full attribute surface in:

- https://cursor.com/docs/enterprise/opentelemetry-export
- https://cursor.com/docs/enterprise/opentelemetry-export/wire

## What this repository does today

- **Accepted:** OTLP/HTTP JSON and binary protobuf on `/v1/logs` and `/v1/metrics`.
- **Not yet:** Cursor `cursor.telemetry` field normalisation into canonical
  sessions (tracked under epic #128 / normaliser follow-up). Protobuf ingest is
  the unblocker so Enterprise export can reach the daemon without 415s.
- **Local-dev only:** `POST /v1/cursor-agent` and `scripts/cursor-agent-tiq`
  (see [cursor-agent-auto-reporting.md](cursor-agent-auto-reporting.md)).

## Auth / multi-tenant note

Ingest auth and tenant binding for shared HTTPS ingress are cross-cutting and
not Cursor-specific. Do not expose an unauthenticated collector on the public
internet until that epic lands; until then treat Enterprise destination wiring
as forward-looking against a protected gateway.
