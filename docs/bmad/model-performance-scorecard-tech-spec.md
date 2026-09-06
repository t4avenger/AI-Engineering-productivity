# Model-performance scorecard technical specification (issue #29)

## Status

Approved for implementation by execution of the attached plan on
`feature/29-model-performance-scorecard` (BMAD Method). Evidence capture for
outcome contracts is complete; this document freezes the contract.

## Purpose

Answer “is the model performing?” with **raw per-model metrics and sample size**,
built only on **outcome contracts**, never raw session lifecycle state.

## Outcome-contract sources (evidence-backed)

| Source | Provider | Fixture evidence | Status mapping |
| --- | --- | --- | --- |
| `api_request` | Claude Code | `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-request-outcome.json` | success (provider completion) |
| `api_error` | Claude Code | `fixtures/claude/observed-sanitised/claude-code-2.1.263-api-error-outcome.json` | failed; `error`/`status_code` → error codes |
| `codex.api_request` | Codex | `fixtures/codex/observed-sanitised/codex-0.153.4-outcome-contracts.json` | success/failed from `success` + status; `attempt` feeds retry rate |
| `codex.tool_result` | Codex | same | success/failed from `success` + `model` (tool/operation outcome; live unittest used `exec_command`) |
| stream-json `result` + init `model` | Cursor Agent | `fixtures/cursor/observed-sanitised/cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json` | provider completion; adapter deferred (P5) — fixture raises matrix only |

Abandoned: not observed in reviewed captures → count remains 0 when no abandoned
contracts are present (legitimate zero of observed abandoned contracts).

PR result / reverted patch: not observed → not invented.

## Non-goals

- Scoring from `session.completed` / `session.failed` / `session.abandoned` alone
- Cross-model ranking when any compared model has `sample_size < 10`
- Inferring outcomes from timing alone
- Full Cursor adapter (P5)

## Functional requirements

FR-1 — Persist and surface an `outcome_contract` only when a reviewed provider
signal is present (`source`, `status` ∈ {success, failed, abandoned}, optional
`error_code`, `confidence=observed`, model when known).

FR-2 — Per model, report sample_size, success/failed/abandoned counts, retry
rate when retry linkage (`attempt` > 0) is observed else honest unavailable,
error-code histogram, tokens-per-completed-task (completed contracts only; omit
when tokens absent — never fabricate zero), latency p50/p95 from `duration_ms`.

FR-3 — Ranking / ordered comparative list is suppressed unless every ranked
model has `sample_size >= 10`. Raw rows and caveats remain available.

FR-4 — `GET /api/v1/insights/model-performance` and Insights UI section follow
the skill/MCP insight patterns.

## Non-functional requirements

NFR-1 — Privacy invariants unchanged; no prompts, responses, command arguments,
or identities retained.

NFR-2 — Metric golden tests + ranking-guard test; live ingest→read + non-mocked
Playwright e2e (QUALITY_GATES live-data DoD).

NFR-3 — Unknown values never represented as healthy zeros for unavailable rates.

## Acceptance story

**Given** sanitised events with known outcome contracts for models A and B  
**when** the model-performance insight is computed  
**then** each model row shows the expected counts, sample_size, and percentiles  
**and** ranking is omitted when sample_size &lt; 10.

## Architecture handoff

See `docs/bmad/architecture.md` and `docs/bmad/gate-check.md`.
