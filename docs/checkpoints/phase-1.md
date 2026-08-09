# Phase 1 Checkpoint: canonical ingestion

Status: complete

## Scope and evidence

- Canonical schemas, a sanitised Codex adapter, fixture validation, and golden
  normalisation tests are present.
- SQLite persistence sanitises every event before writing its event JSON and
  provenance, deduplicates event IDs, rebuilds sessions in timestamp order,
  and uses migration version 1.
- Tests replay fixtures, cover duplicate and out-of-order events, prove
  prohibited synthetic fields do not reach storage, and verify database-level
  session deletion.

## Decision

The Phase 1 exit criteria are met. Current command evidence is recorded with the Phase 2 completion verification: `make verify-push` passed on 2026-08-09, including the relevant ingestion, privacy, fuzz-smoke, coverage, static-analysis, security, and build checks.
