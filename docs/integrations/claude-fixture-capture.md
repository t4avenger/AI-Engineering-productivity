# Claude Code Fixture Capture Procedure

Defines the supported process for adding a Claude Code telemetry fixture.
Fixtures are sensitive even when prompt and response capture is disabled.

## Capture rules

1. Use an isolated local test repository with synthetic prompts, skills, source
   files, commands, and credentials only.
2. Enable only the minimum supported local Claude Code telemetry export needed
   for the experiment. Never enable prompt, response, or source-code capture
   unless the experiment explicitly requires a gated attribute (for example
   `OTEL_LOG_TOOL_DETAILS=1` to retain custom skill names, or the
   `OTEL_LOG_USER_PROMPTS` / `OTEL_LOG_ASSISTANT_RESPONSES` / `OTEL_LOG_RAW_API_BODIES`
   content gates for #94 content fixtures — with synthetic prompts only).
3. Record the Claude Code tool version and export format before copying an event
   into a temporary local file outside this repository.
4. Remove prohibited fields and replace file paths, identifiers, emails, account
   IDs, and command arguments with synthetic values. Preserve unknown field
   names and structural shape where safe.
5. Add fixture metadata: `fixture_version`, `fixture_origin`, `provider`, `tool`,
   `tool_version`, RFC3339 `captured_at`, and `sanitisation_reviewed` true.
6. Run the validator and a second human review before staging the file. Never
   commit the original capture.

## Skill invocation capture

To raise Skill invocations above `unknown`, run a synthetic skill (for example a
project skill named `tiq-probe`) in an isolated workspace with:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:8080
export OTEL_LOGS_EXPORT_INTERVAL=2000
# Optional: retain custom skill names (default collapses to custom_skill)
export OTEL_LOG_TOOL_DETAILS=1
```

Invoke the skill (for example `claude -p '/tiq-probe'`), flush the exporter, then
sanitise. The reviewed skill signal is the OTLP log event
`event.name = skill_activated` with `skill.name`, `invocation_trigger`, and
`skill.source`.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.263-skill-activated.json`
  (sample-event summary for `NormalizeEvents`)
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-skill-activated-otlp.json`
  (sanitised OTLP `resourceLogs` for live ingest)

## Metrics capture

To capture the Claude Code OTLP **metrics** surface (`POST /v1/metrics`), run a
synthetic turn against a loopback sink with the metrics exporter enabled. Do not
set `OTEL_SERVICE_NAME` or force resource attributes — the point is to observe
what Claude Code emits by default (it stamps `service.name = claude-code`):

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_METRIC_EXPORT_INTERVAL=2000
```

Run a synthetic prompt (`claude -p 'Reply with exactly the word: pong'`) so a real
model request produces `claude_code.token.usage`, then let the exporter flush
before exit. The observed shape (tool 2.1.268): `claude_code.token.usage` is a
monotonic **sum** whose datapoints encode the value as `asDouble` and carry the
token category in a camelCase `type` attribute
(`input`/`output`/`cacheRead`/`cacheCreation`), with `session.id`, `model`, and
`query_source` stamped per datapoint. Sanitise per the rules above — replace
`user.id`, `session.id`, `organization.id`, `user.email`, `user.account_*`, and
host identifiers with synthetic values (the raw 64-char `user.id` hash trips the
validator's entropy check), keeping the instrument/datapoint shape intact.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.268-token-usage-metrics.json`
  (sanitised OTLP `resourceMetrics` for live `/v1/metrics` ingest)
- `fixtures/claude/expected/claude-code-2.1.268-token-usage-metrics.events.json`
  (golden canonical token-usage events)

## Traces capture

To capture the Claude Code OTLP **traces** surface (`POST /v1/traces`), the
enhanced-telemetry beta must be enabled — the span exporter is gated behind it and
the default telemetry build emits no spans. Run a synthetic turn against a loopback
sink with the traces exporter enabled:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
export OTEL_TRACES_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_TRACES_EXPORT_INTERVAL=2000
```

Run a synthetic prompt (`claude -p 'Reply with exactly the word: pong'`) so a real
model request produces a span tree, then let the exporter flush before exit. The
observed shape (tool 2.1.268): a `resourceSpans` envelope with `service.name =
claude-code`, scope `com.anthropic.claude_code.tracing`, and a two-span tree — a
root `claude_code.interaction` span and a child `claude_code.llm_request` span
whose `parentSpanId` is the interaction's `spanId`. Each span carries
`traceId`/`spanId`, `startTimeUnixNano`/`endTimeUnixNano`, `kind`, `status`, and
per-span `attributes` (`span.type`, `gen_ai.*`, token counts, `stop_reason`,
`interaction.*`). Sanitise per the rules above — replace `user.id`, `session.id`,
`organization.id`, `user.email`, host identifiers, and any request/response IDs
with synthetic values, and zero-pad the synthetic `traceId`/`spanId` while keeping
the parent→child linkage intact.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.268-trace-spans-otlp.json`
  (sanitised OTLP `resourceSpans` for live `/v1/traces` ingest)
- `fixtures/claude/expected/claude-code-2.1.268-trace-spans.events.json`
  (golden canonical span events)

## Outcome-contract capture

To raise Task outcome above `unknown`, capture provider-completion signals without
content logging:

1. Successful turn → OTLP `event.name = api_request` with `model`, tokens, `duration_ms`.
2. Failed turn → OTLP `event.name = api_error` with `model`, `status_code`/`error`, `duration_ms`.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-request-outcome.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-request-outcome-otlp.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-error-outcome.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-error-outcome-otlp.json`

## Refusal capture (`api_refusal`, E8/#95)

A refusal is the third provider-completion outcome: the Messages API returns
`stop_reason: "refusal"` as HTTP `200 OK`, and Claude Code emits
`event.name = api_refusal` with `model`, `request.id`, `duration_ms`,
`server_fallback_hop`, `has_category`/`has_explanation`, and (under
`OTEL_LOG_TOOL_DETAILS=1`) `category` / `explanation`.

**Live capture is bounded by content safety.** A genuine `stop_reason: "refusal"`
only fires for frontier-safety categories (`cyber` / `bio` / `frontier_llm` /
`reasoning_extraction`). Do **not** author a real bio/cyber/weapons prompt to force
one. When the E8 fixture was produced, a loopback OTLP-logs sink was stood up and a
benign `reasoning_extraction` probe was run against a throwaway workspace — it
produced a normal soft decline (`stop_reason: end_turn`), **not** an API refusal.
Because the only remaining lever was a harmful-category prompt, the fixture is
`fixture_origin: "synthetic"`, reproduced from the documented `api_refusal` schema:

- one user-visible refusal (`server_fallback_hop: false`, `has_category: true`,
  `category: "cyber"`, `has_explanation: true`, `explanation`), and
- one silently-retried hop (`server_fallback_hop: true`, `has_category: false`,
  explanation attributes omitted, matching the documented absence).

If a real refusal is ever captured, re-label the fixture `observed-sanitised` and
record the tool version actually used.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-refusal-outcome.json`
- `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-refusal-outcome-otlp.json`
- `fixtures/claude/expected/claude-code-2.1.263-api-refusal-outcome.events.json`

## Session JSONL transcript capture

To capture a session JSONL transcript fixture for `NormalizeTranscript` (F4, #91),
sanitise a real transcript from `~/.claude/projects/**/<session>.jsonl`:

1. The wrapper is the standard fixture envelope; put the sanitised records under
   `payload.source_type = "session_jsonl_transcript"` and
   `payload.transcript_lines[]` (one JSON object per element, mirroring one line of
   the on-disk NDJSON). The golden test re-serialises them to NDJSON before
   feeding `NormalizeTranscript`, so the fixture replays the real on-disk shape.
2. Keep the shared envelope keys (`type`, `uuid`, `parentUuid`, `sessionId`,
   `timestamp`, `version`, …) and, on assistant records, `message.model` and the
   full `message.usage` block (including `output_tokens_details.thinking_tokens`
   and `cache_creation.ephemeral_*`) — these are the numeric behaviour signals F4
   captures.
3. Replace every content body with innocuous synthetic text: prompt/response
   text, thinking, tool `input`, and `toolUseResult`. The fixture validator
   prohibits the *flat* field names `command`, `file_path`/`filename`, `prompt`,
   `response`, `token`, `secret`, etc., so a fixture cannot legally carry those
   tool-content keys — which is why the content drop-guarantee is proven by a
   direct-NDJSON canary test in `jsonl_test.go`, not by the committed fixture.
4. Include ≥2 auxiliary record types (e.g. `ai-title`, `cost-state`) and a
   `system` record to prove the skip-unknown behaviour, plus a `user` record.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.269-session-transcript.json`
  (sanitised transcript records for live `/v1/claude/transcript` import)
- `fixtures/claude/expected/claude-code-2.1.269-session-transcript.events.json`
  (golden canonical `assistant_message` events)

## Tool-decision capture

To raise Approval / permission decisions above `unknown`, capture `tool_decision`
events covering both a grant and a denial, from more than one `source`. The
denial is the point of the exercise — `tool_result.decision_type` is always
`accept`, so a reject shows up only on `tool_decision`.

Run a non-interactive session against a loopback OTLP logs sink with tool details
on, in an isolated throwaway workspace:

```bash
# Project .claude/settings.json in the throwaway workspace (project settings
# override the user's ~/.claude env, so telemetry goes to the local sink, and
# the deny hooks are honoured even when the workspace is untrusted):
#   env: {
#     CLAUDE_CODE_ENABLE_TELEMETRY:"1", OTEL_LOGS_EXPORTER:"otlp",
#     OTEL_EXPORTER_OTLP_PROTOCOL:"http/json",
#     OTEL_EXPORTER_OTLP_ENDPOINT:"http://127.0.0.1:4318",
#     OTEL_LOG_TOOL_DETAILS:"1"
#   }
#   permissions.allow: ["Read"]            # → accept, source:config
#   hooks.PreToolUse: deny Bash and an MCP tool  # → reject, source:hook
claude -p '<prompt that reads a file, runs a bash command, and calls an MCP tool>'
```

Notes learned from the live capture (2.1.270):

- A config **allow** yields `decision:accept`, `source:config`. A config **deny**
  removes the tool entirely and emits *no* decision event — use a `PreToolUse`
  deny **hook** (`permissionDecision:"deny"`) to get `decision:reject`,
  `source:hook`. Deny hooks are honoured even in an untrusted workspace, whereas
  `permissions.allow` entries are ignored there.
- `tool_source` is `builtin` or `mcp`. The MCP server/tool names live *only* inside
  the gated `tool_parameters` attribute (present under `OTEL_LOG_TOOL_DETAILS=1`),
  which `NormalizeLogs` drops — so do **not** reproduce real `tool_parameters` in
  the reviewed wrapper. The `-otlp` fixture carries a short synthetic
  `tool_parameters` canary purely to prove the drop; the reviewed wrapper omits it.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.270-tool-decision.json`
  (reviewed wrapper: one config-allow, two hook-denials — builtin and mcp)
- `fixtures/claude/observed-sanitised/claude-code-2.1.270-tool-decision-otlp.json`
  (raw OTLP wire shape, with `prompt.id` and `tool_parameters` drop canaries)
- `fixtures/claude/expected/claude-code-2.1.270-tool-decision.events.json`
  (golden canonical approval events)

## Prompt/response & raw API body content capture

To raise Prompt/response content above `unsupported` (#94), capture the four
content-bearing log events with content logging **on**, using only **synthetic
prompts** in an isolated throwaway workspace against a loopback OTLP sink:

```bash
# Project .claude/settings.json env in the throwaway workspace:
#   env: {
#     CLAUDE_CODE_ENABLE_TELEMETRY:"1", OTEL_LOGS_EXPORTER:"otlp",
#     OTEL_EXPORTER_OTLP_PROTOCOL:"http/json",
#     OTEL_EXPORTER_OTLP_ENDPOINT:"http://127.0.0.1:4318",
#     OTEL_LOG_USER_PROMPTS:"1",        # → user_prompt carries `prompt`
#     OTEL_LOG_ASSISTANT_RESPONSES:"1", # → assistant_response carries `response`
#     OTEL_LOG_RAW_API_BODIES:"1",      # → api_request_body/api_response_body
#     OTEL_LOG_TOOL_DETAILS:"1"         # → real command_name on user_prompt
#   }
claude -p '<a synthetic, secret-free prompt, e.g. a /slash command probe>'
```

Notes for the reviewed wrapper:

- Capture **two shapes** so the length-without-content contract is proven: a
  content-present `user_prompt` (gate on) and a length-only one (gate off, `prompt`
  key absent — never fabricated). For `assistant_response` the default is the
  literal `"<REDACTED>"` sentinel, captured as received.
- `api_request_body`/`api_response_body` have **no** length-only shape — the events
  are simply absent when `OTEL_LOG_RAW_API_BODIES` is off. Capture an inline `body`
  and a `file:<dir>`-mode `body_ref` pointer to prove both ride raw.
- Keep every prompt/response/body **synthetic**. The validator no longer prohibits
  the `prompt`/`response` field names, but its value-based secret/entropy scan still
  runs — so a real credential would still (correctly) be rejected. The `-otlp`
  fixtures carry `prompt.id`/`message.uuid` drop canaries to prove the bare
  correlation identifiers never reach canonical output (that mapping is #106's job).
- Content is captured raw with no ingest-time re-redaction (epic #87); the per-field
  visibility decision is deferred to a later policy.

Committed evidence:

- `fixtures/claude/observed-sanitised/claude-code-2.1.270-user-prompt.json` +
  `…-user-prompt-otlp.json` (content-present + length-only)
- `fixtures/claude/observed-sanitised/claude-code-2.1.270-assistant-response.json` +
  `…-assistant-response-otlp.json` (raw response + `"<REDACTED>"` sentinel)
- `fixtures/claude/observed-sanitised/claude-code-2.1.270-api-bodies.json` +
  `…-api-bodies-otlp.json` (inline `body` + `body_ref`, request and response)
- `fixtures/claude/expected/claude-code-2.1.270-user-prompt.events.json`,
  `…-assistant-response.events.json`, `…-api-bodies.events.json` (golden events)

## Validation

The validator rejects missing origin or tool-version metadata, prohibited field
names (credential/path/command names — **not** `prompt`/`response`, relaxed for
#94), known credential patterns, private-key markers, and high-entropy secret-like
strings. Errors contain only field paths, never values.

## Review checklist

- Confirm source code, paths, command arguments, account identifiers, emails, and
  credentials are absent. Prompt/response/body content **may** be present for #94
  content fixtures — but only if it is synthetic and secret-free (the value-based
  secret/entropy scan still applies).
- Confirm remaining values are synthetic or structurally necessary telemetry
  metadata.
- Confirm unknown fields have not been silently discarded.
- Run `go test ./internal/fixture` and the repository security scan before
  commit.
