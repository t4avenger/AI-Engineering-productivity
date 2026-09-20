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

The projection reads only existing Claude Code and Codex trace envelopes. Codex
trace-only observations retain their `codex:trace:<trace-id>` session identity;
the endpoint never joins them to conversation sessions by timestamp, model, or
other proximity. Missing provider fields remain null or explicitly unavailable.
