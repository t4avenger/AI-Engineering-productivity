# Codex Fixture Capture Procedure

Task 006 defines the supported process for adding a Codex telemetry fixture. Fixtures are sensitive even when prompt and response capture is disabled.

## Capture rules

1. Use an isolated local test repository with synthetic prompts, source files, commands, and credentials only.
2. Enable only the minimum supported local Codex telemetry export needed for the experiment. Never enable prompt, response, or source-code capture.
3. Record the Codex tool version and export format before copying an event into a temporary local file outside this repository.
4. Remove prohibited fields and replace file paths, identifiers, and command arguments with synthetic values. Preserve unknown field names and structural shape where safe.
5. Add fixture metadata: fixture_version, fixture_origin, provider, tool, tool_version, RFC3339 captured_at, and sanitisation_reviewed true.
6. Run the validator and a second human review before staging the file. Never commit the original capture.

## Validation

The validator rejects missing origin or tool-version metadata, prohibited field names, known credential patterns, private-key markers, and high-entropy secret-like strings. Errors contain only field paths, never values.

The checked-in fixture is deliberately synthetic. It documents the wrapper format but is not evidence of a real Codex capability. A real observed fixture must use fixture_origin observed-sanitised and include the tool version observed during capture.

## Bounded live receiver test

The local receiver accepts Codex OTLP logs at `http://127.0.0.1:8080/v1/logs`. Use `test-harness/codex-otel-config.toml` only in an isolated temporary Codex home with a synthetic repository. It keeps prompt logging disabled and selects the JSON protocol required by the current receiver.

Start the receiver, copy the template to the temporary home as `config.toml`, then run one synthetic Codex session with that home. Confirm the accepted ingest counter increases. Stop Codex to flush its asynchronous exporter, delete the temporary home, and sanitise any locally captured observation before attempting to create an `observed-sanitised` fixture. Never use a normal working repository or normal Codex configuration for this test.

## Review checklist

- Confirm prompts, responses, source code, paths, command arguments, account identifiers, and credentials are absent.
- Confirm remaining values are synthetic or structurally necessary telemetry metadata.
- Confirm unknown fields have not been silently discarded.
- Run go test ./internal/fixture and the repository security scan before commit.
