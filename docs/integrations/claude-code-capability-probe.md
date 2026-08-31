# Claude Code Capability Probe

Observed on 2026-08-31 with Claude Code 2.1.251.

Evidence fixture: `fixtures/claude/observed-sanitised/claude-code-2.1.251-capability-probe.json`.
Reference docs: <https://code.claude.com/docs/en/monitoring-usage>, <https://code.claude.com/docs/en/env-vars>.

## Result

The local CLI version is confirmed, but no reviewed OTLP or session JSONL event fixture has
been captured in this workspace. Capability cells for model identity, token categories,
tool calls, MCP calls, skill invocations, file operations, session boundaries, and task
outcome therefore remain `unknown`.

Claude Code documentation describes OpenTelemetry metrics, logs/events, and optional traces
behind `CLAUDE_CODE_ENABLE_TELEMETRY`. That documentation is setup guidance only for this
project; it does not raise a matrix cell without a committed sanitised event fixture.

## Capture boundary

A future event fixture must come from an isolated synthetic workspace, with prompt,
response, source-code, path, identifier, and command-argument values removed before commit.
Do not raise any Claude Code capability cell from `unknown` until that event fixture exists.
