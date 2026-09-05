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

Every supported ingestion path decodes raw provider payloads into a generic map and runs this pipeline before normalisation, storage, diagnostics, or logging. The persistent Codex OTLP log path normalises only the sanitised payload; SQLite applies the same sanitizer again before committing canonical events as a defense-in-depth storage boundary.

Canary tests seed synthetic secrets into OTLP resource attributes, log attributes, log bodies, command/tool argument fields, and provider-extension-like metadata. They then scan derived sessions, event timelines, stored provenance, and the development inspector response to assert prohibited values do not cross either the persistence or diagnostics boundary.
