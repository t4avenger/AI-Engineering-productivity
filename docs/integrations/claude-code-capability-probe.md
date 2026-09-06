# Claude Code Capability Probe

Observed on 2026-08-31 with Claude Code 2.1.251.

Evidence fixtures:
- `fixtures/claude/observed-sanitised/claude-code-2.1.251-capability-probe.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.251-otlp-events.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-skill-activated.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-skill-activated-otlp.json`

Reference docs: <https://code.claude.com/docs/en/monitoring-usage>, <https://code.claude.com/docs/en/env-vars>.

## Result

Local CLI version is confirmed and a synthetic Claude Code run emitted OTLP HTTP/JSON log events to the loopback development inspector with content logging disabled.

Observed from the event fixtures:
- model identity on `api_request`
- input, output, cache-read, and cache-creation token counts on `api_request`
- session identifier on event records, replaced with a synthetic placeholder before commit
- MCP server connection status, transport type, scope, and duration
- skill invocation identity on `skill_activated` (`skill.name`, `invocation_trigger`, `skill.source`) from Claude Code 2.1.263

Not observed:
- executed tool calls
- MCP call invocations
- file operations
- task outcome contract
- reasoning-token counts
- trace/span correlation

Claude Code documentation describes OpenTelemetry metrics, logs/events, and optional traces behind `CLAUDE_CODE_ENABLE_TELEMETRY`; only committed sanitised fixtures raise matrix cells above `unknown`.

## Capture boundary

A future event fixture must come from an isolated synthetic workspace, with prompt, response, source-code, path, identifier, and command-argument values removed before commit. Do not raise any Claude Code capability cell from `unknown` unless event fixture evidence exists. See [claude-fixture-capture.md](claude-fixture-capture.md).

