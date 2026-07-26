# TelemetryIQ

Local-first AI engineering intelligence and governance.

Task 004 adds a minimal OTLP/HTTP traces ingest proof to the repository scaffold, loopback Go daemon, React health page, validated privacy-safe configuration, and canonical schemas. It does not persist telemetry, use a database, or implement authentication.

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

The daemon binds to `127.0.0.1:8080` by default and exposes `GET /api/v1/health`, `POST /v1/traces`, and `GET /api/v1/ingest/counters`.

## OTLP/HTTP ingest proof

`POST /v1/traces` accepts one JSON OTLP traces payload with a non-empty `resourceSpans` array. Requests must use `application/json` and are limited to 1 MiB. The endpoint returns `202 Accepted` for accepted payloads and JSON errors with a stable `error.code` for malformed, invalid, unsupported-media-type, or oversized requests.

This proof keeps only aggregate accepted/rejected counters in process memory. It neither logs nor persists raw telemetry content; field redaction and canonical persistence are intentionally deferred to Task 005 and later work.

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

Task 004 deliberately excludes telemetry persistence, provider adapters, analytics, and third-party telemetry. Authentication will be introduced in a later local-only task without weakening loopback defaults.
