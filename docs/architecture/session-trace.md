# Session Trace five-lane assembly

Session detail (`GET /sessions/{id}`) composes Image #2's five-lane Session Trace
(#159 / T01–T10) from existing read projections. It does not add public APIs or
invent stage sequences, per-file diffs, or session joins.

## Composition

| Lane | Source |
|---|---|
| Conversation | `conversation.Project` retained Claude Code content |
| Agent | Model / lifecycle events plus `planning` / `llm_request` spans |
| Tools & MCP | Operations, skills, MCP events, and tool spans |
| Files | `insights.SessionFilesFromEvidence` |
| Spans | `spans.Project` flat nodes (client nesting by parent id only) |

Shared relative time uses session `started_at` when present, otherwise the earliest
retained timestamp labelled as an observed-window origin. Events without usable
timestamps render in **Unplaced events**. Point events stay points; durations are
never inferred.

Selection deep links use `?event=&inspector=&source=trace` and keep the #189
inspector. The right rail remains the #190 Session Breakdown / Governance /
Event Legend. Chronological conversation, files, spans, and timeline lists stay
available as the accessible alternative (T10).

## Honesty rules

- Unknown values never become zero.
- Planning labels require explicit `planning` evidence; otherwise the Agent lane
  reports planning telemetry unavailable.
- Codex trace-only observations are not joined to conversations by time or model.
- Share remains unavailable until the E03 architecture gate (#198).
