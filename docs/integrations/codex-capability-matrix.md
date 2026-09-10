# Codex Capability Matrix

Observed with Codex CLI 0.145.0 in an isolated synthetic session on 2026-07-27.

| Capability | State | Evidence |
|---|---|---|
| OTLP log export | supported | Loopback receiver accepted live payloads. |
| Tool version | supported | service.version reported 0.145.0. |
| Model identity | supported | model attribute observed; extracted into `canonical.ModelInteraction.model` by `codex.ExtractLogModelInteractions` (absent → `"unknown"`, provenance downgraded). |
| Token usage | partial | Sanitised fixture and normaliser retain input and output token attributes, extracted into `ModelInteraction.input_tokens`/`output_tokens` (nil when absent or unparseable, never a fabricated zero). Cache, reasoning, and tool-token usage require separate reviewed fixture evidence and stay nil. |
| Session identifier | partial | `conversation.id` is retained raw in the local-only edition as `codex:<conversation.id>` when present; `request_id` and fallback session identity use a non-keyed content ID for uniqueness only (epic #87 — no HMAC fingerprint). |
| Log exporter service.name | supported | Both `codex_cli_rs` (interactive TUI) and `codex_exec` (non-interactive `codex exec`) observed; the log adapter accepts both. |
| Prompt/response content | unsupported | not enabled; log body is removed before inspection. |
| Account, email, hostname | unsupported | observed as sensitive attributes and removed before output. |
| Trace/span correlation | unsupported | observed log records had empty trace/span IDs. |
| Skill invocations | version-dependent | `codex.skill.injected` metric with `skill`, `status`, `invoke_type` in `fixtures/codex/observed-sanitised/codex-0.153.4-skill-injected-metrics.json` (CLI 0.153.4) maps to explicit named skill usage. A live trigger-style `bmad:brainstorm` probe on 2026-09-06 emitted `codex.skill.turn.duration_seconds` with `status` and `plugin_id=unattributed`, but no skill name; this maps only to inferred skill-detection coverage. Companion logs do not carry skill identity on tool events. |

No capability not listed here is inferred from this run.

## Implementation note

The Codex log adapter contains defensive handling for newer `codex.tool_result` metadata observed locally on 2026-09-06: when a log record contains a non-empty `mcp_server`, it stores the raw `provider_extensions.mcp_call.server_name` (the correlation identity, `identity_state: provider_reported`) and safe invocation metadata while promoting the server name out of generic log attributes. This remains implementation support, not a matrix capability claim, until a reviewed committed fixture is added.

Skill identity is taken only from `codex.skill.injected` metrics, never inferred from `tool_name` on `codex.tool_result`.

