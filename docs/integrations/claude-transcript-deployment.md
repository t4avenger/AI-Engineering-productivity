# Claude Code session transcript deployment

This guide is for an **org admin** configuring Claude Code managed settings so
developer machines push session JSONL to `POST /v1/claude/transcript` (F4;
see [claude-normaliser.md](claude-normaliser.md#transcript-path--normalizetranscript))
**with zero developer action**.

> **Auth status (read first).** Today every `/v1/*` ingest route — including
> `/v1/claude/transcript` — is **unauthenticated**, matching the local-first MVP
> (loopback by default). `TELEMETRYIQ_ORG_KEY` in the recipe below is **forward-
> looking scaffolding only**: the hook already sends `Authorization`, but the
> daemon does not validate it yet. Do **not** expose this endpoint on a shared
> HTTPS ingress until the cross-cutting org-key→tenant auth epic lands on all
> ingest routes. Until then, bind ingest to loopback / private network only.

> **Verify one snippet before production.** The Claude Code hook block below is
> written against the documented hooks schema, but hook field names and the
> `SessionEnd` stdin payload can change between Claude Code releases. Confirm the
> exact block against the **current** Claude Code hooks reference (`Settings →
> Hooks`, or `claude` docs for your pinned version) before an MDM rollout, and
> pin the Claude Code version your fleet runs.

## Why a hook, not a file watcher

The backend cannot watch a developer's filesystem, so ingest is always an HTTP
push. The session JSONL transcript (`~/.claude/projects/**/<session>.jsonl`)
reaches the endpoint via a Claude Code **`SessionEnd` hook** — it fires once per
session and receives the session's `transcript_path` on stdin. Capture is
configured through Claude Code **enterprise managed settings** deployed by MDM
(Intune/Jamf/…), which are enforced and cannot be disabled by the developer.

## Managed settings paths (enforced, per OS)

| OS | Path |
|---|---|
| macOS | `/Library/Application Support/ClaudeCode/managed-settings.json` |
| Linux / WSL | `/etc/claude-code/managed-settings.json` |
| Windows | `C:\Program Files\ClaudeCode\managed-settings.json` |

The same `managed-settings.json` both points the OTLP exporter at the ingest
endpoint (`env`) and registers the transcript hook (`hooks`).

> **POSIX-only hook recipe.** The `ship-transcript.sh` below requires Bash,
> `jq`, and `curl`. This document does **not** yet ship a native Windows
> `.cmd`/PowerShell hook. On Windows, deploy via WSL with the Linux managed-
> settings path, or wait for a Windows-native script in a follow-up.

## `managed-settings.json` (shape)

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:8080",
    "TELEMETRYIQ_ORG_KEY": "<unused-until-ingest-auth>"
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
    ],
    "Stop": [
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

- Deploy `ship-transcript.sh` (below) to the fixed path the hook references via the
  same MDM package.
- Registering the same command under both `SessionEnd` and `Stop` is intentional:
  flush lag on `SessionEnd` can leave a partial JSONL that the route rejects with
  `400`; `Stop` (and the script's own short retry) re-ships the same path once the
  file has stabilised. Idempotent `event_id`s mean duplicate successful ships are
  harmless.

## `ship-transcript.sh`

The hook passes a JSON object on **stdin** that includes `transcript_path`. The
script waits briefly for the file to stop growing, then POSTs the raw `.jsonl`
with a few retries so a flush-lag `400` does not permanently drop the session:

```bash
#!/usr/bin/env bash
set -euo pipefail

# SessionEnd/Stop deliver a JSON object on stdin; transcript_path points at the
# session's .jsonl. (Confirm the field name against your Claude Code version.)
payload="$(cat)"
transcript_path="$(printf '%s' "$payload" | jq -r '.transcript_path // empty')"
[ -n "$transcript_path" ] && [ -f "$transcript_path" ] || exit 0

# Wait until the transcript stops growing (flush lag), then POST with retries.
# Always exit 0 so a transient ingest error never blocks session teardown.
stable_bytes=""
for _ in 1 2 3 4 5; do
  bytes="$(wc -c < "${transcript_path}" | tr -d ' ')"
  if [ "${bytes}" = "${stable_bytes}" ]; then
    break
  fi
  stable_bytes="${bytes}"
  sleep 0.2
done

endpoint="${OTEL_EXPORTER_OTLP_ENDPOINT%/}/../v1/claude/transcript"
for _ in 1 2 3; do
  if curl -sS --fail-with-body \
    --max-time 30 \
    -H "Content-Type: application/x-ndjson" \
    -H "Authorization: Bearer ${TELEMETRYIQ_ORG_KEY:-}" \
    --data-binary "@${transcript_path}" \
    "${endpoint}"; then
    exit 0
  fi
  sleep 1
done
exit 0
```

- Content-Type is `application/x-ndjson` (also accepted: `application/jsonl`) — the
  route rejects anything else with `415`.
- The transcript is POSTed **whole**; the route caps the body at 32 MiB.
- Invalid JSONL syntax returns `400 malformed_payload`; missing structural fields
  on an `assistant` record return `422 normalization_failed`. Either case is
  retried by the loop above and/or the `Stop` hook.

## Flush-lag and why re-shipping is safe

When `SessionEnd` fires, the `.jsonl` may not be fully flushed. A partial file
that truncates mid-line is **rejected** (not partially persisted). Recovery is:

1. the script's size-stability wait + retries on the **same** `transcript_path`, and
2. the optional `Stop` hook re-ship of that same path.

F4 is **idempotent**: each event's `event_id = claude-code:<sessionId>:<uuid>` is
deterministic, and persistence uses `INSERT OR IGNORE`. A later complete ship of
the **same** transcript fills gaps without duplicating events. The next session's
transcript cannot recover a previous session — that is why same-path retry matters.

## What is and isn't captured

The endpoint persists only the normalised, allow-listed `assistant_message`
events: model, token usage (including reasoning/thinking tokens), and safe scalar
envelope fields. Prompt/response text are deferred to E7 (#94); MCP calls to J17
(#104); tool IO, diffs, and sub-agents to J18 (#105). The route does not wire the
dev ingest inspector, so the raw transcript body is never echoed back. See
[claude-normaliser.md](claude-normaliser.md#transcript-path--normalizetranscript)
for the full field contract.

## Validate the rollout

On one machine, confirm the managed file is enforced (`claude` reports managed
settings), run a short session, and check the daemon:

- `GET /api/v1/sessions` shows the `anthropic`/`claude-code` session.
- `GET /api/v1/sessions/{id}/events` shows an `assistant_message` event with the
  model and token counts, correlated with any OTLP data for the same session id.
