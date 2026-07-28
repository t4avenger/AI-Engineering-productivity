# Session API

Task 009 provides local read-only session endpoints:

- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{id}`

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

The daemon opens the existing local SQLite repository at the platform
configuration directory and reuses its installation-specific privacy salt.
The API never reads raw intake payloads and the live ingest-to-storage path is
not part of this task. Authentication is also deferred; the daemon remains
loopback-only by default.
