# TelemetryIQ

Local-first AI engineering intelligence and governance.

The local dashboard is served by the Go daemon (ADR 0002) using HTML templates
and HTMX over the authenticated SQLite-backed session store. Supported Codex
OTLP logs are normalised and persisted locally with no ingest-time hiding
(epic #87).

## Requirements

- Go 1.26.6 or newer
- Node.js 24.0.0 or newer (Playwright e2e only)
- npm 10.x or newer (Playwright e2e only)

## Local setup

```bash
make bootstrap
make hooks-install
```

Run the daemon:

```bash
make run
```

Open `http://localhost:8080/`, run `make auth-token`, and paste the token into
the Unlock page. The token is stored only in an httpOnly Secure cookie for the
browser session on loopback (use the `localhost` hostname so browsers accept the
Secure cookie over local HTTP). The daemon exposes HTML dashboard routes plus
`GET /api/v1/health`, `GET /api/v1/sessions`, `GET /api/v1/sessions/{id}`,
`DELETE /api/v1/sessions/{id}`, `DELETE /api/v1/sessions`, `POST /v1/logs`,
`POST /v1/traces`, `POST /v1/metrics`, `POST /v1/claude/transcript`,
`POST /v1/cursor-agent`, and `GET /api/v1/ingest/counters`.

## OTLP/HTTP ingest

`POST /v1/logs` is the supported live ingest path for provider log events. It
accepts one OTLP payload with a non-empty `resourceLogs` array as either
`application/json` or `application/x-protobuf` (OTLP ExportLogsServiceRequest),
optionally with `Content-Encoding: gzip`, and is limited to 1 MiB (compressed
and decompressed). Accepted payloads return `202 Accepted`. Validation
failures return JSON errors with a stable `error.code` (`malformed_payload`,
`invalid_payload`, `unsupported_media_type`, `payload_too_large`).

`POST /v1/metrics` accepts the same content types with a non-empty
`resourceMetrics` array (JSON or ExportMetricsServiceRequest protobuf). Only
reviewed Codex `codex.skill.injected` datapoints, Claude Code
`claude_code.token.usage` datapoints, and Cursor Enterprise
`cursor.token.usage` datapoints are persisted as canonical events; other
metrics are accepted so exporters can flush, but are not turned into insight
rows.

`POST /v1/traces` accepts OTLP JSON `resourceSpans` (JSON-only today) and
persists Claude Code's enhanced-telemetry beta span tree
(`claude_code.interaction` -> `claude_code.llm_request`) as canonical span
events, resolving the former `501`. Codex CLI 0.153.4 did not emit `/v1/traces`
payloads in the reviewed F1 capture, so Codex trace export is documented as
unsupported for that version. If a Codex-shaped spans payload arrives anyway, it
is accepted so exporters can flush, but is not persisted.

The raw OTLP envelope is never logged or persisted verbatim. The supported,
observed Codex and Claude Code OTLP shapes are normalised into canonical events
before being saved locally; epic #87 removed ingest-time hiding, so raw
provider-native IDs, paths, and commands are retained inside those events.

## Claude Code session transcript ingest

`POST /v1/claude/transcript` accepts a Claude Code session JSONL transcript
(newline-delimited JSON, Content-Type `application/x-ndjson` or
`application/jsonl`, up to 32 MiB). It normalises `assistant` records into
`assistant_message` events (model + token usage, correlated to the same
`claude-code:<sessionId>` as OTLP) and deliberately does not persist prompt,
response, or tool content bodies. See
`docs/integrations/claude-normaliser.md` and
`docs/integrations/claude-transcript-deployment.md`.

## Cursor ingest

**Org/SaaS path (supported):** Cursor Enterprise **OpenTelemetry Export** pushes
OTLP/HTTP **binary protobuf** to `/v1/logs` and `/v1/metrics`. See
`docs/integrations/cursor-enterprise-otel.md`. MDM does not ship per-laptop
Cursor usage telemetry; Enterprise plan + admin Team Settings are required.

`POST /v1/logs` and `POST /v1/metrics` accept both `application/json` and
`application/x-protobuf`. Enterprise `cursor.telemetry` normalisation currently
persists `cursor.api.request` logs and `cursor.token.usage` metrics (#130);
other Cursor wire families are accepted but not mapped yet.

**Local-dev only (not org setup):** Cursor Agent stream-json can be posted to
`POST /v1/cursor-agent` via `scripts/ingest-cursor-agent-stream-json.py` or
`scripts/cursor-agent-tiq`. See
`docs/integrations/cursor-agent-auto-reporting.md`.

```bash
TELEMETRYIQ_DAEMON=http://localhost:8080 scripts/cursor-agent-tiq "say ok"
```

## Codex fixture normalisation

The Codex adapter supports reviewed trace fixtures and the observed Codex CLI
0.145.0 OTLP log shape. It retains model and available token metadata, uses the
raw provider-native `codex:<conversation.id>` as the session ID when present, and
explicitly marks unavailable lifecycle and capability data. Canonical events are
persisted to SQLite verbatim — epic #87 removed the ingest-time sanitizer, so raw
provider-native IDs, paths, and commands are stored as normalised.

## Session API

`GET /api/v1/sessions` returns the stable envelope `{data, pagination}`. It accepts `limit` (1–100), opaque `cursor`, `tool`, `model`, `outcome`, `started_after`, and `started_before` (RFC3339) filters. Results are reverse chronological and use `next_cursor` for pagination. A model filter only matches model metadata that was actually observed; unavailable model values are not invented.

`GET /api/v1/sessions/{id}` returns `{data}` or a stable JSON error. The API reads the local database at the platform configuration directory (`telemetryiq/telemetryiq.db`); the database directory is mode `0700` and database file mode is `0600`.

`DELETE /api/v1/sessions/{id}` permanently deletes that session and its retained events. The local dashboard requires confirmation before it sends this request.

## Local dashboard

The Go/HTMX dashboard provides Home, Sessions, Session Detail, Insights,
Integrations, Privacy, and Costs pages on the daemon origin. Home emphasises
orchestration usage (not cost). Missing data is labelled unavailable,
integrations appear only after local session data has been observed, and the
Privacy page uses a typed confirmation before bulk deletion.

## Privacy pipeline

The local-only edition does not capture prompts, responses, or source code by default (configurable capture is tracked in #94). Epic #87 removed ingest-time hiding: there is no sanitiser choke point, no HMAC fingerprinting, and no path/command tokenisation. Raw provider-native identifiers, file paths, and command lines are persisted verbatim and shown to the local user so the operator on their own single-user machine can see the actual data to act on it. Pure classifiers (`ClassifyPath`/`ClassifyCommandAccess`) run over those raw values to power governance signals such as risky-access detection.

## Configuration and privacy

The safe, local-only defaults use schema version `0.1.0`: operational collection, 30-day local retention, and no diagnostics or analytics sharing. Prompts, responses, and source code are always disabled in this configuration version; raw file paths and command lines are retained (epic #87 — no ingest-time hiding).

Set `TELEMETRYIQ_CONFIG` to load an explicit YAML file:

```yaml
schema_version: "0.1.0"
mode: local-only
collection:
  level: operational
  prompts: false
  responses: false
  source_code: false
  tool_calls: true
  model_usage: true
storage:
  destination: local
  retention_days: 30
sharing:
  diagnostics: false
  anonymous_analytics: false
  research_sessions: explicit-only
```

The daemon rejects unknown fields, unsupported schema versions, content capture, non-local storage, unsafe sharing, non-loopback hosts, and invalid ports with actionable startup errors. `TELEMETRYIQ_HOST` (default `localhost`) and `TELEMETRYIQ_PORT` (default `8080`) override the loopback server address.
