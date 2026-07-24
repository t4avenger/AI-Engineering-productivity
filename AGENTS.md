# AGENTS.md

## Mission

Build the local-first MVP described in `PRODUCT_MAP.md`.

## Source of truth

Read in this order:

1. `PRODUCT_MAP.md`
2. Relevant files in `docs/architecture/`
3. Relevant ADRs in `docs/decisions/`
4. Relevant schemas
5. Existing tests

When implementation and documentation conflict, stop and report the conflict. Do not silently choose a new product direction.

## Working method

- Complete one narrowly scoped task at a time.
- Inspect existing code before editing.
- State assumptions.
- Prefer simple, testable designs.
- Add tests before or with behaviour changes.
- Preserve backward compatibility unless the task explicitly changes a contract.
- Update documentation with code.
- Do not add speculative abstractions for future enterprise features.

## Privacy invariants

- Prompts are not persisted by default.
- Responses are not persisted by default.
- Source code is not persisted by default.
- Raw command arguments are not persisted by default.
- Redaction occurs before persistence and diagnostics.
- Sensitive values must never be logged.
- Diagnostic exports must be sanitised.
- Tests use synthetic data only.
- Local APIs bind to loopback by default.
- Unknown values must not be represented as zero.

Any proposed change that weakens these invariants must be rejected unless the product specification is explicitly revised.

## Provider integration rules

- Do not invent telemetry fields.
- Record provider and tool versions when available.
- Preserve unknown fields in provider extensions.
- Use capability states: supported, partial, unsupported, unknown, version-dependent.
- Build adapters behind a common interface.
- Add raw sanitised fixtures and expected canonical fixtures.
- Add deterministic golden tests.
- Treat provider documentation as guidance; verify against observed fixtures.

## Required verification

Read `QUALITY_GATES.md`. Before completing a task, run all applicable checks. The normal minimum is:

```bash
make format-check
make lint
make static-analysis
make test-unit
make test-component
make test-integration
make coverage
make security-scan
make build
```

When the web application changes, also run functional browser and accessibility checks:

```bash
make test-e2e
```

When concurrency or shared state changes, run:

```bash
make test-race
```

When schemas, API contracts, or migrations change, run compatibility, contract, and migration tests. When ingestion, parsing, redaction, or normalisation boundaries change, add or update fuzz tests and run at least the smoke fuzz target.

Before a phase is declared complete, create or update its checkpoint evidence and run `make verify-push` or the equivalent full CI workflow.

Never skip, disable, or weaken a required check simply to make CI pass. Any temporary suppression must have a written reason, owner, and expiry.

## Completion report

At the end of every task, report:

1. What changed.
2. Files changed.
3. Tests, static-analysis checks, and security scans run with their exact results.
4. Coverage results and threshold status.
5. Acceptance criteria met.
6. Non-functional impact and measurements where relevant.
7. Any skipped checks, suppressions, unresolved risks, or uncertainties.
8. Checkpoint status when the task completes a phase.
9. The next smallest recommended task.
