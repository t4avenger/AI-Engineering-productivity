# Session API

The Phase 2 local management API provides authenticated session endpoints:

- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{id}`
- `DELETE /api/v1/sessions/{id}`
- `GET /api/v1/costs/summary`, `GET /api/v1/sessions/{id}/costs`, `GET /api/v1/insights/mcp-inventory`, `DELETE /api/v1/sessions`

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

The MCP inventory insight response contains `data.totals`, `data.servers`, and `data.notes`. Server identities include provider-reported MCP server names when available, plus privacy-safe fingerprints for correlation. Usage is `observed` only with explicit matching invocation evidence; otherwise it is `not_observed` or `unavailable`. Token context is request-level and labelled as not exact per-MCP allocation.

The daemon opens the existing local SQLite repository at the platform
configuration directory and reuses its installation-specific privacy salt.
The API never reads raw intake payloads. Session endpoints require a bearer token; the auth-token CLI command deliberately prints the protected local token for dashboard setup. Health and OTLP intake remain unauthenticated, and the daemon remains loopback-only by default.
