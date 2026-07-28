# TelemetryIQ

Local-first AI engineering intelligence and governance.

Task 009 adds a privacy-safe, SQLite-backed session API. It does not yet connect live ingestion to storage or implement local API authentication.

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

The daemon binds to `127.0.0.1:8080` by default and exposes `GET /api/v1/health`, `GET /api/v1/sessions`, `GET /api/v1/sessions/{id}`, `POST /v1/traces`, `POST /v1/logs`, and `GET /api/v1/ingest/counters`.

## OTLP/HTTP ingest proof

`POST /v1/traces` and `POST /v1/logs` accept one JSON OTLP payload with a non-empty `resourceSpans` or `resourceLogs` array. Requests must use `application/json` and are limited to 1 MiB. Each endpoint returns `202 Accepted` for accepted payloads and JSON errors with a stable `error.code` for malformed, invalid, unsupported-media-type, or oversized requests.

This proof keeps only aggregate accepted/rejected counters in process memory. It neither logs nor persists raw telemetry content. The live ingest-to-normalisation-to-storage connection remains a later task.

## Codex fixture normalisation

The Task 007 normaliser accepts only reviewed, sanitised Codex OTLP trace fixtures. It deterministically maps one trace span to one canonical event, preserves safe unknown fields in provider extensions, and explicitly marks capabilities absent from the synthetic fixture as unavailable. Task 008 persists canonical events through a storage-agnostic interface after sanitisation.

## Session API

`GET /api/v1/sessions` returns the stable envelope `{data, pagination}`. It accepts `limit` (1–100), opaque `cursor`, `tool`, `model`, `outcome`, `started_after`, and `started_before` (RFC3339) filters. Results are reverse chronological and use `next_cursor` for pagination. A model filter only matches model metadata that was actually observed; unavailable model values are not invented.

`GET /api/v1/sessions/{id}` returns `{data}` or a stable JSON error. The API reads the local database at the platform configuration directory (`telemetryiq/telemetryiq.db`); the database directory is mode `0700` and database file mode is `0600`.

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

Task 009 deliberately excludes live ingest-to-storage wiring, authentication, analytics, and third-party telemetry. Authentication will be introduced in a later local-only task without weakening loopback defaults.
