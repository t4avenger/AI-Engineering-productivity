# Codex Capability Matrix

Observed with Codex CLI 0.145.0 in an isolated synthetic session on 2026-07-27.

| Capability | State | Evidence |
|---|---|---|
| OTLP log export | supported | Loopback receiver accepted live payloads. |
| Tool version | supported | service.version reported 0.145.0. |
| Model identity | supported | model attribute observed; extracted into `canonical.ModelInteraction.model` by `codex.ExtractLogModelInteractions` (absent → `"unknown"`, provenance downgraded). |
| Token usage | partial | Sanitised fixture and normaliser retain input and output token attributes, extracted into `ModelInteraction.input_tokens`/`output_tokens` (nil when absent or unparseable, never a fabricated zero). Cache, reasoning, and tool-token usage require separate reviewed fixture evidence and stay nil. |
| Session identifier | partial | conversation.id observed but removed by privacy policy; the extractor derives `request_id`/`session_id` from the installation HMAC fingerprint instead. |
| Log exporter service.name | supported | Both `codex_cli_rs` (interactive TUI) and `codex_exec` (non-interactive `codex exec`) observed; the log adapter accepts both. |
| Prompt/response content | unsupported | not enabled; log body is removed before inspection. |
| Account, email, hostname | unsupported | observed as sensitive attributes and removed before output. |
| Trace/span correlation | unsupported | observed log records had empty trace/span IDs. |

No capability not listed here is inferred from this run.
