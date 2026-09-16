# Correlation and deterministic replay

P1 correlation is a normalisation responsibility. Provider adapters must emit canonical observations in a stable order, collapse exact duplicate source observations, and preserve the source relationships that are available without retaining prompt, response, source-code, file-path, or command-argument content.

## Deduplication

For the synthetic Codex OTLP trace fixture path, the deduplication key is:

```text
codex:<traceId>:<spanId>
```

Codex CLI 0.153.4 trace export is recorded as unsupported in the capability
matrix because the F1 capture observed zero `/v1/traces` POSTs. Live `/v1/traces`
ingest accepts Codex-shaped spans for exporter compatibility but does not persist
them unless a future reviewed Codex trace fixture proves a supported surface.

The same value is used as the canonical `event_id` and is copied to `provider_extensions.correlation.dedup_key` so downstream storage, diagnostics, and tests can explain why a duplicate collapsed. Replaying a fixture twice must not create a second observation. If two source spans present the same trace/span identity, the normaliser sorts first and keeps one deterministic canonical event for that key.

Codex OTLP log records observed in 0.145.0 can carry `conversation.id`. Log-derived events and `canonical.ModelInteraction` records use the raw `codex:<conversation.id>` as the session ID. Records without a conversation ID fall back to a non-keyed content ID (epic #87 removed ingest-time hiding — no HMAC fingerprint). Their deduplication key remains the `request_id`, which is copied to `provider_extensions.correlation.dedup_key`.

## Session identity scope

SQLite reconstructs one retained row for every distinct event `session_id`; it
does not merge rows unless provider evidence proves a shared identifier. Each
reconstructed row records `attributes.identity_scope` and
`attributes.identity_source`:

- `provider` identifies a provider-backed conversation/session ID, with a source
  such as `conversation.id`, `session.id`, or `cursor.conversation.id`.
- `observation` identifies a content-derived or trace-only row that is useful as
  evidence but is not proven to represent a whole coding session. This includes
  `codex-log:*`, Codex token/skill metric IDs, Cursor token metric IDs, Claude
  trace fallbacks, and provider `:unknown` fallbacks.
- `unknown` / `unproven` preserves legacy or unfamiliar rows without asserting
  either a provider session or an observation identity.

This classification changes list scope only. Events and reconstructed rows are
not deleted, aliased, or attached to a nearby provider session. In particular,
Codex turn-token metrics currently carry no conversation ID, so timing, model,
and arrival order must not be used to merge them into a conversation-backed
row.

## Ordering

Adapters must sort canonical events by:

1. observed occurrence time, ascending
2. canonical event ID, ascending
3. event type, ascending
4. received time, ascending

Synthetic Codex trace fixture events also expose `provider_extensions.correlation.ordering_key` as:

```text
<zero-padded occurred_at unix nanoseconds>:<event_id>
```

Codex log-derived `canonical.ModelInteraction` records are sorted by:

1. observed start time, ascending
2. request ID, ascending
3. observed completion time, ascending

Their `provider_extensions.correlation.ordering_key` uses the same zero-padded timestamp form with `started_at` and `request_id`:

```text
<zero-padded started_at unix nanoseconds>:<request_id>
```

These rules make shuffled fixture replay byte-identical after JSON serialisation and give storage a stable tie-breaker that does not depend on intake order.

## Trace and span relationships

For the synthetic Codex OTLP trace fixture path, source trace relationships are retained in `provider_extensions.correlation`:

- `trace_id`: observed OTLP `traceId`
- `span_id`: observed OTLP `spanId`
- `parent_span_id`: observed OTLP `parentSpanId`, or `null` when absent

`parentSpanId` is promoted into the correlation object and excluded from generic unknown span fields to avoid duplicating evidence.

## Task boundaries

The current reviewed Codex CLI 0.153.4 capture observed no live trace export, and the reviewed Codex trace fixture is synthetic only. Codex logs also do not expose a privacy-safe task-boundary signal. Synthetic trace fixture events therefore keep `task_id` as `null` and carry:

```json
{
  "task_boundary": {
    "confidence": "unknown",
    "reason": "Codex trace telemetry has no reviewed task-boundary signal"
  }
}
```

Log-derived model-interaction records carry the same `confidence: "unknown"` marker with a log-specific reason under `provider_extensions.correlation.task_boundary`.

Adapters must not infer a task boundary from timing, ordering, model name, or content. A future provider-specific task signal can only become a canonical task ID after fixture evidence and a privacy review.

## Replay contract

A conforming replay of the same sanitised fixture must produce byte-identical canonical observations when:

- the fixture is replayed repeatedly
- source spans are shuffled
- duplicate source spans are included

Unknown values remain unknown rather than zero or empty strings. Unknown provider fields remain in `provider_extensions` unless promoted into a documented correlation field.
