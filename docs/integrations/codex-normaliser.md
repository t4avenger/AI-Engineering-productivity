# Codex Normaliser

`codex.NormalizeTraces` ingests the observed Codex CLI 0.154.0 OTLP trace
surface from `codex_exec` and `codex_cli_rs`. Both OTLP/HTTP JSON and binary
protobuf feed the same adapter. Recognized resources are hard-fail normalized:
a malformed supported span returns `422` for the whole batch instead of being
accepted and silently dropped. The CLI 0.153.4 no-trace result remains valid
for that version (#112); trace export is therefore version-dependent.

For live traces, the event ID is `codex:<traceId>:<spanId>` and the trace-only
session ID is `codex:trace:<traceId>`. Trace-only rows are observations with
`identity_source=trace.id`; they are not fabricated conversation joins. The
adapter uses OTLP `startTimeUnixNano` plus receipt time and sorts spans by
observed time plus stable identifiers, collapses duplicate trace/span IDs, and
stores dedup, ordering, trace/span, parent-span, and task-boundary confidence
evidence under `provider_extensions.correlation` (see
`docs/architecture/correlation.md`).

The observed `session_task.turn` span promotes input, cached-input,
cache-write-input, output, and reasoning-output token counts when present.
Other span attributes remain provider-specific evidence; model, tool, file,
command, approval, content, repository, task-outcome, and provider-cost
semantics are not inferred from internal span names.

Resource, scope, span, and unknown attribute fields are preserved under
`provider_extensions`; the adapter does not log them. Committed fixtures remain
sanitized synthetic-only evidence even though the local ingest path retains the
raw values it receives under epic #87.

`fixtures/codex/expected/codex-0.154.0-trace-spans.events.json` is the golden
output for the version-pinned observed-sanitized trace fixture.

## Observed log support

Codex CLI 0.145.0 was observed exporting OTLP JSON logs with `service.name`
`codex_cli_rs` (interactive TUI) and `codex_exec` (the non-interactive `codex
exec` subcommand); the log adapter accepts both. The log adapter retains reviewed operational attributes (`event.name`,
`model`, `input_token_count`, `output_token_count`, cached-input tokens from
the observed `cached_token_count` log key, and `reasoning_token_count`) plus
sanitised provider extensions. Cached and reasoning counts are promoted only
when they parse as non-negative integer token counts; absent or malformed values
stay absent rather than becoming `0`. Local Codex 0.153.4 metadata also shows tool telemetry such as
`codex.tool_decision`, `codex.tool_result`, `codex.sandbox_outcome`,
`tool_name`, `tool_namespace`, `call_id`, `duration_ms`, `success`,
`mcp_server`, and `mcp_server_origin`. A `codex.tool_result` now becomes a
first-class tool-call signal: event attributes expose `operation_id`,
`category`, `outcome`, and observed `duration_ms`, while
`provider_extensions.tool_call` preserves the provider-reported tool name,
namespace, call ID, status, sequence, truncation flag, and observed provenance.
`codex.tool_decision` becomes an approval/permission decision signal: event
attributes expose `approval_id`, `approval_decision`, optional
`approval_reason_class`, and safe provider-reported tool identity, while
`provider_extensions.tool_decision` preserves the provider-specific decision
fields. `approvals` is removed from `attributes.unavailable_fields` only for
`codex.tool_decision`. `codex.sandbox_outcome` becomes a command-execution signal with the same public
timeline operation fields; safe provider details are kept under
`provider_extensions.sandbox_outcome`, using `initial_duration_ms` as the
operation duration when present. Operation IDs include the session identity plus
provider call ID when present, so replay deduplication cannot collapse reused
call IDs from different sessions. `tool_calls` is removed from
`attributes.unavailable_fields` only for `codex.tool_result`;
`command_execution` is removed only for `codex.sandbox_outcome`. Codex
`codex.conversation_starts` is normalised to canonical `session.active` and
stamps `lifecycle_kind=session_start`, while `codex.startup_phase` and
`codex.websocket_connect` retain safe lifecycle/governance evidence such as
phase, status, duration, entrypoint, auth mode, approval policy, sandbox policy,
and terminal type. These lifecycle-backed events remove `session_lifecycle` from
`attributes.unavailable_fields`; session end remains unknown because no reviewed
Codex 0.153.4 fixture proves a completion signal. Other Codex log events keep
those fields unavailable.

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
hiding). SQLite session reconstruction carries that identity forward under
`provider_extensions.correlation` with `session_id_source=conversation.id` and
keeps the raw provider session ID separate from the stable `codex:` prefix.
Reconstructed conversation-backed rows are labelled
`identity_scope=provider`, `identity_source=conversation.id`. Content-derived
`codex-log:*`, token, and skill rows are labelled `identity_scope=observation`
and remain available in the observation/all list scopes. The normaliser and
storage layer do not use time, model, or intake order to attach those rows to a
conversation because the reviewed metric surface carries no join key.

Observed Codex resource metadata is also promoted into reconstructed sessions
from both log `provider_extensions.resource_attributes` and metric
`provider_extensions.resource`: `service.name` and `service.version` are mirrored
under session `provider_extensions.resource_attributes`, while session attributes expose
`service_name`, `service_version`, and the normalized entrypoint
(`codex_cli_rs` -> `interactive`, `codex_exec` -> `codex exec`) for the sessions
evidence browser. Only log-derived `codex:<conversation.id>` sessions get
`session_id_source=conversation.id`; content-derived metric/trace IDs do not.
Prompt/response/source-code content is not captured by
default; its configurable capture is tracked in #94.

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
- **Tool-call operations** (`supported` for `codex.tool_result`) and **command-execution operations** (`supported` for `codex.sandbox_outcome`) are extracted into `canonical.Operation` by `ExtractLogOperations`, persisted by the live `/v1/logs` path, and exposed in operation stats on the Insights page. Timeline events still carry operation ID, category, outcome, and duration for the evidence browser. Operation ordering uses observed log timestamps when present, falling back to receive time only when absent. Known observed Codex tool names map conservatively (`exec_command` -> shell command, `apply_patch` -> filesystem write); sandbox outcomes are categorised as shell commands. Provider-emitted command, argument, and output fields are retained verbatim as local raw tool evidence; credentials, account identifiers, prompt/response fields, and diagnostics remain excluded. Unknown tool-result names stay `unknown`.
- **Pull-request / merge-request URLs** (`supported` for 0.155.1 `codex.tool_result`) are recognised only when a provider-emitted field contains an exact HTTP(S) URL with a GitHub/GitLab/Bitbucket/Azure DevOps-compatible pull or merge-request path. The raw URL and source field remain in provider extensions; one distinct session candidate becomes `attributes.pr_link`, while conflicts are `partial`. The normaliser never derives a link from repository metadata or calls the host.
- **Approval/permission decisions** (`supported` for `codex.tool_decision`) are retained as event-level approval signals rather than `canonical.Operation` records, because they describe permission decisions before/around a tool call, not execution itself. Missing decisions are labelled `unknown`; approved/denied variants retain local raw command/argument/output evidence when Codex emits it, but exclude credentials, account identifiers, emails, prompt/response fields, and slug values.
- **MCP-backed tool results** (`partial` for MCP inventory) are represented only when Codex reports a non-empty `mcp_server`; the provider-reported raw server name is retained under `provider_extensions.mcp_call` and is itself the correlation identity.
- **Session lifecycle/governance** (`partial`) maps `codex.conversation_starts`
  to canonical `session.active` and keeps safe startup/websocket governance
  metadata on the timeline. Session end is still `unknown` pending fixture-backed
  evidence.
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
