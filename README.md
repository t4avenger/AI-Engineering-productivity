# TelemetryIQ

Local-first AI engineering intelligence and governance.

The local dashboard is served by the Go daemon (ADR 0002) using HTML templates
and HTMX over the authenticated SQLite-backed session store. Supported Codex
OTLP logs are normalised, sanitised, and persisted locally.

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
`POST /v1/traces`, `POST /v1/metrics`, and `GET /api/v1/ingest/counters`.

## OTLP/HTTP ingest

`POST /v1/logs` is the supported live ingest path for provider log events. It
accepts one JSON OTLP payload with a non-empty `resourceLogs` array, requires
`application/json`, and is limited to 1 MiB. Accepted payloads return
`202 Accepted`. Validation failures return JSON errors with a stable
`error.code` (`malformed_payload`, `invalid_payload`, `unsupported_media_type`,
`payload_too_large`).

`POST /v1/metrics` accepts OTLP JSON `resourceMetrics` the same way. Only
reviewed Codex `codex.skill.injected` datapoints are persisted as canonical
skill events; other metrics are accepted so exporters can flush, but are not
turned into insight rows. `POST /v1/traces` remains `501 Not Implemented` with
`error.code` `not_implemented` so exporters are never told a dropped payload
was accepted.

Raw OTLP payloads are never logged or persisted. The supported, observed Codex
and Claude Code OTLP shapes are normalised and sanitised before canonical
events are saved locally.

## Codex fixture normalisation

The Codex adapter supports reviewed trace fixtures and the observed Codex CLI
0.145.0 OTLP log shape. It retains model and available token metadata, never
uses the sensitive conversation identifier as a session ID, and explicitly
marks unavailable lifecycle and capability data. Canonical events pass through
the privacy sanitizer before SQLite persistence.

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

The local-only privacy pipeline removes prompts, responses, source code, and recognised secret-bearing fields; hashes file paths with an installation-specific HMAC salt; and redacts command arguments. Every retained or transformed field has provenance explaining the action. It is tested by serialising the safe output and proving synthetic prohibited content cannot cross the storage boundary.

## Configuration and privacy

The safe, local-only defaults use schema version `0.1.0`: operational collection, hashed file paths, redacted command arguments, 30-day local retention, and no diagnostics or analytics sharing. Prompts, responses, and source code are always disabled in this configuration version.

Set `TELEMETRYIQ_CONFIG` to load an explicit YAML file:

```yaml
schema_version: "0.1.0"
mode: local-only
collection:
  level: operational
  prompts: false
  responses: false
  source_code: false
  file_paths: hash
  command_arguments: redact
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
