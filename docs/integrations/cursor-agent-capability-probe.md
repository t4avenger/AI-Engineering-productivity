# Cursor Agent Capability Probe

Observed on 2026-08-31 with Cursor Agent 2026.05.16-0338208.

Evidence fixture: `fixtures/cursor/observed-sanitised/cursor-agent-2026.05.16-0338208-capability-probe.json`.
Reference docs: <https://docs.cursor.com/en/account/teams/admin-api>, <https://docs.cursor.com/background-agent/api/overview>.

## Result

The local CLI version is confirmed, but no reviewed usage API export or event-level fixture
has been captured in this workspace. Capability cells for model identity, token categories,
tool calls, MCP calls, skill invocations, file operations, session boundaries, and task
outcome therefore remain `unknown`.

Cursor documentation describes team usage events and background-agent APIs. That
documentation is setup guidance only for this project; it does not raise a matrix cell
without a committed sanitised API or event fixture from the configured account shape.

## Capture boundary

A future event fixture must come from an isolated synthetic workspace or approved usage
export, with prompt, response, source-code, path, identifier, and command-argument values
removed before commit. Do not raise any Cursor capability cell from `unknown` until that
event fixture exists.
