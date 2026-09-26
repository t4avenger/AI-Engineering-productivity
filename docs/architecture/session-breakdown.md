# Session duration breakdown

`GET /api/v1/sessions/{id}/breakdown` returns the full retained-session duration
partition for the Session Trace right rail (#190 / T09). The calculation is
independent of UI pagination and never invents Planning or User wait from idle
gaps.

## Response contract

```json
{
  "data": {
    "availability": "available",
    "calculation_version": "1",
    "window": {
      "start_at": "2026-09-20T10:00:00Z",
      "end_at": "2026-09-20T10:00:10Z",
      "duration_ms": 10000
    },
    "categories": [
      {
        "id": "model_generation",
        "label": "Model generation",
        "duration_ms": 4000,
        "percent": 40.0,
        "source_event_ids": ["canonical-event-id"]
      }
    ],
    "overlap": {"duration_ms": 2000, "percent": 20.0},
    "unclassified": {"duration_ms": 4000, "percent": 40.0},
    "coverage": {
      "state": "complete",
      "classified_ms": 6000,
      "window_ms": 10000
    },
    "unavailable_reason": null
  }
}
```

`availability` is `available` or `unavailable`. When unavailable (no positive
observed window, or no valid classified intervals), `window`, category rows,
overlap, unclassified, and coverage stay null/empty and `unavailable_reason`
explains the state. Clients must not render a donut for unavailable results.

## Interval contract

1. Project retained span envelopes with `spans.Project`. Deduplicate by
   `trace_id` + `span_id`.
2. Classify only proven span types:
   - `llm_request` → Model generation
   - `tool` / `tool.execution` → Tool calls
   - `tool.blocked_on_user` → User wait
   - `planning` → Planning (explicit evidence only)
3. Ignore inverted/missing intervals and mark coverage `partial`. Do not repair
   them.
4. Observe the wall window from session `started_at`/`completed_at` when both
   are present with positive duration; otherwise use the min/max of available
   span intervals.
5. Partition the window into disjoint segments: one category → that category;
   multiple different categories → Overlap; no classified evidence →
   Unclassified. Same-category nesting counts once.
6. Percentages use the observed window duration, not summed overlapping spans.
   Category + overlap + unclassified durations partition the window.

Interaction and hook spans are not duration categories. Codex spans without a
reviewed category mapping do not create classified intervals. The sole reviewed
Codex mapping is CLI 0.155.1 `session_task.turn` → Model generation: its
retained `turn.id` must exactly equal a same-session `codex.sse_event` log's
retained `turn.id`, and that log must report a model. `thread.id` joins the
session only; neither it, the span name, timestamps, tokens, nor model presence
alone is duration-category evidence.

Session detail HTML shares `breakdown.Calculate` for the right rail.
