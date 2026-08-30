# Multi-Provider Capability Matrix

P0 gate artifact. This matrix records, per provider, whether a behaviour signal is
actually derivable from observed telemetry — before any canonical type or dashboard is
built on it. No cell may be raised above `unknown` without version-pinned fixture evidence
committed under `fixtures/<provider>/` and referenced in the Evidence column.

States: `supported | partial | unsupported | unknown | version-dependent`.

Provider-specific per-tool findings:
- Codex: [codex-capability-matrix.md](codex-capability-matrix.md), [codex-phase-0-findings.md](codex-phase-0-findings.md)
- Claude Code: pending — see task "Capture version-pinned fixtures for all three tools"
- Cursor: pending — same

## Observed telemetry sources per provider

| Provider | Version observed | Sources |
|---|---|---|
| Codex | CLI 0.145.0 (2026-07-27) | OTLP JSON logs to loopback receiver |
| Claude Code | not yet captured | expected: OTLP + session JSONL (unconfirmed) |
| Cursor | not yet captured | expected: usage/admin API; event-level unconfirmed |

## Capability matrix

| Capability | Codex | Claude Code | Cursor | Notes |
|---|---|---|---|---|
| Model identity | supported | unknown | unknown | Codex: `model` attribute observed. |
| Token: input/output | partial | unknown | unknown | Codex retains input+output; **cached/reasoning unconfirmed**. |
| Token: cached | unknown | unknown | unknown | No provider confirmed yet. |
| Token: reasoning | unknown | unknown | unknown | No provider confirmed yet. |
| Tool calls (generic) | unknown | unknown | unknown | Codex fixture did not yet expose tool-call spans. |
| MCP calls | unknown | unknown | unknown | Must be proven, not inferred. Skill/MCP identity may be a tool name only. |
| Skill invocations | unknown | unknown | unknown | Mark `explicit \| inferred \| unavailable` once observed. |
| File operations | unknown | unknown | unknown | Needed for risky-access governance. |
| Session boundaries | partial | unknown | unknown | Codex: `conversation.id` observed but privacy-removed; correlation TBD. |
| Trace/span correlation | unsupported | unknown | unknown | Codex logs had empty trace/span IDs — correlation is a real risk. |
| Task outcome | unknown | unknown | unknown | Needed for model-performance scorecard; likely needs outcome contracts. |
| Prompt/response content | unsupported | unknown | unknown | Not collected by default; out of scope for MVP. |

## How to raise a cell above `unknown`
1. Capture a sanitised, version-pinned fixture per `codex-fixture-capture.md`.
2. Commit expected canonical output + warnings alongside it.
3. Update the cell with state + a one-line Evidence pointer to the fixture path and tool version.
4. Never encode a signal one provider emits as "canonical truth" until a second provider
   produces semantically equivalent data (keep it in `provider_extensions` until then).
