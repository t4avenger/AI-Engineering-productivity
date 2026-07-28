# Codex Normaliser

Task 007 supports one deliberately narrow fixture shape: a reviewed, sanitised
Codex OTLP trace wrapper with payload.resourceSpans[].scopeSpans[].spans[].
Each span becomes one canonical event. The normaliser is deterministic: its
event ID is codex:<traceId>:<spanId>, its session ID is codex:<traceId>, and
it uses the OTLP startTimeUnixNano plus the fixture captured_at timestamp.

The synthetic fixture is not evidence of any real Codex field beyond the shape
it contains. Model, token, cache, tool-call, file-operation, command,
approval, content, repository, task-outcome, and provider-cost fields are
therefore listed in attributes.unavailable_fields. Actor and device IDs use
the explicit string unavailable; unknown values are never represented as zero
values.

Safe fields outside the supported OTLP mapping are preserved verbatim under
provider_extensions.resource, provider_extensions.scope, or
provider_extensions.span. The fixture validator runs before normalisation, so
prohibited field names and likely secrets are rejected without exposing their
values. The normaliser neither logs nor persists fixture data. Task 008 must
apply the privacy pipeline before canonical events cross a storage or
diagnostics boundary.

fixtures/codex/expected/fixture-001.canonical.json is the golden output for
the checked-in synthetic input. Adding a real observed fixture requires the
Task 006 capture procedure, a recorded tool version, an expected canonical

## Observed log support

Codex CLI 0.145.0 was observed exporting OTLP JSON logs with `service.name`
`codex_cli_rs`. The log adapter retains only reviewed operational attributes
(`event.name`, `model`, `input_token_count`, and `output_token_count`) plus
sanitised provider extensions. Since the observed logs lack trace correlation
and a safe retained session identifier, each retained log forms an explicitly
`unknown` lifecycle session identified by an installation-specific HMAC
fingerprint. Account, hostname, email, conversation ID, raw command arguments, tool output, and body fields are
removed by the privacy pipeline before storage.
fixture, and a review of any newly evidenced capabilities.

The static-analysis gate enforces cyclomatic complexity of 15 or lower for
each Go function. The normaliser separates fixture, resource, scope, and span
handling so that nesting does not conceal excessive decision complexity.
