# Session API

The Phase 2 local management API provides authenticated session endpoints:

- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{id}`
- `GET /api/v1/sessions/{id}/events`
- `GET /api/v1/sessions/{id}/events/{event_id}`
- `GET /api/v1/sessions/{id}/files`
- `GET /api/v1/sessions/{id}/conversation`
- `GET /api/v1/sessions/{id}/spans`
- `GET /api/v1/sessions/{id}/agents`
- `DELETE /api/v1/sessions/{id}`
- `GET /api/v1/costs/summary`, `GET /api/v1/sessions/{id}/costs`, `GET /api/v1/insights/mcp-inventory`, `GET /api/v1/insights/skill-usage`, `GET /api/v1/insights/model-performance`, `GET /api/v1/insights/context-waste`, `DELETE /api/v1/sessions`

Responses use stable JSON envelopes: list responses contain `data` and
`pagination`; detail responses contain `data`; errors contain `error.code` and
`error.message`. List responses are reverse chronological, with session ID as a
deterministic tie-breaker, and use opaque cursors rather than offset paging.

The list supports `tool`, `model`, `outcome`, `started_after`, `started_before`,
and `scope` filters. `scope=primary` is the default and returns provider-backed
sessions plus legacy/unproven rows while excluding proven observation-only
rows. `scope=observation` returns only content-derived/trace-only observations;
`scope=all` returns both. Dates are RFC3339; `started_after` is inclusive and
`started_before` is exclusive. A `model` filter matches only a retained,
observed event attribute named `model`. Sessions whose model is unavailable do
not match, preserving the distinction between unknown and zero or fabricated
values.

List and detail rows expose `identity_scope` and `identity_source`. These fields
describe the evidence for the row's correlation boundary; they do not imply that
observation rows were merged into a provider session. Direct session detail and
event access remain available for every retained row.

Session list and detail responses include an `availability` object for the
shared cross-tool fields rendered by the dashboard: `provider`, `tool`,
`outcome`, `started_at`, `completed_at`, `model`, `entrypoint`,
`git_branch`, `pr_link`, `tool_version`, `observed_events`, `token_usage`, and
`conversation`. `conversation` is `observed` when the session retains at least one
projected conversation content event (`user_prompt`, `assistant_response`,
`api_request_body`, `api_response_body`, or a Claude transcript `user_message` /
`assistant_message` carrying retained prompt, response, or thinking text) and
`unavailable` otherwise, so a trace-only session — or a transcript session whose
assistant records only carry tool_use — reports `conversation: unavailable`
without any heuristic join.
Values are `observed`, `partial`, `unavailable`, `unsupported`, or `unknown`.
The UI must render non-observed states as labelled cells, never as blanks or
numeric zeroes.

Reconstructed sessions promote observed environment metadata into session-level
evidence when providers emit it:

- Codex log/metric events: `attributes.entrypoint`, `attributes.service_name`,
  and `attributes.service_version`, with raw `service.name`/`service.version`
  mirrored under `provider_extensions.resource_attributes`. `codex_cli_rs` is
  displayed as `interactive`; `codex_exec` is displayed as `codex exec`. When the
  session ID comes from log-backed `conversation.id`, the session provider
  extension records that source and the raw provider session ID with the stable
  `codex:` prefix separated. Content-derived Codex metric or trace session IDs
  must not be labelled as `conversation.id` evidence.
- Claude Code session JSONL transcript events (#91):
  `provider_extensions.transcript.entrypoint` and
  `provider_extensions.transcript.git_branch` promote into
  `attributes.entrypoint` and `attributes.git_branch` (#158). OTLP
  `app.entrypoint` and richer environment identity remain owned by #107.
- `pr_link` is promoted only from one distinct provider-emitted HTTP(S)
  pull/merge-request URL. GitHub, GitLab, Bitbucket, Azure DevOps, and compatible
  self-hosted URL paths are accepted verbatim from reviewed tool evidence; multiple
  distinct candidates produce `partial`, never an arbitrary selected link. Do not
  invent a link from metrics such as `pull_request.count`, branch names, or
  repository metadata, and never fetch or enrich the URL. The URL grammar and
  extraction are shared across providers in `normalize.AttachPRLinkEvidence`.
  Codex sources it from `codex.tool_result` `arguments`/`output`; Claude sources it
  from a tool span's raw `full_command` (e.g. `gh pr view <url>`), so
  `claude_code.pull_request.count` (a metric counter, #98) never stands in for a
  URL (#183). The Claude `tool_decision` `tool_parameters` and session-JSONL tool
  output surfaces can also carry a URL but are currently dropped at ingest; they
  stay `unavailable` for `pr_link` until retained raw by #173 / #105.

Session event timeline entries include optional token fields for retained model
signals: `input_token_count`, `output_token_count`, `cached_input_token_count`,
and `reasoning_token_count`. Missing or malformed provider values remain
absent/null rather than becoming `0`.

Session event timeline entries include optional lifecycle fields for retained
session/governance signals: `lifecycle_kind`, `lifecycle_phase`,
`lifecycle_status`, and `entrypoint`. Additive optional provider-state fields are
`approval_policy`, `sandbox_policy`, `auth_mode`, `integration_kind`,
`integration_name`, and `integration_state`; absence remains null/omitted.

Session event timeline entries include optional operation fields for retained
tool-call signals: `operation_id`, `category`, `outcome`, and `duration_ms`.
`operation_id` is session-scoped when the provider reports a call ID, preventing
cross-session call-ID reuse from collapsing evidence. These fields are populated
only when the provider event proves an executed tool call, such as Codex
`codex.tool_result`; ordinary model/lifecycle events leave them absent/null and
keep `tool_calls` in `unavailable_fields` where appropriate.

`GET /api/v1/sessions/{id}/files` is the additive Files-lane projection (#156 /
T06). It returns cursor-paged `data` rows with `event_id`, nullable
`operation_id` / `path` / `action` / `occurred_at` / `duration_ms`, always-null
`additions`/`deletions` until a per-file fixture exists, plus `availability` and
`source` provenance. Paths come only from retained tool-block `file_path`
evidence (Claude tool spans). Filesystem `action` values (`read`/`write`/
`delete`/`unknown`) come from proven filesystem operation categories or are
inferred from tool names when a path is present; tool names never invent a path.
Aggregate `lines_of_code` counters never fill per-file diffs. Codex currently
proves write category without path; filesystem delete remains unavailable.
Session detail HTML shares the same `insights.SessionFilesFromEvidence` reader.

`GET /api/v1/sessions/{id}/spans` is the additive flat retained-span projection (#157). It returns opaque-cursor-paged trace/span nodes with raw provider identities, nullable parent, original interval/status evidence, provenance, and source-event links. Parent availability distinguishes roots, loaded parents, parents outside the page, and absent retained parents; cycles remain flat. Codex trace-only observations are never joined; Codex traces with the observed resource-level `conversation.id` are projected through that exact provider conversation session. See `docs/architecture/session-spans.md` for the complete record contract.

`GET /api/v1/sessions/{id}/agents` returns the session's stored sub-agent relations (#246) nested into a parent/child tree by `agenttree.Build`. It is deliberately **unpaged**: `data` is the full list of roots, because a row page could split a subtree and orphan its children. Each node carries every raw `canonical.AgentRelation` field unchanged (nullable rollups stay `null`, never `0`; `provider_extensions.spawn` / `span_ids` raw) plus:

- `parent_state`: `main_session_observed` (no `parent_agent_id`, and the observed spawning span carries no `agent_id`), `parent_agent_observed` (`parent_agent_id` names a relation retained in the same trace), `parent_agent_not_retained` (rendered as a root with the raw id), `parent_conflict` (spans report more than one parent, or the spawning span's owner disagrees with `parent_agent_id`), `cycle` (rendered as a flagged root), or `unknown`.
- `parent_candidates`: the raw conflicting or spawning-span parent ids, otherwise `[]`.
- `evidence`: Claude relations expose one `{kind: "span", span_id, event_id, session_id}` per raw span id, with `event_id` resolved only within the same trace. Codex relations expose `{kind: "event", span_id: null, event_id, session_id}` using exact child-session rollout evidence, so the inspector link can cross to that retained child session.
- `children`: nested nodes in stored `(trace_id, agent_id)` order.

Lineage comes only from provider ids and observed span parentage, never from timestamps, proximity, or model names. A session with no relations returns `{"data": []}`; an unknown session returns 404; a store without the relation reader returns 503; a relation or event query failure returns 500.

Codex CLI 0.160.0 relations are owned by the exact root
`codex:<root-thread-id>` session even though child evidence lives in separate
provider sessions. Nodes retain raw child/parent/thread/turn/trace/path fields,
explicit lifecycle outcome, nullable input/output/cached/reasoning tokens,
operation count, summed operation duration, and elapsed task duration. Global
reconstruction is arrival-order independent and replay-idempotent. An unretained
parent is exposed as `parent_agent_not_retained`; absent rollups stay null.

`GET /api/v1/sessions/{id}/conversation` is the additive retained-conversation
projection (#188 / T03). It cursor-pages chronological `data` records keyed by
the source `event_id`, with `event_type`, `occurred_at`, provider/tool/version
provenance, `role` (`user`, `assistant`, or `unknown`), nullable inline `text`,
`content_availability`, and nullable `thinking`. `available`,
`provider_redacted`, `length_only`, `body_reference`, and `unavailable` remain
distinct; a missing body is never replaced with text. `content_availability`
describes `text` only; `thinking` is present-only provider reasoning text (a
string or null, with no separate status). The reviewed Claude Code surfaces are:

- OTLP logs: `user_prompt` and `assistant_response` map to user/assistant roles,
  while `api_request_body` and `api_response_body` remain unknown-role raw
  evidence.
- Session JSONL transcripts (#91, #105, #243): `user_message` projects
  `provider_extensions.transcript.prompt_content` as a user record and
  `assistant_message` projects `transcript.response_content` as an assistant
  record with `transcript.thinking` in `thinking`. A thinking-only assistant
  record has `text: null`, `content_availability: unavailable`, and a
  `thinking` value; a tool_use-only assistant record is model work and is not
  projected (its tool body rides on the Operation).

The API does not parse raw request/response payloads into duplicate messages,
fetch provider data, or join records by time: a prompt retained by both OTLP and
the transcript appears once per source event, each with its own provenance.

`GET /api/v1/sessions/{id}/events/{event_id}` is the additive session-scoped
event inspector (#189 / T08). It returns one retained event (timeline fields plus
raw `attributes` / `provider_extensions`), truncation metadata, and proven-only
`relationships` (files, spans, related events). Missing, deleted, or
cross-session event IDs share structured `404 event_not_found` without leaking
foreign content. Large string values are truncated by default
(`truncation.expand_available`); `?expand=1` returns full retained values.
Relationships never use temporal proximity. Session detail HTML shares
`inspector.Build` and deep-links selection with
`?event=<event_id>&inspector=details|attributes|events`. Session detail HTML
composes the five-lane Session Trace (#159) over these projections; see
`docs/architecture/session-trace.md`.

`GET /api/v1/sessions/{id}/breakdown` is the additive full-session duration
partition (#190 / T09). It returns availability, observed wall window,
exclusive category durations with percentages, overlap/unclassified segments,
coverage, and `calculation_version`. The calculation uses all retained span
evidence and is independent of UI page size. Unavailable results omit a
misleading donut. See `docs/architecture/session-breakdown.md` for the interval
contract.

Timeline entries can also include optional approval fields for reviewed
authorization decisions: `approval_id`, `approval_decision`,
`approval_reason_class`, `tool_name`, and `tool_namespace`. `approval_id` is
session-scoped when the provider reports a call ID. Codex `codex.tool_decision`
currently maps allow/approved-like values to `approved`, deny/block-like values
to `denied`, and missing decisions to explicit `unknown`. These fields must not
retain raw command arguments, prompts, responses, source code, host/user
identifiers, authorization headers, working directories, or file paths; such
values remain absent/null and are represented through `unavailable_fields` where
appropriate.

The MCP inventory insight response contains `data.totals`, `data.servers`, and `data.notes`. Server identities are the raw provider-reported MCP server names (`identity_state: provider_reported`), which are themselves the correlation key — epic #87 removed the HMAC fingerprint. Usage is `observed` only with explicit matching invocation evidence; otherwise it is `not_observed` or `unavailable`. Token context is request-level and labelled as not exact per-MCP allocation.

The operation stats insight response (`GET /api/v1/insights/operations`) reads retained `canonical.Operation` records rather than timeline event attributes. It returns total operations, category counts, outcome counts, duration sample counts, and average duration only where a reviewed provider field reports `duration_ms`. Missing operation durations remain unavailable and are never represented as zero.

`GET /api/v1/insights/integration-states` returns the latest retained evidence
per provider/tool/kind/name/state. `cache_hit`, `cache_load`, `discovered`,
`cache_published`, `refreshed`, `disabled`, and `used` remain distinct; only an
explicit MCP call yields `used`.

`GET /api/v1/insights/governance-states` returns retained provider governance
observations per provider/tool/session/kind/value/phase. Approval policy,
sandbox policy, authentication mode, and authentication-recovery status retain
their exact observed provider values and source-event provenance. Different
values remain separate change evidence; repeated identical evidence keeps the
latest observation. These observations do not imply local enforcement.

The daemon opens the existing local SQLite repository at the platform
configuration directory and reuses its installation-specific privacy salt.
The API never logs raw intake payloads. Session endpoints and Codex rollout intake require a bearer token; the auth-token CLI command deliberately prints the protected local token for dashboard setup. Health and OTLP intake remain unauthenticated for exporter compatibility, and the daemon remains loopback-only by default.

Live OTLP persistence accepts `POST /v1/logs` (provider log events and reviewed Codex/Claude Code operation records), a
`POST /v1/metrics` path that persists reviewed Codex `codex.skill.injected` and
integration-state metrics (plugin cache, MCP discovery/cache publication, app refresh),
Claude Code `claude_code.token.usage`, and Cursor Enterprise
`cursor.token.usage` datapoints as canonical events, and a
`POST /v1/traces` path that accepts JSON and protobuf and persists Claude Code's
enhanced-telemetry beta span tree plus the observed Codex CLI 0.154.0 trace
surface as canonical span events. Codex spans without an explicit provider
session join use `codex:trace:<traceId>` observation identities. Cursor
Enterprise `cursor.api.request` logs are also persisted via
`/v1/logs`. Other metrics and spans are accepted with HTTP 202 so exporters
flush, but are not turned into insight rows. The Codex CLI 0.153.4 no-trace
evidence remains historical; 0.154.0 support is fixture-backed (#172).

`POST /v1/claude/transcript` accepts Claude Code session JSONL (`application/x-ndjson`
or `application/jsonl`, 32 MiB cap) and persists normalised events correlated to
the same provider-native session id as OTLP: `assistant_message` (with raw
thinking and response text), `user_message` (raw prompt text), and content-free
`tool_call` / `mcp_call` correlation events, plus one Operation per tool call
carrying the raw tool input, tool_result body, and record-scoped `toolUseResult`
(raw capture, epic #87 / #94 / #105 / #173). A pull/merge-request URL in that tool
I/O is recorded as `pr_link_candidates` on the correlation event (#251). Only the
raw request body itself is never echoed (no dev ingest inspector on this route).

`POST /v1/codex/rollout` accepts authenticated Codex rollout JSONL with the same
media types and 32 MiB cap. It persists every record under
`provider_extensions.rollout`, correlates through the exact 0.157.1-observed
`session_meta.payload.id`, and projects observed user/assistant message roles
through the existing conversation response. CLI 0.159.2 completed command,
file-change, and MCP items also persist atomically as `canonical.Operation`
records and content-free correlation events, feeding the existing Operations,
Files, MCP inventory, governance, event-detail, and Session Trace reads. Older
custom tool call/output pairs are joined only by exact `call_id`. Replay is idempotent. The route is
excluded from development intake inspection and error responses never include
rollout values. `telemetryiq import-codex-rollout --file <path>` is the supported
loopback-only one-shot importer.
