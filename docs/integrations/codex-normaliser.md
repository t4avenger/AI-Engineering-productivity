# Codex Normaliser

Task 007 supports one deliberately narrow fixture shape: a reviewed, sanitised
Codex OTLP trace wrapper with payload.resourceSpans[].scopeSpans[].spans[].
Each span becomes one canonical event. The normaliser is deterministic: its
event ID is codex:<traceId>:<spanId>, its session ID is codex:<traceId>, and
it uses the OTLP startTimeUnixNano plus the fixture captured_at timestamp. It
sorts spans by observed time plus stable identifiers, collapses duplicate
trace/span IDs, and stores dedup, ordering, trace/span, parent-span, and
task-boundary confidence evidence under `provider_extensions.correlation` (see
`docs/architecture/correlation.md`).

The synthetic fixture is not evidence of any real Codex field beyond the shape
it contains. Model, token, cache, tool-call, file-operation, command,
approval, content, repository, task-outcome, and provider-cost fields are
therefore listed in attributes.unavailable_fields. Actor and device IDs use
the explicit string unavailable; unknown values are never represented as zero
values.

Safe fields outside the supported OTLP mapping are preserved verbatim under
provider_extensions.resource, provider_extensions.scope, or
provider_extensions.span. The fixture validator runs before normalisation, so
prohibited field names and likely secrets are rejected without exposing their
values. The normaliser neither logs nor persists fixture data. Task 008 must
apply the privacy pipeline before canonical events cross a storage or
diagnostics boundary.

fixtures/codex/expected/fixture-001.canonical.json is the golden output for
the checked-in synthetic input. Adding a real observed fixture requires the
Task 006 capture procedure, a recorded tool version, an expected canonical
fixture, and a review of any newly evidenced capabilities.

## Observed log support

Codex CLI 0.145.0 was observed exporting OTLP JSON logs with `service.name`
`codex_cli_rs` (interactive TUI) and `codex_exec` (the non-interactive `codex
exec` subcommand); the log adapter accepts both. The log adapter retains reviewed operational attributes (`event.name`,
`model`, `input_token_count`, and `output_token_count`) plus sanitised provider
extensions. Local Codex 0.153.4 metadata also shows tool telemetry such as
`codex.tool_decision`, `codex.tool_result`, `codex.sandbox_outcome`,
`tool_name`, `tool_namespace`, `call_id`, `duration_ms`, `success`,
`mcp_server`, and `mcp_server_origin`. A `codex.tool_result` now becomes a
first-class tool-call signal: event attributes expose `operation_id`,
`category`, `outcome`, and observed `duration_ms`, while
`provider_extensions.tool_call` preserves the provider-reported tool name,
namespace, call ID, status, sequence, truncation flag, and observed provenance.
`codex.sandbox_outcome` becomes a command-execution signal with the same public
timeline operation fields; safe provider details are kept under
`provider_extensions.sandbox_outcome`, using `initial_duration_ms` as the
operation duration when present. Operation IDs include the session identity plus
provider call ID when present, so replay deduplication cannot collapse reused
call IDs from different sessions. `tool_calls` is removed from
`attributes.unavailable_fields` only for `codex.tool_result`;
`command_execution` is removed only for `codex.sandbox_outcome`. Other Codex
log events keep those fields unavailable.

When a `codex.tool_result` carries a non-empty `mcp_server`, the event normaliser
also keeps the existing explicit MCP-use signal under
`provider_extensions.mcp_call` with the provider-reported raw `server_name` (the
correlation identity, `identity_state: provider_reported`) and safe invocation
metadata; the server name is promoted out of generic log attributes to avoid
duplicate evidence but retained in the MCP-specific record for display. Empty
`mcp_server` means the provider did not report that tool result as an MCP server
call, so it remains an internal Codex/tool invocation rather than MCP inventory
evidence. When a `conversation.id` is present, logs use the raw provider-native
session identity `codex:<conversation.id>`. Records without that field fall back
to a non-keyed content ID for uniqueness only (epic #87 — no ingest-time
hiding). Prompt/response/source-code content is not captured by default; its
configurable capture is tracked in #94.

## Model-interaction records

`ExtractLogModelInteractions` (`internal/normalize/codex/records.go`) is the
capability-bounded counterpart to the Event path: it maps the same reviewed
Codex log shape into stable-primitive `canonical.ModelInteraction` records
(issue #21). Only signals the P0 Codex capability matrix marks
`supported`/`partial` are extracted:

- **Model identity** (`supported`) → `model`; absent becomes the explicit
  string `"unknown"` and downgrades the record's provenance, never a fabricated
  value.
- **Input/output tokens** (`partial`) → `input_tokens`/`output_tokens` parsed
  from the OTLP `stringValue`/`intValue`. Absent or unparseable counts stay
  `nil` (serialised as `null`), never a fabricated `0`, so a genuine absence is
  distinguishable from a real zero.
- **Cached and reasoning tokens, task outcome** (`unknown` for typed model records) are left
  `nil`/`"unknown"`; no typed model field is fabricated from provider-extension evidence.
- **Tool-call operations** (`supported` for `codex.tool_result`) and **command-execution operations** (`supported` for `codex.sandbox_outcome`) are extracted into `canonical.Operation` by `ExtractLogOperations` and exposed on timeline events through operation ID, category, outcome, and duration. Operation ordering uses observed log timestamps when present, falling back to receive time only when absent. Known observed Codex tool names map conservatively (`exec_command` → shell command, `apply_patch` → filesystem write); sandbox outcomes are categorised as shell commands because the reviewed event proves sandboxed command execution but does not retain raw command arguments. Unknown tool-result names stay `unknown`.
- **MCP-backed tool results** (`partial` for MCP inventory) are represented only when Codex reports a non-empty `mcp_server`; the provider-reported raw server name is retained under `provider_extensions.mcp_call` and is itself the correlation identity.
- **Session/request identity** uses the raw `codex:<conversation.id>` for the
  session when a conversation ID is present. Request IDs and records without a
  conversation ID use a non-keyed content ID for deterministic correlation and
  uniqueness only — never an HMAC fingerprint (epic #87).
- **Correlation evidence** records the dedup key, ordering key, and explicit
  unknown task-boundary confidence under `provider_extensions.correlation`; log
  records are sorted by `started_at`, `request_id`, and `completed_at`, then
  deduplicated by `request_id` before being returned.

A record is emitted only when the log `event.name` is a whitelisted
model-interaction event (`codex.sse_event`) and it carries at least a model or a
token count, so a bare event never becomes an all-unknown record. Non-extracted
resource and log attributes plus the severity are preserved verbatim under
`provider_extensions`. `fixtures/codex/expected/codex-0.145.0-logs.records.json`
is the golden output for the checked-in observed-sanitised input.

## Codex OTLP metrics

`NormalizeMetrics` maps OTLP `resourceMetrics` from Codex (`codex_cli_rs` /
`codex_exec`) only where a committed fixture proves a product-facing signal and
the canonical event can preserve absent-vs-zero semantics. Metrics outside the
mapped set remain HTTP-accepted but intentionally ignored, so Codex exporters can
flush without TelemetryIQ inventing rows for internal timings, startup counters,
SQLite/cache internals, or app/plugin inventory signals.

### Skill metrics

`codex.skill.injected` datapoints with a non-empty `skill` attribute become
explicit named skill events with `provider_extensions.skill_detection =
"explicit"` and `provider_extensions.skill.{name,outcome,invoke_type}`. A live
Codex CLI 0.153.4 trigger-style `bmad:brainstorm` probe emitted
`codex.skill.turn.duration_seconds` as a histogram with `status` and
`plugin_id=unattributed`, but no skill name; those datapoints become inferred
coverage events with `provider_extensions.skill_detection = "inferred"` and no
`provider_extensions.skill` record.

Golden: `fixtures/codex/expected/codex-0.153.4-skill-injected-metrics.events.json`
for `fixtures/codex/observed-sanitised/codex-0.153.4-skill-injected-metrics.json`.

### Token-usage metrics

`codex.turn.token_usage` was observed in the Codex 0.153.4 metric inventory at
`fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-token-usage.json`.
The committed replay fixture
`fixtures/codex/observed-sanitised/codex-0.153.4-turn-token-usage-metrics.json`
uses synthetic counts for the observed `token_type` values and normalises each
valid histogram datapoint into a `codex.turn.token_usage` canonical event:

- `input` -> `attributes.input_token_count`
- `output` -> `attributes.output_token_count`
- `cached_input` -> `attributes.cached_input_token_count`
- `cache_write_input` -> `attributes.cache_write_input_token_count`
- `reasoning_output` -> `attributes.reasoning_token_count`
- `total` -> `attributes.total_token_count`

The token count comes from the histogram datapoint `sum`. Missing, malformed, or
negative sums produce no event rather than a fabricated zero. Token metric event
IDs include service/resource, scope, token type, model, timestamp, series
attributes, and token sum so distinct resource or series datapoints do not
collapse during replay deduplication. The datapoint `count` is preserved only as
metric evidence under `provider_extensions.metric`. Non-promoted metric and
resource attributes are retained only from a reviewed allowlist
(`app.version`, `auth_mode`, `deployment.environment`, `originator`,
`session_source`, `tmp_mem_enabled`); other fields are discarded before
persistence rather than filtered by denylist.

Metric token events are deliberately non-priceable in the cost calculator to
avoid double-counting normal Codex exports where `codex.sse_event` logs and
`codex.turn.token_usage` metrics can describe the same turn. Cost attribution for
the full metric token surface remains gated on #118, where an authoritative
source/dedup policy will be chosen.

Golden: `fixtures/codex/expected/codex-0.153.4-turn-token-usage-metrics.events.json`
for `fixtures/codex/observed-sanitised/codex-0.153.4-turn-token-usage-metrics.json`.

The static-analysis gate enforces cyclomatic complexity of 15 or lower for
each Go function. The normaliser separates fixture, resource, scope, and span
handling so that nesting does not conceal excessive decision complexity.
