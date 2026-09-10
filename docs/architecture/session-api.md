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

The list supports `tool`, `model`, `outcome`, `started_after`, and
`started_before` filters. Dates are RFC3339; `started_after` is inclusive and
`started_before` is exclusive. A `model` filter matches only a retained,
observed event attribute named `model`. Sessions whose model is unavailable do
not match, preserving the distinction between unknown and zero or fabricated
values.

Session list and detail responses include an `availability` object for the
shared cross-tool fields rendered by the dashboard: `provider`, `tool`,
`outcome`, `started_at`, `completed_at`, `model`, `observed_events`, and
`token_usage`. Values are `observed`, `partial`, `unavailable`, `unsupported`,
or `unknown`. The UI must render non-observed states as labelled cells, never
as blanks or numeric zeroes.

Session event timeline entries include optional operation fields for retained
tool-call signals: `operation_id`, `category`, `outcome`, and `duration_ms`. `operation_id` is session-scoped when the provider reports a call ID, preventing cross-session call-ID reuse from collapsing evidence.
These fields are populated only when the provider event proves an executed tool
call, such as Codex `codex.tool_result`; ordinary model/lifecycle events leave
them absent/null and keep `tool_calls` in `unavailable_fields` where appropriate.

The MCP inventory insight response contains `data.totals`, `data.servers`, and `data.notes`. Server identities are the raw provider-reported MCP server names (`identity_state: provider_reported`), which are themselves the correlation key — epic #87 removed the HMAC fingerprint. Usage is `observed` only with explicit matching invocation evidence; otherwise it is `not_observed` or `unavailable`. Token context is request-level and labelled as not exact per-MCP allocation.

The daemon opens the existing local SQLite repository at the platform
configuration directory and reuses its installation-specific privacy salt.
The API never reads raw intake payloads. Session endpoints require a bearer token; the auth-token CLI command deliberately prints the protected local token for dashboard setup. Health and OTLP intake remain unauthenticated, and the daemon remains loopback-only by default.

Live OTLP persistence accepts `POST /v1/logs` (provider log events) and a
narrow `POST /v1/metrics` path that persists only reviewed Codex
`codex.skill.injected` datapoints as canonical skill events. Other metrics are
accepted with HTTP 202 so exporters flush, but are not turned into insight rows.
`POST /v1/traces` remains an honest refusal endpoint (`501`,
`error.code=not_implemented`) so exporters are never told a dropped payload was
accepted.
