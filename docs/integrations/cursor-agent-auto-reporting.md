# Cursor Agent auto-reporting to the TelemetryIQ daemon (local)

Cursor Agent does not currently arrive in TelemetryIQ via OTLP logs in this
repository’s reviewed captures. Instead, TelemetryIQ supports a dedicated
local-only ingest endpoint:

- `POST /v1/cursor-agent`

To make reporting automatic when you run Cursor Agent from a terminal, use the
wrapper script:

- `scripts/cursor-agent-tiq`

## Setup

1. Start the daemon:

```bash
make run
```

2. Run Cursor Agent via the wrapper:

```bash
TELEMETRYIQ_DAEMON=http://localhost:8080 scripts/cursor-agent-tiq "say ok"
```

## Privacy behaviour (important)

- The wrapper runs Cursor Agent in `--output-format stream-json`.
- The ingest helper **ignores** `user` and `assistant` message records, so prompt
  and response text are not posted to TelemetryIQ by default.
- Only the privacy-safe subset (system init + result tokens/outcome) is posted.

## Optional: make it your default `cursor-agent`

If you want `cursor-agent` to always report to TelemetryIQ when used in this
workspace, you can add a shell alias (example for bash):

```bash
alias cursor-agent="TELEMETRYIQ_DAEMON=http://localhost:8080 $(pwd)/scripts/cursor-agent-tiq"
```

Rollback:

```bash
unalias cursor-agent
```

This repo intentionally does **not** auto-edit your shell rc files.

