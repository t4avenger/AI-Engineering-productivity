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

The local receiver accepts Codex OTLP logs at `http://127.0.0.1:8080/v1/logs`
and skill-relevant OTLP metrics at `http://127.0.0.1:8080/v1/metrics`. Use
`test-harness/codex-otel-config.toml` only in an isolated temporary Codex home
with a synthetic repository. It keeps prompt logging disabled and selects the
JSON protocol required by the current receiver.

Start the receiver, copy the template to the temporary home as `config.toml`,
then run one synthetic Codex session with that home. Confirm the accepted ingest
counter increases. Stop Codex to flush its asynchronous exporter, delete the
temporary home, and sanitise any locally captured observation before attempting
to create an `observed-sanitised` fixture. Never use a normal working repository
or normal Codex configuration for this test.

## Skill injection capture

To raise Skill invocations above `unknown`, add a synthetic skill under the
isolated `CODEX_HOME/skills/` tree, enable both `exporter` (logs) and
`metrics_exporter` (metrics) in the harness config, then run a synthetic
`codex exec` turn that injects the skill (for example `$tiq-probe`).

The reviewed skill signal is the OTLP metric `codex.skill.injected` with
datapoint attributes `skill`, `status`, and `invoke_type`. Companion logs from
the same 0.153.4 capture did not carry skill identity on tool events; do not
infer skills from `tool_name`.

Committed evidence:

- `fixtures/codex/observed-sanitised/codex-0.153.4-skill-injected-metrics.json`

## Outcome-contract capture

Use an isolated `CODEX_HOME` with auth + harness OTEL config. Capture:

1. `codex.tool_result` with `success` and `model` (strip `arguments`/`output` before commit).
2. `codex.api_request` with `success`, status, `attempt` (retry), and `model` when present.

A live unittest run (`python3 test_add.py` via `exec_command`) produced
`tool_result success=true` with model `gpt-6-astra` on CLI 0.153.4.

Committed evidence:

- `fixtures/codex/observed-sanitised/codex-0.153.4-outcome-contracts.json`
- `fixtures/codex/observed-sanitised/codex-0.153.4-outcome-contracts-otlp.json`

## Current OTLP surface inventory

Issue #110 captured Codex CLI 0.153.4 on 2026-09-10 with the same isolated `CODEX_HOME` rules and a raw loopback OTLP HTTP/JSON receiver. The committed inventory is one reviewed fixture per observed log event and metric instrument under:

- `fixtures/codex/observed-sanitised/current-0.153.4-surface/`

The run covered `codex_exec` and a PTY-backed `codex_cli_rs` session. The nested sandbox in this environment blocked reading the temporary synthetic skill and shell execution during normal probes, so those two synthetic-only probes used Codex's explicit bypass flag inside the temporary synthetic workspace; do not use that flag outside an isolated synthetic capture workspace. No `/v1/traces` POST was observed.

## Review checklist

- Confirm prompts, responses, source code, paths, command arguments, account identifiers, and credentials are absent.
- Confirm remaining values are synthetic or structurally necessary telemetry metadata.
- Confirm unknown fields have not been silently discarded.
- Run go test ./internal/fixture and the repository security scan before commit.
