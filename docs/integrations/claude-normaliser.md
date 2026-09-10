# Claude Code Normaliser

Issue #24 adds a capability-bounded Claude Code adapter (`internal/normalize/claude/`)
behind the same conventions as the Codex adapter. It maps the reviewed, sanitised
Claude Code OTLP HTTP/JSON event fixture into canonical events and stable-primitive
model-interaction records. It neither logs nor persists fixture data, and it raises
no capability above what the committed fixtures prove
([capability-matrix.md](capability-matrix.md), [claude-code-capability-probe.md](claude-code-capability-probe.md)).

Supported tool version: Claude Code **2.1.251**. Supported fixture shape:
`payload.source_type = "otlp_http_json_logs"` with a `sample_events[]` array. The
capability-probe shape (`local_cli_capability_probe`) carries no events and yields
an empty result rather than a fabricated all-unknown record. Any other
`source_type` is rejected.

## Event path — `NormalizeEvents`

Each sample event becomes one `canonical.Event`. The event ID is
`claude-code:<session_id>:<event_sequence>`, the session ID is
`claude-code:<session_id>`, `source_schema` is `otel`, and `source_version`
is the pinned tool version. `occurred_at` is the observed `event_timestamp`;
`received_at` is the fixture `captured_at`. Actor and device IDs use the explicit
string `unavailable`.

Every signal the event does not carry is listed in `attributes.unavailable_fields`
so an absent signal is explicit, never silently missing. The `api_request` event
carries model and token identity; the `mcp_server_connection` event carries neither
and additionally lists `model`, `token_usage`, and `cache_usage` as unavailable.
Neither event proves an executed tool call, an MCP **call** invocation, a file
operation, a task outcome, or trace/span correlation. Skill identity is reported
only on `skill_activated` events (see
`fixtures/claude/observed-sanitised/claude-code-2.1.263-skill-activated.json`);
non-skill events omit `skill_detection` rather than fabricating `unavailable`.


Events are sorted by observed time plus stable identifiers and deduplicated by
event ID. Dedup key, ordering key, and an explicit `unknown` task-boundary
confidence are stored under `provider_extensions.correlation`. Safe fields outside
the mapped set survive verbatim under `provider_extensions.event`, minus the
promoted identifiers.

## Model-interaction records — `ExtractModelInteractions`

Only `api_request` events become a `canonical.ModelInteraction`. An
`mcp_server_connection` is a connection lifecycle event, not a tool invocation, so
no `Operation` record is fabricated from it (Claude MCP **calls** stay `unknown`).

Only signals the P2 Claude Code matrix marks `supported`/`partial` are extracted:

- **Model identity** (`supported`) → `model`; absent becomes the explicit string
  `"unknown"` and downgrades the record's provenance.
- **Input/output tokens** (`supported`) → `input_tokens`/`output_tokens`; absent or
  unparseable stays `nil` (serialised `null`), never a fabricated `0`.
- **Cache-read tokens** (`supported`) → `cached_input_tokens`. `cache_creation_tokens`
  has no canonical field, so it is preserved verbatim under `provider_extensions`
  rather than conflated with cache-read.
- **Reasoning tokens, tool calls, task outcome** (`unknown`) are left `nil`/`"unknown"`.
- **Timestamps**: `completed_at` is the observed `api_request` event timestamp;
  `started_at` is derived as `completed_at − duration_ms` and labelled
  `derived_from_duration` under `provider_extensions.timestamps`, so it is never
  mistaken for a directly observed endpoint. `duration_ms` itself is observed.

Records are sorted by `started_at`, `request_id`, and `completed_at`, then
deduplicated by `request_id`.

## Privacy

`NormalizeEvents` and `ExtractModelInteractions` retain `session_id` and
`request_id` as raw provider-prefixed native correlation keys
(`claude-code:<session_id>`) — epic #87 removed ingest-time hiding, so there is no
HMAC fingerprint and no fallback redaction. The fixture validator runs before
mapping, rejecting prohibited field names (prompt/response/source-code content)
so that content is not captured by default; its configurable capture is tracked
in #94.

## Golden fixtures

- `fixtures/claude/expected/claude-code-2.1.251-otlp-events.events.json` — canonical
  events for the committed OTLP-events fixture.
- `fixtures/claude/expected/claude-code-2.1.251-otlp-events.records.json` — canonical
  model-interaction records for the same input.

Regenerate them with `UPDATE_GOLDEN=1 go test ./internal/normalize/claude/ -run Golden`
after a reviewed change, then inspect the diff.

## Out of scope

Session JSONL is not parsed: no reviewed, sanitised JSONL fixture is committed, so
that source stays `unknown`. Live HTTP ingest for Claude Code OTLP logs is
supported via `NormalizeLogs`; the sample-event fixture shape remains the
reviewed golden path for `NormalizeEvents`.

