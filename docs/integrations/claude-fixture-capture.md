# Claude Code Fixture Capture Procedure

Defines the supported process for adding a Claude Code telemetry fixture.
Fixtures are sensitive even when prompt and response capture is disabled.

## Capture rules

1. Use an isolated local test repository with synthetic prompts, skills, source
   files, commands, and credentials only.
2. Enable only the minimum supported local Claude Code telemetry export needed
   for the experiment. Never enable prompt, response, or source-code capture
   unless the experiment explicitly requires a gated attribute (for example
   `OTEL_LOG_TOOL_DETAILS=1` to retain custom skill names).
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

## Validation

The validator rejects missing origin or tool-version metadata, prohibited field
names, known credential patterns, private-key markers, and high-entropy
secret-like strings. Errors contain only field paths, never values.

## Review checklist

- Confirm prompts, responses, source code, paths, command arguments, account
  identifiers, emails, and credentials are absent.
- Confirm remaining values are synthetic or structurally necessary telemetry
  metadata.
- Confirm unknown fields have not been silently discarded.
- Run `go test ./internal/fixture` and the repository security scan before
  commit.
