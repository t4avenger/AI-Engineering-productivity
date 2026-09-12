# Claude Code session transcript deployment (SaaS)

This guide is for an **org admin** rolling TelemetryIQ transcript capture out to a
fleet of developer machines with **zero developer action**. It covers the
client-side deployment that feeds `POST /v1/claude/transcript` (the F4 import
route, see [claude-normaliser.md](claude-normaliser.md#transcript-path--normalizetranscript)).

> **Verify one snippet before production.** The Claude Code hook block below is
> written against the documented hooks schema, but hook field names and the
> `SessionEnd` stdin payload can change between Claude Code releases. Confirm the
> exact block against the **current** Claude Code hooks reference (`Settings →
> Hooks`, or `claude` docs for your pinned version) before an MDM rollout, and
> pin the Claude Code version your fleet runs.

## Why a hook, not a file watcher

TelemetryIQ is hosted: the backend cannot watch a developer's filesystem, so
ingest is always an HTTP push. The session JSONL transcript
(`~/.claude/projects/**/<session>.jsonl`) reaches the endpoint via a Claude Code
**`SessionEnd` hook** — it fires once per session and receives the session's
`transcript_path` on stdin. Capture is configured exactly like the OTLP exporter
already is: through Claude Code **enterprise managed settings** deployed by MDM
(Intune/Jamf/…), which are enforced and cannot be disabled by the developer.

## Managed settings paths (enforced, per OS)

| OS | Path |
|---|---|
| macOS | `/Library/Application Support/ClaudeCode/managed-settings.json` |
| Linux / WSL | `/etc/claude-code/managed-settings.json` |
| Windows | `C:\Program Files\ClaudeCode\managed-settings.json` |

The same `managed-settings.json` both points the OTLP exporter at the tenant
endpoint (`env`) and registers the transcript hook (`hooks`), so OTLP and
transcript data land in the same tenant with one deployed file.

## `managed-settings.json` (shape)

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://ingest.<tenant>.telemetryiq.example",
    "TELEMETRYIQ_ORG_KEY": "<org-api-key>"
  },
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/opt/telemetryiq/ship-transcript.sh"
          }
        ]
      }
    ]
  }
}
```

- `TELEMETRYIQ_ORG_KEY` is delivered through the managed `env` block — the same
  mechanism that carries the OTLP endpoint — so the hook script has the org key
  without any developer setup. (F4's ingest routes are currently unauthenticated,
  matching the other `/v1/*` push routes; org-key→tenant auth is a cross-cutting
  concern that will land on all ingest routes at once, at which point the hook
  already sends this header.)
- Deploy `ship-transcript.sh` (below) to the fixed path the hook references via the
  same MDM package.

## `ship-transcript.sh`

The `SessionEnd` hook passes a JSON object on **stdin** that includes
`transcript_path`. The script reads it and POSTs the raw `.jsonl`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# SessionEnd delivers a JSON object on stdin; transcript_path points at the
# session's .jsonl. (Confirm the field name against your Claude Code version.)
payload="$(cat)"
transcript_path="$(printf '%s' "$payload" | jq -r '.transcript_path // empty')"
[ -n "$transcript_path" ] && [ -f "$transcript_path" ] || exit 0

curl -sS --fail-with-body \
  --max-time 30 \
  -H "Content-Type: application/x-ndjson" \
  -H "Authorization: Bearer ${TELEMETRYIQ_ORG_KEY:-}" \
  --data-binary "@${transcript_path}" \
  "${OTEL_EXPORTER_OTLP_ENDPOINT%/}/../v1/claude/transcript" || exit 0
```

- Content-Type is `application/x-ndjson` (also accepted: `application/jsonl`) — the
  route rejects anything else with `415`.
- The transcript is POSTed **whole**; the route caps the body at 32 MiB.
- The script exits `0` even on failure so a transient ingest error never blocks a
  developer's session teardown; the next session's ship re-covers any gap.

## Flush-lag and why re-shipping is safe

When `SessionEnd` fires, the `.jsonl` may not be fully flushed, so an early ship
can be incomplete. This is safe because F4 is **idempotent**: each event's
`event_id = claude-code:<sessionId>:<uuid>` is deterministic, and persistence uses
`INSERT OR IGNORE`. A later, complete ship of the same transcript fills in any
records an early partial ship missed **without** duplicating events. If you want a
belt-and-braces second ship, add the same command under a `Stop` hook as well —
the dedup contract makes repeated shipping harmless.

## What is and isn't captured

The endpoint persists only the normalised, allow-listed `assistant_message`
events: model, token usage (including reasoning/thinking tokens), and safe scalar
envelope fields. Prompt/response text, tool inputs/results, and file diffs in the
transcript are **never** read into storage (owned by E7 #94 / J18 #105), and the
route does not wire the dev ingest inspector, so the raw transcript body is never
echoed back. See [claude-normaliser.md](claude-normaliser.md#transcript-path--normalizetranscript)
for the full field contract.

## Validate the rollout

On one machine, confirm the managed file is enforced (`claude` reports managed
settings), run a short session, and check the tenant:

- `GET /api/v1/sessions` shows the `anthropic`/`claude-code` session.
- `GET /api/v1/sessions/{id}/events` shows an `assistant_message` event with the
  model and token counts, correlated with any OTLP data for the same session id.
