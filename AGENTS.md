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

## Duplication and SonarCloud (hard rule)

SonarCloud fails the PR quality gate when **Duplication on New Code > 3%**.
This repo has repeatedly burned CI on copy-pasted helpers, OTLP payload
builders, assertion blocks, and near-identical test cases. Treat duplication
as a merge blocker equal to a failing unit test.

**Before writing new code or tests:**

1. Search for an existing helper, fixture builder, assertion, or table-driven
   case that already expresses the same behaviour (`rg`, sibling `*_test.go`,
   `web/tests/e2e/live-ingest-helpers.ts`, nearby packages).
2. Prefer extending a shared helper over pasting a second copy “just for this
   issue.” Cross-file copy of ≥ ~10 consecutive similar lines is almost always
   wrong.
3. When two specs need the same UI assertion (nav, unlock, empty states), put
   it in one exported helper and call it from both — do not re-inline the
   block in the second file to “consolidate coverage.”
4. Table-driven / parameterized tests beat N near-duplicate `t.Run` bodies or
   Playwright `test(...)` blocks that differ only by literals.
5. After editing, re-read the diff specifically for CPD risk: if Sonar would
   see the same token sequence in another file (especially another new or
   recently touched file), extract before pushing.

**Do not:**

- Duplicate a block from `health.spec.ts` (or any sibling) into a `z-live-*`
  spec to satisfy an issue DoD — share a helper instead.
- “Fix” Sonar by renaming locals, reordering lines, or adding noise comments.
  That is suppression theatre; extract or parameterize.
- Copy OTLP JSON / protobuf shapes between adapters or e2e helpers when a
  builder already exists.

**When Sonar reports duplication on a PR you authored:** stop feature work,
deduplicate first, and only then continue. Cite the shared helper in the PR
body if the change was primarily a CPD fix.

## Privacy invariants

- Raw capture is the default (epic #87, PRODUCT_MAP §11.3). Prompts, responses,
  source content, file paths, and raw command lines are captured verbatim at the
  ingest boundary — nothing is dropped, tokenised, hashed, or disguised. A
  normaliser must not drop or reduce a provider field citing an earlier adapter's
  choice or a "not persisted by default" default; those defaults are superseded
  by this stance.
- What (if anything) to hide is a downstream visibility policy layered *over* the
  retained raw data, deferred to a later policy stage or customer/enterprise
  configuration — never an ingest-time amputation. Governance classifiers
  (`privacy.ClassifyPath` / `privacy.ClassifyCommandAccess`) run over the raw
  stored value to add a class + boundary as an additional signal, not a
  replacement.
- Sensitive values must never be logged to diagnostics or stderr (distinct from
  the captured governance record).
- Diagnostic exports must be sanitised.
- Tests use synthetic data only (git hygiene — no real credentials in history);
  field names and shapes are captured and asserted raw.
- Local APIs bind to loopback by default.
- Unknown values must not be represented as zero.
- Provider-native session/conversation/request IDs, file paths, and command
  lines are retained and displayed raw in the local-only edition with a stable
  provider prefix for namespacing; account identifiers and email remain dropped
  at the wire boundary in this edition.
- Cloud, team, or cross-device sharing is a separate trust boundary and must
  re-evaluate every field before any upload or aggregation; the no-hiding stance
  above applies to the local-only single-user edition.

Any proposed change that weakens these invariants must be rejected unless the product specification is explicitly revised. This section reflects the epic #87 revision.

## Provider integration rules

- Do not invent telemetry fields.
- Record provider and tool versions when available.
- Preserve unknown fields in provider extensions.
- Use capability states: supported, partial, unsupported, unknown, version-dependent.
- Build adapters behind a common interface.
- Add raw sanitised fixtures and expected canonical fixtures.
- Add deterministic golden tests.
- Treat provider documentation as guidance; verify against observed fixtures.
- Do not mark a capability `unavailable` / `unsupported`, or stamp `skill_detection: unavailable`, merely because capture or parsing is hard. `unknown` means not yet proven from committed fixtures; `unavailable` means reviewed fixtures prove the supported telemetry surface cannot carry the signal. Harder sources (skill-using sessions, session JSONL, OTLP metrics) must be captured or explicitly scheduled — not skipped by downgrading the cell.

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

When adding or changing a provider adapter or insight that surfaces retained
telemetry, satisfy the live-data DoD in `QUALITY_GATES.md` (daemon
ingest→read gate plus at least one non-mocked Playwright e2e).

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
