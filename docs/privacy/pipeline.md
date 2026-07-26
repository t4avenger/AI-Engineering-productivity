# Privacy Pipeline

Task 005 provides the local-only privacy boundary for TelemetryIQ. Callers must sanitise telemetry before it reaches storage, diagnostics, or logging code.

## Transformations

- Prompts, responses, source code, and recognised secret-bearing fields (for example `token` and `api_key`) are removed.
- File paths and filenames are replaced with `hmac-sha256:` hashes.
- Command arguments are replaced with `[REDACTED]`.
- Other fields are retained as operational metadata, including unknown provider-extension fields unless they are themselves sensitive.

The result includes field-level provenance (`path`, `action`, and `reason`) but never original sensitive values.

## Installation salt

`privacy.LoadOrCreateSalt(dataDir)` creates a random 32-byte HMAC salt at `dataDir/privacy-hmac-salt` and enforces directory mode `0700` and file mode `0600`. The same local installation produces stable hashes; separate installations produce different hashes. The salt must never be sent in telemetry, diagnostics, or logs.

## Current boundary

The Task 004 receiver intentionally does not retain raw payloads. Task 007 normalisation and Task 008 persistence must call this pipeline before any record is stored or exported. Tests serialize pipeline output and assert synthetic prohibited values do not cross that boundary.
