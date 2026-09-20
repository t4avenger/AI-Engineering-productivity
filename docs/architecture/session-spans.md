# Session span projection

`GET /api/v1/sessions/{id}/spans` is an authenticated, cursor-paged evidence
projection for the Session Trace spans lane. Its response is the standard
`data`/`pagination` envelope.

Each node is keyed by `trace_id` plus `span_id` and returns raw provider span
IDs, nullable `parent_span_id`, `name`, original `start_at`/`end_at`, derived
`duration_ms`, `status_code`, provider/tool/version provenance, source event
IDs, and only provider-proven `operation_ids`. The server returns flat records;
clients may derive nesting but must not manufacture joins.

`parent_availability` is `root`, `loaded`, `not_loaded`, or `absent`. It
distinguishes a parent on another cursor page from one absent from all retained
session evidence. `interval_availability` is `available`, `missing`, or
`invalid`; absent and reversed bounds never become a zero duration.

Records are sorted by observed start timestamp, then trace and span IDs. A
cursor is opaque and identifies the final returned trace/span tuple. Repeated
retained events for the same tuple collapse to one node with sorted source event
IDs. Nodes from different traces never collide, and cycles remain flat rather
than triggering recursive server work.

The projection reads only existing Claude Code and Codex trace envelopes. Codex traces with an observed resource-level `conversation.id` use the matching provider conversation session. Trace-only observations retain their `codex:trace:<trace-id>` session identity; the endpoint never joins them by timestamp, model, or other proximity. Missing provider fields remain null or explicitly unavailable.


## Response contract

```json
{
  "data": [{
    "trace_id": "observed-trace-id",
    "span_id": "observed-span-id",
    "parent_span_id": null,
    "parent_availability": "root",
    "name": "provider-emitted-span-name",
    "start_at": "2026-09-20T10:00:00Z",
    "end_at": "2026-09-20T10:00:01Z",
    "duration_ms": 1000,
    "interval_availability": "available",
    "status_code": 0,
    "provider": "openai",
    "tool": "codex",
    "source_version": "0.154.0",
    "source_event_ids": ["canonical-event-id"],
    "operation_ids": []
  }],
  "pagination": {"limit": 50, "next_cursor": "opaque-or-null"}
}
```

`data` is always an array, including an empty projection. `next_cursor` is null
at the end of the result set. A malformed, stale, or non-span cursor returns
`400 {"error":{"code":"invalid_query", "message":"..."}}`; unknown session
IDs return `404 session_not_found`; unavailable session/event storage returns
`503 sessions_unavailable`; and a read failure returns `500 span_query_failed`.

The reviewed Claude Code surface is OTLP enhanced-telemetry 2.1.268 traces;
Claude uses provider `session.id` when observed and otherwise keeps a
trace-scoped session. The reviewed Codex trace surfaces are CLI 0.154.0, which remains trace-only, and CLI 0.155.1, whose resource-level `conversation.id` exactly matched same-run logs in the committed fixture. Other versions and traces without that key remain `codex:trace:<trace-id>` observations; consumers must display unavailable rather than infer a join.
