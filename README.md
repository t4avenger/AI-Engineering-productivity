# TelemetryIQ

Local-first AI engineering intelligence and governance.

Task 002 provides the repository scaffold, loopback Go daemon, React health page, and validated privacy-safe configuration. It does not ingest telemetry, use a database, or implement authentication.

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

The daemon binds to `127.0.0.1:8080` by default and exposes `GET /api/v1/health`.

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

Some integration, contract, and fuzz checks report not applicable until their corresponding product capabilities are implemented.

## Current scope

Task 002 deliberately excludes telemetry ingestion, persistence, provider adapters, analytics, and third-party telemetry. Authentication will be introduced in a later local-only task without weakening loopback defaults.
