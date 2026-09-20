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
carries model and token identity and promotes observed request-level tokens to
`model`, `input_token_count`, `output_token_count`, and
`cached_input_token_count` attributes for session APIs and cost calculation; the
`mcp_server_connection` event carries neither
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

### Tool-decision approval signals

The `tool_decision` event is the only Claude signal that reports a permission
**grant or denial**, together with its source. Reject decisions appear *only*
here — the `tool_result` event's `decision_type` is always `accept` — so without
this event a denied tool call is invisible. `attachToolDecision`
(`internal/normalize/claude/normalizer.go`) stamps, on `tool_decision` events only:

- `attributes.approval_id` = `claude-code:<session_id>:approval:<tool_use_id>`. This
  shares the `tool_use_id` key with the E5 operation id
  (`…:tool:<tool_use_id>`), so a decision correlates to its result.
- `attributes.approval_decision` = the wire `decision` normalised onto the
  cross-provider `approved`/`denied` vocabulary (matching Codex):
  `accept`→`approved`, `reject`→`denied`. A missing `decision` is reported as
  `unknown` and is **never** inferred from any other signal; an unrecognised value
  passes through verbatim.
- `attributes.approval_reason_class` = the wire `source`
  (`config` / `hook` / `user_permanent` / `user_temporary` / `user_abort` /
  `user_reject`).
- `attributes.tool_name`, `attributes.tool_source` (`builtin` / `mcp`).
- `provider_extensions.tool_decision` carries `source`, `tool_name`, `tool_source`,
  and `tool_use_id` verbatim, plus `provenance: "observed"`. Note the two decision
  keys: `decision` holds the **normalised** `approved`/`denied` value (identical to
  `attributes.approval_decision`), while `raw_decision` holds the **untouched** wire
  value (`accept`/`reject`) — so a consumer can read either the canonical or the
  original decision, and nothing is hidden (epic #87). These fields are excluded
  from `provider_extensions.event` so they are not double-echoed.

`unavailable_fields` on a `tool_decision` event drops `approvals` (the decision is
the authoritative approval signal) while keeping `tool_calls` unavailable — a
decision references a tool but is not evidence of an executed call. Every other
event lists `approvals` as unavailable, so the capability is explicit per event.

The gated `tool_parameters` attribute — which under `OTEL_LOG_TOOL_DETAILS=1`
carries the full command and MCP server/tool names — is dropped at the wire
boundary by `NormalizeLogs` (`logs.go` `droppedKeys`) and never reaches
`provider_extensions`; the reviewed-wrapper fixture omits it entirely. Extracting
the MCP server/tool identity from that gated blob is deferred to a follow-up
(the reject/`mcp` decision here classifies only `tool_source: mcp`).

### Prompt & response content path

The four content-bearing `/v1/logs` events — `user_prompt`, `assistant_response`,
`api_request_body`, `api_response_body` (#94) — are the highest-signal,
highest-sensitivity Claude surface. They carry no typed content record:
`ModelInteraction`/`Operation` hold only numeric primitives (`sampleOperation`
even strips content), so — exactly like `api_request`'s token numerics — every
non-structural field, **content included**, rides verbatim under
`provider_extensions.event` via `normalize.UnknownFields`. Nothing about content
is promoted, hidden, or re-redacted at ingest (epic #87 — capture raw, defer the
per-field visibility policy):

- `user_prompt` → `prompt_length`, `command_name`, `command_source`, and (behind
  `OTEL_LOG_USER_PROMPTS=1`) the raw `prompt`. When the gate is off the `prompt`
  key is simply **absent** — `prompt_length` is still reported and the prompt is
  never fabricated or zero-filled.
- `assistant_response` → `response_length`, `model`, `query_source`, the namespaced
  `request_id`, and (behind `OTEL_LOG_ASSISTANT_RESPONSES=1`) the raw `response`.
  By default the provider emits the literal string `"<REDACTED>"`; it is captured
  **as received**, not dropped and not re-redacted.
- `api_request_body` / `api_response_body` → `model`, `body_length`,
  `body_truncated`, and (behind `OTEL_LOG_RAW_API_BODIES=1`) either the inline raw
  `body` or, in `file:<dir>` mode, a `body_ref` pointer — both echoed verbatim.

`unavailable_fields` is precise per event so an absent signal stays explicit:
`prompt_content` is available (removed) on `user_prompt` and `api_request_body`
(the request body is the conversation/prompts); `response_content` and `model` are
available on `assistant_response` and `api_response_body` (the response body is the
model output). No content key is added to `gatedEventFields()`, and none is in
`logs.go` `droppedKeys`, so content survives both the reviewed-fixture and live-wire
paths. The bare `prompt.id`/`message.uuid` correlation identifiers **are** dropped
at the wire boundary — mapping those into canonical correlation is #106 (X19)'s job,
not E7.

To let synthetic content-present fixtures be committed, the shared fixture
validator (`internal/fixture/validator.go`) no longer prohibits the field names
`prompt`/`prompts`/`response`/`responses`. Its value-based `likelySecret` scan
(credential regexes + length/entropy heuristic) is untouched and still runs on
every string, so no real credential can ride under a prompt/response key; the
credential/path/command field-name blocks (`password`, `token`, `command*`,
`file_path*`, …) also remain.

## Provider-completion outcome contract — `attachOutcomeContract`

Three log events carry a provider-completion outcome, stamped verbatim under
`provider_extensions.outcome_contract` (`source: provider_completion`,
`confidence: observed`) with the observed `model`/`duration_ms`/token counts and,
when present, `error_code` and `retry_attempt`:

- `api_request` → `status: success`
- `api_error` → `status: failed`
- `api_refusal` → `status: refused` (E8, #95)

A **refusal** is the Messages API `stop_reason: "refusal"` — a distinct outcome
that is neither success nor error and arrives as HTTP `200 OK`, so error-rate or
5xx monitoring never sees it. `api_refusal` additionally carries, verbatim (epic
#87 — raw capture, no ingest redaction):

- `server_fallback_hop` (bool) — `true` when the server-side model fallback
  silently retried this refusal on another model (**the user never saw it — an
  intermediate hop**); `false` when the request actually ended in a user-visible
  refusal. A single turn can emit a `true` hop event **and** a later `false` final
  event, so any count of *user-visible* refusals must filter on
  `server_fallback_hop == false` rather than counting every `api_refusal`.
- `has_category` / `has_explanation` (bools, always emitted) plus, only under
  `OTEL_LOG_TOOL_DETAILS=1`, the `category` (`cyber` | `bio` | `frontier_llm` |
  `reasoning_extraction`) and `explanation` strings from the API `stop_details`.
  The has_* booleans keep an absent category explicit rather than silently missing;
  `has_explanation`/`explanation` are absent on a `server_fallback_hop` event.

Like `api_request`/`api_error`, `api_refusal` produces a `canonical.Event` with the
outcome contract but **no** `ModelInteraction` record (only `api_request` does —
see below); the refusal outcome lives at the event level.

## Session-state & governance — `attachGovernanceContext`

Three lifecycle events frame *how* a session is allowed to act rather than what a
model returned. They carry no model/token/tool/approval signal (so those stay
explicitly unavailable, as for any pure lifecycle event), but they are governance
signals we must persist rather than drop. `attachGovernanceContext` — called from
`normaliseSampleEvent` alongside `attachOutcomeContract`, and **gated on the event
name** rather than sniffing fields — stamps a stable, queryable contract under
`provider_extensions.governance` with a `kind` equal to the event type (E9, #96):

- `permission_mode_changed` → `from_mode`/`to_mode`/`trigger` plus a
  `bypass_permissions` bool. Modes are `default`/`plan`/`acceptEdits`/`auto`/
  `bypassPermissions`; triggers are `shift_tab`/`exit_plan_mode`/`auto_gate_denied`/
  `auto_opt_in`. The event fires only when `from != to`. A transition **into**
  `bypassPermissions` is the governance red flag — `bypass_permissions` is `true`
  only for that case — because every subsequent tool decision is framed by the mode
  in force.
- `auth` → `action` (login/logout), `success` (bool), `auth_method`, and, only on a
  failure, `error_category`/`status_code`. `status_code` is coerced to an integer
  consistently across the sample-JSON and OTLP (int/double/string) paths so golden
  and parity don't diverge on type; a successful `auth` carries no `status_code`.
- `plugin_loaded` → `plugin_scope`, `enabled_via`, the CLI's **pre-hashed**
  `plugin_id_hash`, `has_hooks`/`has_mcp`/`host_owned_mcp`, `skill_path_count`/
  `command_path_count`/`agent_path_count`, and `safe_mode`. The wire spells the
  plugin fields both dotted (`plugin.name`) and underscore (`plugin_name`); the
  helper accepts both so the sample and `/v1/logs` paths agree.

No credential- or account-adjacent field is promoted (the wire boundary already
drops `user.email`/`user.account_id`), and `promotedEventFields` lists these keys —
in both dotted and underscore spellings — so they don't double-echo under
`provider_extensions.event`. Building alerting/rules on the contract is downstream
(`internal/governance`) and out of scope here.

## Model-interaction records — `ExtractModelInteractions`

Only `api_request` events become a `canonical.ModelInteraction`. Executed tool
calls are carried by `canonical.Operation` via `ExtractOperations` (below), not
this record. An `mcp_server_connection` is a connection lifecycle event, not a
tool invocation, so no `Operation` is fabricated from it.

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

## Operation records — `ExtractOperations`

Only `tool_result` events become a `canonical.Operation` — the first real
evidence of an executed tool call (capability matrix "Tool calls (generic)",
#92). Every other event is skipped, never turned into a fabricated operation.

- **Outcome** ← `success`, which arrives as the string `"true"`/`"false"` on the
  wire (a bool is tolerated); an absent or unrecognised value stays `"unknown"`,
  never a fabricated `success`/`failed`.
- **Category** (`OperationCategory`, §10.5) is classified from `tool_name`
  without fabricating what it cannot prove: `mcp__…` prefix (or a
  `mcp_server_scope`) → `MCP call`; `Bash` → shell command; `Read`/`Glob`/`Grep`
  → filesystem read; `Write`/`Edit` → filesystem write; `WebFetch`/`WebSearch` →
  network request; anything else stays `unknown` (still promoted as a generic
  tool call). MCP-call correlation detail is owned by #104.
- **OperationID** prefers the provider `tool_use_id`, then the integral
  `event_sequence`, then a content hash, so operations never silently collide.
- **ProviderExtensions** preserve every raw field verbatim (`tool_name`,
  `tool_use_id`, `success`, `duration_ms`, `error_type`,
  `decision_type`/`decision_source`, the size counters, `mcp_server_scope`)
  under `event` — including the fields that also derive the typed
  Category/Outcome, since those are derived views, not replacements. An absent
  `duration_ms` is omitted, never coerced to `0`.

Tool input parameters and result bodies (gated content) are out of scope —
owned by the JSONL issues. Operations are ordered and deduplicated by
`CorrelateOperations`. Live `/v1/logs` ingest uses `ExtractLogOperations` to reduce OTLP wire records through the same reviewed sample-event mapper, persist the resulting `canonical.Operation` records, and feed operation stats on the Insights page without changing `NormalizeLogs`' event-only contract.

## Metrics path — `NormalizeMetrics`

Issue #89 routes Claude Code's OTLP **metrics** (`POST /v1/metrics`) into the
adapter, mirroring the dual-adapter routing already used for `/v1/logs`.
`persistMetrics` (`internal/api/otlp.go`) invokes `codex.NormalizeMetrics` then
`claude.NormalizeMetrics` and persists the union; each adapter normalises only
the resources whose `service.name` it recognises (Claude Code: `claude-code`)
and returns `ErrUnsupportedMetrics` for a payload with none of its own, so a
mixed-tool batch is safe. A non-sentinel error from either adapter aborts the
whole batch — a malformed resource never lets half a mixed batch persist.

The capture shows `claude_code.token.usage` is a monotonic **sum** whose
datapoints encode the count as `asDouble`, carry the token category in a
camelCase `type` attribute (`input`/`output`/`cacheRead`/`cacheCreation`), and
stamp `session.id`, `model`, and `query_source` per datapoint
(`fixtures/claude/observed-sanitised/claude-code-2.1.268-token-usage-metrics.json`,
tool 2.1.268). Each datapoint becomes one `canonical.Event`
(`event_type = claude_code.token.usage`, `provider = anthropic`,
`tool = claude-code`) whose `session_id` is the raw `claude-code:<session.id>`
(epic #87 — no hiding) shared across the session's token categories. The token
category maps onto the canonical keys shared with the Codex adapter:
`input → input_token_count`, `output → output_token_count`,
`cacheRead → cached_input_token_count`,
`cacheCreation → cache_write_input_token_count`. A genuine `0` is kept (never a
fabricated absence); an unrecognised `type` is skipped so a future Claude token
category does not fail the batch, while a recognised type with an unparseable
value is a hard normalisation error rather than a silently dropped 202.

`claude_code.cost.usage` is also mapped end-to-end (#97, M10). It is the
provider's own reported spend, decoded from the sum/gauge datapoint's numeric
value (`asDouble`, or a numeric-string `asInt`) into a canonical event
(`event_type = claude_code.cost.usage`) carrying the USD amount under
`attributes.provider_cost`. Cost is **secondary/descriptive** (`PRODUCT_MAP.md`
§0): the amount is preserved verbatim as evidence but is **never** fed into
`cost.Calculate` — `internal/cost/cost.go` explicitly early-returns
`claude_code.cost.usage` (alongside the `*.token.usage` metric families) so a
provider-reported figure never double-counts against request-level pricing. A
cost datapoint whose value is absent, non-numeric, negative, or non-finite
(NaN/Inf) is dropped to nil by `optionalCostUSD` and, because a recognised
cost.usage instrument is expected to carry a value, treated as a hard
normalisation error rather than a silent 202 (same routing contract as
token.usage). When a resource emits cost.usage, its sibling `token.usage` events
stop declaring `provider_cost` unavailable — the correlated cost event carries
it; a token-only resource keeps `provider_cost` in `unavailable_fields`.

The three **code-output** counters are mapped end-to-end (#98, M11): they are the
direct outcome signals — did the session actually produce code, commits, or PRs —
that complete the spend-vs-output view alongside token/cost.
`claude_code.lines_of_code.count` carries a `type` dimension (`added`/`removed`)
that selects the canonical key exactly as a token type does
(`added → lines_added_count`, `removed → lines_removed_count`) and promotes
`model`; `claude_code.commit.count` and `claude_code.pull_request.count` carry
standard attrs only, so their counts land under `attributes.commit_count` /
`pull_request_count` with no `model`. All three share the count-parsing discipline
of `optionalCount` (a thin alias over `OptionalTokenCount`): a genuine `0` is
kept, and an absent/non-integer/negative value is a hard normalisation error per
the routing contract (never a silent 202) — while an unrecognised `lines_of_code`
`type` is skipped so a future category does not fail the batch. The
`lines_of_code` attribution dims (`skill.name`, `agent.name`, …) survive into
`provider_extensions.metric_attributes`, so produced code is attributable to the
skill/sub-agent that produced it. The surface is exercised by the synthetic
fixture `fixtures/claude/observed-sanitised/claude-code-2.1.268-code-output-metrics.json`
→ `fixtures/claude/expected/claude-code-2.1.268-code-output-metrics.events.json`;
per-file LOC attribution stays out of scope (JSONL diffs, #105).

The three **engagement** metrics complete the documented eight-metric surface
(#99, M12) — the behaviour signals at the centre of the reorientation, not cost.
`claude_code.code_edit_tool.decision` (an accept/reject on an Edit/Write/
NotebookEdit) and `claude_code.session.count` land as a single canonical count
each (`attributes.edit_decision_count` / `session_count`) with their categoricals
kept as **surviving dimensions** in `provider_extensions.metric_attributes`
(`decision`, `tool_name`, `source`, `language` / `start_type`, all allow-listed).
This differs deliberately from `lines_of_code`: those categoricals *partition a
single measure* rather than naming distinct measures, so the edit-acceptance rate
is a group-by `decision`, and an unforeseen decision/start_type value is never
dropped. `claude_code.active_time.total` is a **duration**, so it takes a float
path (`optionalSeconds`, mirroring `optionalCostUSD`): a fractional or
int-encoded second is kept, only a negative/unparseable value is the hard error,
so a valid exporter double is not dropped. Its `type` (`user`/`cli`) is *promoted*
onto `attributes.activity_type` rather than allow-listed — token
(`type=input/output`) and lines (`type=added/removed`) datapoints already carry
`type` on the wire, so allow-listing it would surface a duplicate in their
`metric_attributes` — and is folded into the event-ID identity so the user/cli
datapoints of one session/timestamp stay distinct. The surface is exercised by the
synthetic fixture
`fixtures/claude/observed-sanitised/claude-code-2.1.268-engagement-metrics.json`
→ `fixtures/claude/expected/claude-code-2.1.268-engagement-metrics.events.json`.
No documented Claude Code metric now routes-tolerated unmapped; an undocumented
future metric still route-tolerates (yields no event).

Because #88 removed storage-side sanitising, the adapter is the sole guard for
metric attributes: it carries only an **allow-list** of safe keys into
`provider_extensions.metric_attributes`/`resource`, dropping operator/identity
and any unforeseen attribute (`user.*`, `organization.*`, `terminal.*`,
secrets, paths) by default. The allow-list carries `query_source`, `host.arch`,
`os.type` and — the M10 attribution dimensions that answer the epic's central
efficiency question, which skill / MCP tool / sub-agent / plugin burned the
tokens and cost — `skill.name`, `mcp_server.name`, `mcp_tool.name`,
`agent.name`, `plugin.name`, `marketplace.name`, `speed`, and `effort` (each
allow-listed in both its dotted wire form and the underscore variant so the same
key survives whichever an exporter build emits; the match lower-cases and
trims), plus the M12 engagement dims `decision`, `tool_name`, `source`,
`start_type`, and `language`. These dims are pre-redacted behaviour metadata, not
identities. Event
IDs are a content hash over the metric name, the per-metric count key (token type
for token.usage, `lines_added`/`removed` for lines_of_code), model, timestamp,
resource/scope identity, datapoint index, and value, so same-timestamp datapoints
in one session stay distinct under `CorrelateEvents`.
The attributed token.usage + cost.usage surface is exercised by the synthetic
fixture `fixtures/claude/observed-sanitised/claude-code-2.1.268-cost-attribution-metrics.json`
→ `fixtures/claude/expected/claude-code-2.1.268-cost-attribution-metrics.events.json`;
live OTLP metrics carrying attribution dims were not reachable in the capture
environment, so the fixture is shaped from the documented cost.usage surface
(<https://code.claude.com/docs/en/monitoring-usage>) and marked
`fixture_origin: synthetic`.

## Traces path — `NormalizeTraces`

Issue #90 (F3) routes Claude Code's OTLP **traces** (`POST /v1/traces`) into the
adapter, replacing the previous 501 that bug #50 filed. `persistTraces`
(`internal/api/otlp.go`) invokes `claude.NormalizeTraces`, which normalises only
resources whose `service.name` is `claude-code` and returns `ErrUnsupportedTraces`
for a payload with none of its own, so a mixed-tool batch is safe. A structurally
malformed claude-code span (missing `traceId`/`spanId`) is a hard normalisation
error — not the sentinel — so the route never silently 202-accepts and drops
supported Claude trace data (#50/#49). Codex has no traces adapter yet (#112);
the ingest path keeps the multi-adapter shape so one can slot in.

The enhanced-telemetry beta exports the span tree that logs do not carry:
`claude_code.interaction` (root) → `claude_code.llm_request` (child), confirmed by
a live capture (tool 2.1.268,
`fixtures/claude/observed-sanitised/claude-code-2.1.268-trace-spans-otlp.json`).
Each span becomes one `canonical.Event`. The raw `session.id` is promoted to the
canonical `session_id` (`claude-code:<session.id>`); when a span carries no
`session.id` the identity falls back to the trace id (`claude-code:trace:<traceId>`)
so spans from different traces are not merged into one synthetic session. The raw
span identity (`traceId`/`spanId`/`parentSpanId`), span `name`, `kind`, start/end
nanos, status, and scope are retained verbatim under
`provider_extensions.span`/`correlation` (epic #87); a root span's absent parent
is kept as `null`, not an empty string, so a genuine root is distinguishable from
an empty parent. The event ID is a content hash over `traceId`+`spanId`+resource
identity, so two resources cannot collide on one event ID and be silently dropped
by `CorrelateEvents`. A supported claude-code span missing a required structural
field (`traceId`, `spanId`, `name`, or a positive `startTimeUnixNano`) is a hard
normalisation error, never a silently dropped or schema-invalid event.

Issue #100 (T13) maps the two **per-prompt** span types into typed, present-only
field blocks on `attributes`: `attributes.interaction` (`sequence`, `duration_ms`,
`user_prompt_length` — a length, never the prompt text — `parent_source`,
`queued_sends`) and `attributes.llm_request` (`model`, `gen_ai.*`, `context` from
`llm_request.context`, latency `duration_ms`/`ttft_ms`/`first_content_ms`, the four
token counts, `attempt`/`success`/`status_code`, `stop_reason`,
`finish_reasons`, `response_has_tool_call`, and sub-agent workflow correlation
`agent_id`/`parent_agent_id`/`workflow_run_id`/`workflow_name`). Every field is
present-only — a genuinely absent attribute is omitted, never fabricated.
`gen_ai.response.finish_reasons` is a Claude Code OTLP **arrayValue** the scalar
attribute decoder cannot read (so F3 silently dropped it); a sibling
`arrayAttributeValues` decoder (`logs.go`) now surfaces it in the `llm_request`
block. The interaction span is the per-user-prompt root — a genuine task
boundary — so `provider_extensions.correlation.task_boundary.confidence` is raised
to `observed` for it; the llm_request child is not itself a boundary and stays
`unknown`. The raw free-text `error` message is captured **raw** (epic #87 — a
governance/timeline product needs the actual failure text; #101 aligned this with
`tool.execution` rather than dropping it "for consistency" with the pre-#87
default), alongside the bounded `error_class`, `status_code`, and `success`.
`query_source` is a metrics-only dimension, not a span attribute, so it is never
invented here.

Issue #101 (T14) maps the three **tool** span types into typed, present-only
blocks:

- `attributes.tool` (`span.type = tool`): `tool_name`, `tool_name_safe`,
  `bash_command_class`, `bash_argv0`, the OTEL_LOG_TOOL_DETAILS-gated `file_path`
  and `full_command` captured **raw**, `skill_name`, `subagent_type`,
  `tool_use_id`, `gen_ai.tool.call.id`, `agent_id`/`parent_agent_id`/`workflow.*`,
  `duration_ms`, `result_tokens`.
- `attributes.tool_execution` (`span.type = tool.execution`): `tool_use_id`,
  `gen_ai.tool.call.id`, `success`, `error_class`, the gated free-text `error`
  captured **raw**, `duration_ms`.
- `attributes.tool_blocked_on_user` (`span.type = tool.blocked_on_user`):
  `decision` (`accept`/`reject` — the denial shows only here, never on
  `tool_result`), `source`, `duration_ms`.

Every field is present-only. The raw `file_path`/`full_command`/`error` are the
governance signal (epic #87 — captured verbatim; the per-field hide decision is
deferred downstream), so they are emitted under their provider-native keys and the
governance layer (`internal/governance/risky_access.go`) classifies over the raw
value (`ClassifyPath`/`ClassifyCommandAccess`). Because tool spans now carry
tool/file/command evidence, `spanUnavailableFields` drops `tool_io`,
`file_operations`, and `command_execution` from their `unavailable_fields` — a tool
span never falsely declares those surfaces unavailable. Tool spans are
intra-interaction operations, so their `task_boundary.confidence` is `observed`
with `TaskID` nil (not themselves boundaries); `tool_use_id`/`gen_ai.tool.call.id`
are the join keys for a later cross-signal correlation with `tool_result` /
`tool_decision` logs (#92/#93). Hook span-type mapping remains owned by #103.

Issue #102 (T15) reconstructs the **sub-agent tree** from those span attributes.
The per-span sub-agent correlation `agent_id`/`parent_agent_id`/`subagent_type`/
`workflow.*` that #100/#101 already capture rides only on `claude_code.llm_request`
and `claude_code.tool` spans (never on `interaction`/`tool.execution`); `agent_id`
is absent on the main session, and `parent_agent_id` is absent for a sub-agent
spawned directly by the main session (requires v2.1.268).
`claude.ReconstructSubAgentRelations(events []canonical.Event)` is a pure function
over a session's canonical span events that produces one
`canonical.AgentRelation` (schema `0.1.0`) per distinct sub-agent — a span
carrying a non-empty `agent_id` — grouped by `(trace_id, agent_id)` (an `agent_id`
is unique only within a trace, so a session spanning multiple traces never
collides). `RelationID` is namespaced `"<trace_id>:<agent_id>"`. The tree root is
unambiguous via `ParentKind`: `sub_agent` when `parent_agent_id` is present,
`main_session` otherwise. Rollups are nullable (nil when never observed, never a
fabricated zero) and summed present-only from the agent's spans: `SpanCount`/
`LLMRequestCount`/`ToolCount`, the four token counts, and — labelled honestly —
`LLMDurationMsTotal`/`ToolDurationMsTotal` (summed per-span durations, which
overlap) versus `WallClockMs` (the true elapsed `max(end_unix_nano) −
min(start_unix_nano)`), so a summed value is never mistaken for wall-clock. All
present-only metadata takes the value from the earliest span (by
`provider_extensions.correlation.ordering_key`) for determinism, and the output is
sorted by `(trace_id, agent_id)`.

The relation is a **derived** record, not a payload-scoped extraction. The SQLite
repository re-derives it inside `rebuildSession` from the session's full persisted
event set and **REPLACE**s the `agent_relations` rows (DELETE then INSERT), so a
rollup is complete and idempotent no matter how a trace's spans were split across
OTLP batches — an `INSERT OR IGNORE` keyed on a first partial row would undercount.
Only `claude-code` sessions carry sub-agent spans, but every tool's rebuild clears
any stale rows. `ListAgentRelations(storage.AgentRelationFilter{SessionID})` reads
them back; HTTP/UI exposure of the tree is downstream (#157/#159). See
`fixtures/claude/observed-sanitised/claude-code-2.1.268-subagent-spans-otlp.json` →
`fixtures/claude/expected/claude-code-2.1.268-subagent-spans.relations.json` (tool
2.1.268; `fixture_origin: synthetic` — the non-interactive sub-agent tool execution
could not be captured live in this environment).

As with metrics, #88 removed storage-side sanitising, so the adapter is the sole
guard: span attributes are also reduced to an **allow-list**
(`safeSpanAttributeKeys`, e.g. `span.type`, `gen_ai.*`, `stop_reason`, token
counts, `duration_ms`, `error_class`, `tool_name`, `tool_use_id`, `result_tokens`,
`decision`/`source`, `agent_id`/`workflow.*`), dropping operator/identity and any
unforeseen or secret-bearing attribute (`user.*`, `session.id`, `authorization`,
the redacted `user_prompt`) by default. The raw `file_path`/`full_command`/`error`
are **not** in the allow-list — they live only in their typed block (their
canonical home, which governance walks), so they are never duplicated into
`provider_extensions.span_attributes`.

## Privacy

`NormalizeEvents` and `ExtractModelInteractions` retain `session_id` and
`request_id` as raw provider-prefixed native correlation keys
(`claude-code:<session_id>`) — epic #87 removed ingest-time hiding, so there is no
HMAC fingerprint and no fallback redaction. The fixture validator runs before
mapping; it no longer prohibits the `prompt`/`response` field names — Claude
prompt/response and raw API body content is now captured raw (#94), deferring the
per-field visibility policy — and #101 likewise removed the `file_path` /
`command` / `filename` field-name blocks so raw paths and command lines are
committable governance evidence. Only credential field NAMES stay blocked
(`password`, `token`, `secret`, `api_key`, `authorization`, `access_token`), and
the value-based secret/entropy scan still runs on every string, so no real
credential can be committed under any key. See "Prompt & response content path"
above.

## Golden fixtures

- `fixtures/claude/expected/claude-code-2.1.251-otlp-events.events.json` — canonical
  events for the committed OTLP-events fixture.
- `fixtures/claude/expected/claude-code-2.1.251-otlp-events.records.json` — canonical
  model-interaction records for the same input.
- `fixtures/claude/expected/claude-code-2.1.268-token-usage-metrics.events.json` —
  canonical token-usage events for the committed `/v1/metrics` fixture.
- `fixtures/claude/expected/claude-code-2.1.268-code-output-metrics.events.json` —
  canonical lines_of_code/commit/pull_request events for the committed code-output
  `/v1/metrics` fixture (#98, M11).
- `fixtures/claude/expected/claude-code-2.1.268-engagement-metrics.events.json` —
  canonical edit-decision/session/active-time events for the committed engagement
  `/v1/metrics` fixture (#99, M12).
- `fixtures/claude/expected/claude-code-2.1.268-trace-spans.events.json` —
  canonical span events for the committed `/v1/traces` fixture, with the typed
  `interaction`/`llm_request` field blocks and decoded `finish_reasons` (#100).
- `fixtures/claude/expected/claude-code-2.1.268-tool-spans.events.json` —
  canonical span events for the tool-span fixture
  (`claude-code-2.1.268-tool-spans-otlp.json`), asserting the typed
  `tool`/`tool_execution`/`tool_blocked_on_user` blocks with raw
  `file_path`/`full_command`/`error` and the span-type-aware `unavailable_fields`
  (#101). The interaction/llm_request tracing pipeline is live-captured; the tool
  spans are a docs-shaped synthetic reproduction (see the fixture's `capture_note`
  and `docs/integrations/claude-fixture-capture.md`).
- `fixtures/claude/expected/claude-code-2.1.269-session-transcript.events.json` —
  canonical `assistant_message` events for the committed session JSONL transcript
  fixture (`claude-code-2.1.269-session-transcript.json`).
- `fixtures/claude/expected/claude-code-2.1.270-user-prompt.events.json` — canonical
  `user_prompt` events (content-present + length-only) for the committed content
  fixture (`claude-code-2.1.270-user-prompt.json`).
- `fixtures/claude/expected/claude-code-2.1.270-assistant-response.events.json` —
  canonical `assistant_response` events (raw response + `"<REDACTED>"` sentinel).
- `fixtures/claude/expected/claude-code-2.1.270-api-bodies.events.json` — canonical
  `api_request_body`/`api_response_body` events (inline `body` + `body_ref`).

Regenerate them with `UPDATE_GOLDEN=1 go test ./internal/normalize/claude/ -run Golden`
after a reviewed change, then inspect the diff.

## Transcript path — `NormalizeTranscript`

The Claude Code session JSONL transcript (`~/.claude/projects/**/<session>.jsonl`)
is the richest Claude data source: newline-delimited JSON, one object per line,
discriminated by a top-level `type`. `NormalizeTranscript(data, receivedAt)` maps
it to canonical events and is served live at `POST /v1/claude/transcript`
(Content-Type `application/x-ndjson` or `application/jsonl`).

- **One record type end-to-end: the `assistant` record.** It is the only line
  carrying the model and full token usage, mirroring the OTLP `api_request`
  slice. Each `assistant` record becomes one `assistant_message` event
  (`source_schema = session_jsonl`). `user`, `system`, and the ~12 auxiliary
  metadata types are decoded only far enough to read `.type` and then skipped —
  the type set is treated as open, so an unrecognised or content-heavy
  out-of-scope record can never fail the import.
- **Correlation.** `session_id` is read from the in-record `sessionId` field (not
  the filename) and mapped through `ProviderNativeSessionID("claude-code:", …)`,
  so transcript events land in the *same* session row as the OTLP
  logs/metrics/traces for that session. `event_id = claude-code:<sessionId>:<uuid>`
  is deterministic, so a `SessionEnd` hook re-shipping a now-complete transcript
  fills in earlier gaps idempotently (`INSERT OR IGNORE`) rather than duplicating.
  The DAG linkage (`uuid`/`parent_uuid`) is carried in `provider_extensions.correlation`.
- **Numeric token capture (in scope).** `message.usage` is decoded via a typed
  struct using `json.Number` (no lossy float coercion) and stamped under the same
  canonical attribute keys the metrics adapter uses: `input_token_count`,
  `output_token_count`, `cached_input_token_count`, `cache_write_input_token_count`,
  and `reasoning_token_count` (from `output_tokens_details.thinking_tokens`). The
  ephemeral cache-window counts (`cache_creation.ephemeral_*`), which have no
  canonical key, are carried under `provider_extensions.cache_usage_extra`.
- **Content deferred, not silently dropped.** Prompt/response text, tool
  `input`/results, and file diffs are never read into an event; they are listed in
  `attributes.unavailable_fields`. Ownership: prompts/responses → E7 (#94);
  MCP calls → J17 (#104); tool IO / diffs / sub-agents → J18 (#105). The
  per-adapter allow-list is the sole guard (epic #88 removed ingest-time hiding):
  only safe scalar envelope fields (`git_branch`, `entrypoint`, `user_type`,
  `request_id`, `effort`, `api_block_index`, `is_sidechain`) reach
  `provider_extensions.transcript` — `cwd` and every content body are excluded.
- **Contract.** An `assistant` record missing a structural field (`uuid`,
  `sessionId`, `timestamp`) — including whitespace-only values — is a hard error
  that aborts the whole import (matching the traces adapter — supported data is
  never silently dropped or half-persisted); invalid JSON lines surface as
  `ErrMalformedTranscript` (HTTP 400); a missing `version` becomes
  `source_version = "unavailable"`, not an error; a transcript with no
  `assistant` records yields zero events and no error.
- **Size cap and inspector.** The route caps the body at 32 MiB (real transcripts
  reach a few MB), larger than the 1 MiB OTLP cap. It deliberately does **not**
  wire the dev ingest inspector: the inspector echoes the raw captured payload, so
  echoing a raw transcript would re-expose exactly the content this path refuses
  to persist.   Accepted/rejected counters are shared with the OTLP routes. Org-admin rollout
  for SaaS fleets is covered in
  [claude-transcript-deployment.md](claude-transcript-deployment.md).

## Out of scope

Sub-agent/sidechain transcripts (sibling `<session>/subagents/agent-*.jsonl`
files) and the transcript content bodies above are out of scope here (owned by the
J-phase and E7). The sample-event fixture shape remains the reviewed golden path
for `NormalizeEvents`.

