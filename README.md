# TelemetryIQ

Local-first AI engineering intelligence and governance.

Task 010 provides a privacy-safe local dashboard over the authenticated SQLite-backed session API. Supported Codex OTLP logs are normalised, sanitised, and persisted locally.

## Requirements

- Go 1.25.12 or newer
- Node.js 24.0.0 or newer
- npm 10.x or newer

## Local setup

```bash
make bootstrap
make hooks-install
```

Run the daemon and web app in separate terminals:

```bash
make run-daemon
make run-web
```

The daemon binds to `127.0.0.1:8080` by default. Run `telemetryiq auth-token` and paste the result into the dashboard once per browser session; the token is never logged or retained by the browser after that session. The daemon exposes `GET /api/v1/health`, `GET /api/v1/sessions`, `GET /api/v1/sessions/{id}`, `DELETE /api/v1/sessions/{id}`, `POST /v1/traces`, `POST /v1/logs`, and `GET /api/v1/ingest/counters`.

## OTLP/HTTP ingest proof

`POST /v1/traces` and `POST /v1/logs` accept one JSON OTLP payload with a non-empty `resourceSpans` or `resourceLogs` array. Requests must use `application/json` and are limited to 1 MiB. Each endpoint returns `202 Accepted` for accepted payloads and JSON errors with a stable `error.code` for malformed, invalid, unsupported-media-type, or oversized requests.

Raw OTLP payloads are never logged or persisted. The supported, observed Codex
OTLP log shape is normalised and sanitised before its canonical event is saved
locally. Other accepted OTLP payloads remain receive-only until an adapter has
reviewed evidence.

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

The Mantine-based dashboard provides Home, Sessions, Session Detail, Integrations, and Privacy pages. It labels missing data as unavailable, lists an integration only after local session data has been observed, and presents the enforced local-only privacy defaults. The token-entry flow retains the value only for the browser session; the Privacy page uses a typed confirmation before bulk deletion. Configure `VITE_API_URL` when the session API has a non-default origin; `VITE_HEALTH_URL` configures the health endpoint.

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

The daemon rejects unknown fields, unsupported schema versions, content capture, non-local storage, unsafe sharing, non-loopback hosts, and invalid ports with actionable startup errors. `TELEMETRYIQ_HOST` (default `127.0.0.1`) and `TELEMETRYIQ_PORT` (default `8080`) override the loopback server address; `VITE_HEALTH_URL` configures the web health endpoint.

## Verification

- `make format` / `make format-check`
- `make lint`
- `make static-analysis`
- `make test-unit`, `make test-component`, `make test-e2e`, `make test-race`
- `make coverage`, `make security-scan`, `make build`
- `make verify` and `make verify-push`

Contract and fuzz checks report not applicable until their corresponding product capabilities are implemented.

## Current scope

Cost insights, analytics, and third-party telemetry remain out of scope. Management API authentication is local-token based and preserves loopback defaults.
