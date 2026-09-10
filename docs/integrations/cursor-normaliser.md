# Cursor normaliser (Cursor Agent)

This document describes what TelemetryIQ extracts from the **reviewed, sanitised**
Cursor Agent fixtures committed under `fixtures/cursor/observed-sanitised/`.

Per `PRODUCT_MAP.md` section 0 and `docs/integrations/capability-matrix.md`, the
Cursor adapter is **capability-bounded**: it promotes only the subset proven by
fixtures and never fabricates parity with other providers.

## Entry points

The Cursor adapter lives under `internal/normalize/cursor/`:

- `cursor.Normalize`: emits a canonical `canonical.Event` for the Cursor run
  result when the fixture contains a reviewed result payload.
- `cursor.ExtractModelInteractions`: emits a stable-primitive
  `canonical.ModelInteraction` record from the same reviewed result payload.

Both entry points run `fixture.Validate` and therefore accept **only** fixtures
that have already crossed the shared sanitisation boundary (no prompts, no
responses, no source code, no file paths, no command arguments).

## Supported fixture shapes (`payload.source_type`)

- `local_cli_print_json`
  - Emits one result `Event`.
  - Emits one `ModelInteraction` record with token and cache-read usage.
  - **Model identity is not present** in this shape, so `ModelInteraction.model`
    is `"unknown"` and provenance is downgraded.

- `local_cli_stream_json`
  - Emits one result `Event`.
  - Emits one `ModelInteraction` record with token and cache-read usage.
  - **Model identity is partial**: when a reviewed `payload.init.model` exists
    (see the committed stream-json fixture), it is promoted into
    `ModelInteraction.model`.

- `local_cli_capability_probe`
  - Emits **no** events and **no** records (capability probes are evidence, not
    behaviour events).

## Promoted signals (verified subset)

The following signals are promoted because reviewed fixtures prove them:

- **Session boundaries (partial)**: `result.session_id` is retained as the
  provider-prefixed native local correlation key `cursor-agent:<session_id>`.
- **Token usage (supported)**: `result.usage.inputTokens` and
  `result.usage.outputTokens` → `ModelInteraction.input_tokens` /
  `ModelInteraction.output_tokens` (nullable when absent; never fabricated 0).
- **Cache usage (supported)**: `result.usage.cacheReadTokens` →
  `ModelInteraction.cached_input_tokens`. `cacheWriteTokens` is preserved under
  `provider_extensions` (no canonical cache-write field yet).
- **Task outcome (partial)**: `result.subtype` and `result.is_error` are mapped
  into a provider-completion outcome contract (`provider_extensions.outcome_contract`).

## Explicit non-goals (until fixtures prove them)

The Cursor adapter does **not** promote any of the following (they remain
explicitly unavailable/unknown and are never fabricated):

- tool calls / operations
- MCP calls
- skill invocation identity
- file operations
- command execution
- approvals
- reasoning tokens
- repository context
- prompt/response content
- trace/span correlation

## Privacy handling

- **Session and request IDs are retained raw**: `session_id` and `request_id`
  appear in canonical output as `cursor-agent:<session_id>` /
  `cursor-agent:<request_id>` verbatim — epic #87 removed ingest-time hiding, so
  there is no HMAC fingerprint.
- **No content is retained**: `fixture.Validate` rejects prohibited message-content
  fields at the ingest boundary, so prompt/response content is not captured by
  default; its configurable capture is tracked in #94.

## Live ingest into the daemon (local-only)

Cursor Agent does **not** currently emit OTLP logs to `POST /v1/logs` in this
repository’s reviewed captures. To still include Cursor runs in the local
daemon, TelemetryIQ supports a dedicated local-only ingest path:

- `POST /v1/cursor-agent`

This endpoint accepts a **minimal** Cursor envelope (model/session init + run
result usage/outcome) and rejects unexpected fields so prompt/response content
is not ingested by default.

To ingest a real Cursor run, you can use the helper script:

```bash
cursor-agent agent --print --output-format stream-json --mode ask --trust "ok" \
  | python3 scripts/ingest-cursor-agent-stream-json.py --daemon http://localhost:8080
```

Only the `system/init` and `result` records are sent; `user`/`assistant` message
records are ignored.

