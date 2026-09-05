# Correlation and deterministic replay

P1 correlation is a normalisation responsibility. Provider adapters must emit canonical observations in a stable order, collapse exact duplicate source observations, and preserve the source relationships that are available without retaining prompt, response, source-code, file-path, or command-argument content.

## Deduplication

For Codex OTLP trace spans, the deduplication key is:

```text
codex:<traceId>:<spanId>
```

The same value is used as the canonical `event_id` and is copied to `provider_extensions.correlation.dedup_key` so downstream storage, diagnostics, and tests can explain why a duplicate collapsed. Replaying a fixture twice must not create a second observation. If two source spans present the same trace/span identity, the normaliser sorts first and keeps one deterministic canonical event for that key.

Codex OTLP log records observed in 0.145.0 do not expose a reviewed stable trace or session identifier. Log-derived records therefore continue to use the local installation fingerprint described by the Codex normaliser; no raw conversation identifier is retained.

## Ordering

Adapters must sort canonical events by:

1. observed occurrence time, ascending
2. canonical event ID, ascending
3. event type, ascending
4. received time, ascending

Codex trace events also expose `provider_extensions.correlation.ordering_key` as:

```text
<zero-padded occurred_at unix nanoseconds>:<event_id>
```

This makes shuffled fixture replay byte-identical after JSON serialisation and gives storage a stable tie-breaker that does not depend on intake order.

## Trace and span relationships

For Codex OTLP traces, source trace relationships are retained in `provider_extensions.correlation`:

- `trace_id`: observed OTLP `traceId`
- `span_id`: observed OTLP `spanId`
- `parent_span_id`: observed OTLP `parentSpanId`, or `null` when absent

`parentSpanId` is promoted into the correlation object and excluded from generic unknown span fields to avoid duplicating evidence.

## Task boundaries

The current reviewed Codex trace fixture does not expose a privacy-safe task-boundary signal. The canonical `task_id` therefore remains `null`, and each trace event carries:

```json
{
  "task_boundary": {
    "confidence": "unknown",
    "reason": "Codex trace telemetry has no reviewed task-boundary signal"
  }
}
```

Adapters must not infer a task boundary from timing, ordering, model name, or content. A future provider-specific task signal can only become a canonical task ID after fixture evidence and a privacy review.

## Replay contract

A conforming replay of the same sanitised fixture must produce byte-identical canonical observations when:

- the fixture is replayed repeatedly
- source spans are shuffled
- duplicate source spans are included

Unknown values remain unknown rather than zero or empty strings. Unknown provider fields remain in `provider_extensions` unless promoted into a documented correlation field.
