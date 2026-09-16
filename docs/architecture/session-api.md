# Session API

The Phase 2 local management API provides authenticated session endpoints:

- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{id}`
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
`tool_version`, `observed_events`, and `token_usage`. Values are `observed`,
`partial`, `unavailable`, `unsupported`, or `unknown`. The UI must render
non-observed states as labelled cells, never as blanks or numeric zeroes.

Reconstructed Codex sessions promote observed resource metadata from retained
canonical log or metric events into session-level evidence:
`attributes.entrypoint`, `attributes.service_name`, and
`attributes.service_version`, with raw `service.name`/`service.version` mirrored
under `provider_extensions.resource_attributes`. `codex_cli_rs` is displayed as
`interactive`; `codex_exec` is displayed as `codex exec`. When the session ID
comes from log-backed `conversation.id`, the session provider extension records
that source and the raw provider session ID with the stable `codex:` prefix
separated. Content-derived Codex metric or trace session IDs must not be labelled
as `conversation.id` evidence.

Session event timeline entries include optional token fields for retained model
signals: `input_token_count`, `output_token_count`, `cached_input_token_count`,
and `reasoning_token_count`. Missing or malformed provider values remain
absent/null rather than becoming `0`.

Session event timeline entries include optional lifecycle fields for retained
session/governance signals: `lifecycle_kind`, `lifecycle_phase`,
`lifecycle_status`, and `entrypoint`.

Session event timeline entries include optional operation fields for retained
tool-call signals: `operation_id`, `category`, `outcome`, and `duration_ms`.
`operation_id` is session-scoped when the provider reports a call ID, preventing
cross-session call-ID reuse from collapsing evidence. These fields are populated
only when the provider event proves an executed tool call, such as Codex
`codex.tool_result`; ordinary model/lifecycle events leave them absent/null and
keep `tool_calls` in `unavailable_fields` where appropriate.

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

The daemon opens the existing local SQLite repository at the platform
configuration directory and reuses its installation-specific privacy salt.
The API never reads raw intake payloads. Session endpoints require a bearer token; the auth-token CLI command deliberately prints the protected local token for dashboard setup. Health and OTLP intake remain unauthenticated, and the daemon remains loopback-only by default.

Live OTLP persistence accepts `POST /v1/logs` (provider log events and reviewed Codex/Claude Code operation records), a
`POST /v1/metrics` path that persists reviewed Codex `codex.skill.injected`,
Claude Code `claude_code.token.usage`, and Cursor Enterprise
`cursor.token.usage` datapoints as canonical events, and a
`POST /v1/traces` path that persists Claude Code's enhanced-telemetry beta span
tree (`claude_code.interaction` → `claude_code.llm_request`) as canonical span
events. Cursor Enterprise `cursor.api.request` logs are also persisted via
`/v1/logs`. Other metrics and spans are accepted with HTTP 202 so exporters
flush, but are not turned into insight rows. Codex trace export is specifically
unsupported for the reviewed CLI 0.153.4 evidence (#112); Codex-shaped spans are
accepted but not persisted unless future fixture evidence proves support.

`POST /v1/claude/transcript` accepts Claude Code session JSONL (`application/x-ndjson`
or `application/jsonl`, 32 MiB cap) and persists normalised `assistant_message`
events correlated to the same provider-native session id as OTLP. Prompt/response
and tool content bodies are not retained on this path.
