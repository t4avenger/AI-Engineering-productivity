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
`mcp_server`, and `mcp_server_origin`. When a `codex.tool_result` carries a
non-empty `mcp_server`, the event normaliser stores an explicit MCP-use signal
under `provider_extensions.mcp_call` with the provider-reported `server_name`,
an installation-specific `server_fingerprint`, and safe invocation metadata;
the raw server name is excluded from generic log attributes to avoid duplicate
evidence but is retained in the MCP-specific record for display. Empty `mcp_server`
means the provider did not report that tool result as an MCP server call, so it
remains an internal Codex/tool invocation rather than MCP inventory evidence.
Since the observed logs lack trace correlation and a safe retained session
identifier, each retained log forms an explicitly `unknown` lifecycle session
identified by an installation-specific HMAC fingerprint. Account, hostname,
email, conversation ID, raw command arguments, tool output, and body fields are
removed by the privacy pipeline before storage.

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
- **MCP-backed tool results** (`partial` for events) are represented only when Codex reports a non-empty `mcp_server`; the provider-reported server name and fingerprint are retained under `provider_extensions.mcp_call` with safe invocation fields.
- **Session/request identity** derives from the installation HMAC fingerprint,
  because `conversation.id` is stripped by the privacy pipeline.
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

## Skill injection metrics

`NormalizeMetrics` maps OTLP `resourceMetrics` from Codex (`codex_cli_rs` /
`codex_exec`) for two reviewed skill surfaces. `codex.skill.injected` datapoints
with a non-empty `skill` attribute become explicit named skill events with
`provider_extensions.skill_detection = "explicit"` and
`provider_extensions.skill.{name,outcome,invoke_type}`. A live Codex CLI 0.153.4
trigger-style `bmad:brainstorm` probe emitted `codex.skill.turn.duration_seconds` as a histogram
with `status` and `plugin_id=unattributed`, but no skill name; those datapoints
become inferred coverage events with `provider_extensions.skill_detection =
"inferred"` and no `provider_extensions.skill` record. Other metrics are
ignored after HTTP accept so exporters can flush without inventing insight rows.

Golden: `fixtures/codex/expected/codex-0.153.4-skill-injected-metrics.events.json`
for `fixtures/codex/observed-sanitised/codex-0.153.4-skill-injected-metrics.json`.

The static-analysis gate enforces cyclomatic complexity of 15 or lower for
each Go function. The normaliser separates fixture, resource, scope, and span
handling so that nesting does not conceal excessive decision complexity.
