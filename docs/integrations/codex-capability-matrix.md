# Codex Capability Matrix

Observed with Codex CLI 0.145.0 in an isolated synthetic session on 2026-07-27.

| Capability | State | Evidence |
|---|---|---|
| OTLP log export | supported | Loopback receiver accepted live payloads. |
| Tool version | supported | service.version reported 0.145.0. |
| Model identity | supported | model attribute observed. |
| Token usage | supported | input, output, cache, reasoning, and tool token attributes observed. |
| Session identifier | partial | conversation.id observed but removed by privacy policy. |
| Prompt/response content | unsupported | not enabled; log body is removed before inspection. |
| Account, email, hostname | unsupported | observed as sensitive attributes and removed before output. |
| Trace/span correlation | unsupported | observed log records had empty trace/span IDs. |

No capability not listed here is inferred from this run.
