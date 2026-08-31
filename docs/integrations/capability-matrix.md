# Multi-Provider Capability Matrix

P0 gate artifact. This matrix records, per provider, whether a behaviour signal is
actually derivable from observed telemetry before any canonical type or dashboard is
built on it. No cell may be raised above `unknown` without version-pinned fixture evidence
committed under `fixtures/<provider>/` and referenced in the Evidence column.

States: `supported | partial | unsupported | unknown | version-dependent`.

Provider-specific per-tool findings:
- Codex: [codex-capability-matrix.md](codex-capability-matrix.md), [codex-phase-0-findings.md](codex-phase-0-findings.md)
- Claude Code: [claude-code-capability-probe.md](claude-code-capability-probe.md)
- Cursor Agent: [cursor-agent-capability-probe.md](cursor-agent-capability-probe.md)

## Observed telemetry sources per provider

| Provider | Version observed | Sources |
|---|---|---|
| Codex | CLI 0.145.0 fixture; local CLI 0.151.0 probed on 2026-08-31 | OTLP JSON logs to loopback receiver; `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json` |
| Claude Code | 2.1.251 probed on 2026-08-31 | Local CLI version probe only; `fixtures/claude/observed-sanitised/claude-code-2.1.251-capability-probe.json` |
| Cursor Agent | 2026.05.16-0338208 probed on 2026-08-31 | Local CLI version probe only; `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-capability-probe.json` |

## Capability matrix

| Capability | Codex | Claude Code | Cursor | Notes |
|---|---|---|---|---|
| Model identity | supported | unknown | unknown | Codex: `model` attribute observed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`. Claude/Cursor probes prove installed versions only, not event-level model telemetry. |
| Token: input/output | partial | unknown | unknown | Codex: input/output token attributes observed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`; cached/reasoning unconfirmed. |
| Token: cached | unknown | unknown | unknown | No reviewed provider event fixture proves cached-token data. |
| Token: reasoning | unknown | unknown | unknown | No reviewed provider event fixture proves reasoning-token data. |
| Tool calls (generic) | unknown | unknown | unknown | Codex fixture did not expose tool-call spans; Claude/Cursor probes have no event-level telemetry. |
| MCP calls | unknown | unknown | unknown | Must be proven from event data, not inferred from configuration or CLI support. |
| Skill invocations | unknown | unknown | unknown | Mark `explicit | inferred | unavailable` only after event fixture evidence. |
| File operations | unknown | unknown | unknown | Needed for risky-access governance; no reviewed event fixture proves file-operation telemetry. |
| Session boundaries | partial | unknown | unknown | Codex: `conversation.id` observed and privacy-removed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`; correlation remains unresolved. |
| Trace/span correlation | unsupported | unknown | unknown | Codex fixture `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json` had empty trace/span IDs. |
| Task outcome | unknown | unknown | unknown | Needed for model-performance scorecard; likely needs outcome contracts. |
| Prompt/response content | unsupported | unknown | unknown | Codex fixture `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json` has no retained log body; content capture remains out of scope by default. |

## How to raise a cell above `unknown`

1. Capture a sanitised, version-pinned fixture for the provider.
2. Commit expected canonical output and warnings alongside it where normalisation is in scope.
3. Update the cell with state and a one-line evidence pointer to the fixture path and tool version.
4. Keep provider-specific shapes in `provider_extensions` until two independent providers produce semantically equivalent data.
5. Keep absent evidence as `unknown` or `unsupported`; do not infer capabilities from configuration, documentation, or provider marketing copy.
