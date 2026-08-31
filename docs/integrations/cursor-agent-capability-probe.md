# Cursor Agent Capability Probe

Observed on 2026-08-31 with Cursor Agent 2026.05.16-0338208.

Evidence fixtures:
- `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-capability-probe.json`
- `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`

Reference docs: <https://docs.cursor.com/en/account/teams/admin-api>, <https://docs.cursor.com/background-agent/api/overview>.

## Result

The local CLI version and account login are confirmed. An authenticated synthetic print-mode run in an isolated temporary workspace returned a JSON result with usage counters, session/request identifiers, duration, and success state.

The same run used OTLP HTTP/JSON log exporter environment variables pointed at TelemetryIQ's loopback development inspector. TelemetryIQ ingest counters stayed at two accepted Claude payloads, so local OTLP emission was not observed from Cursor Agent.

Observed from the local command-output fixture:
- input and output token counts
- cache-read and cache-write token counts
- session identifier, replaced with a synthetic placeholder before commit
- command success/error state

Not observed:
- model identity
- local OTLP event stream
- tool calls
- MCP calls
- skill invocation identity
- file operations
- trace/span correlation
- broader task outcome contract

Cursor documentation describes team usage events and background-agent APIs. That documentation is setup guidance only for this project; it does not raise a matrix cell without a committed sanitised API or event fixture from the configured account shape.

## Capture boundary

A future event fixture must come from an isolated synthetic workspace or approved usage export, with prompt, response, source-code, path, identifier, and command-argument values removed before commit. Raise Cursor capability cells only when committed fixture evidence supports the specific cell.
