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
| Claude Code | 2.1.251 probed on 2026-08-31 | Local CLI version probe; OTLP HTTP/JSON log events from synthetic run; `fixtures/claude/observed-sanitised/claude-code-2.1.251-capability-probe.json`, `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json` |
| Cursor Agent | 2026.05.16-0338208 probed on 2026-08-31 | Local CLI version probe; authenticated print JSON result from synthetic run; local OTLP HTTP/JSON export not observed; `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-capability-probe.json`, `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json` |

## Capability matrix

| Capability | Codex | Claude Code | Cursor | Notes |
|---|---|---|---|---|
| Model identity | supported | supported | unknown | Codex: `model` attribute observed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`, now extracted into `canonical.ModelInteraction.model` by `codex.ExtractLogModelInteractions` (absent → `"unknown"`, provenance downgraded). Claude Code: `model` observed in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`. Cursor Agent result in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json` did not include model identity. |
| Token: input/output | partial | supported | supported | Codex: input/output token attributes observed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`, now extracted into `ModelInteraction.input_tokens`/`output_tokens` (nil when absent/unparseable, never a fabricated zero); cached/reasoning unconfirmed and left nil. Claude Code: input/output token counts observed in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`. Cursor Agent: input/output token counts observed in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`. |
| Token: cached | unknown | supported | supported | Claude Code: cache-read and cache-creation token counts observed in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`. Cursor Agent: cache-read and cache-write token counts observed in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`. Codex fixture does not prove cached-token data. |
| Token: reasoning | unknown | unknown | unknown | No reviewed provider event fixture proves reasoning-token data. |
| Tool calls (generic) | unknown | unknown | unknown | Codex fixture did not expose tool-call spans. Claude Code fixture does not include an executed tool-call event. Cursor Agent result does not include tool calls. |
| MCP calls | unknown | partial | unknown | Claude Code emitted MCP server connection events in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`, but no MCP call invocation was observed. Codex and Cursor fixtures do not prove MCP calls. |
| Skill invocations | unknown | unknown | unknown | Mark `explicit | inferred | unavailable` only after event fixture evidence. |
| File operations | unknown | unknown | unknown | Needed for risky-access governance; no reviewed event fixture proves file-operation telemetry. |
| Session boundaries | partial | partial | partial | Codex: `conversation.id` observed and privacy-removed in `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json`; the extractor derives `ModelInteraction.request_id`/`session_id` from the installation HMAC fingerprint instead; cross-record correlation remains unresolved (see #22). Claude Code: session identifier observed in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`. Cursor Agent: session identifier observed in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`. |
| Trace/span correlation | unsupported | unknown | unknown | Codex fixture `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json` had empty trace/span IDs. |
| Task outcome | unknown | unknown | partial | Cursor Agent result includes command success/error status in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`, but not a broader task outcome contract. Codex and Claude Code fixtures do not prove task outcome. |
| Prompt/response content | unsupported | unsupported | unsupported | Codex fixture `fixtures/codex/observed-sanitised/codex-0.145.0-logs.json` has no retained log body; content capture remains out of scope by default. Claude Code capture disabled content logging and committed fixture excludes content in `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`. Cursor Agent fixture records only a synthetic result summary in `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-print-result.json`. |

## How to raise a cell above `unknown`

1. Capture a sanitised, version-pinned fixture for the provider.
2. Commit expected canonical output and warnings alongside it where normalisation is in scope.
3. Update the cell with state and a one-line evidence pointer to the fixture path and tool version.
4. Keep provider-specific shapes in `provider_extensions` until two independent providers produce semantically equivalent data.
5. Keep absent evidence as `unknown` or `unsupported`; do not infer capabilities from configuration, documentation, or provider marketing copy.
