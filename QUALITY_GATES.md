# Quality Gates and Checkpoints

This document is the operational checklist for the quality requirements in `PRODUCT_MAP.md`.

## Local commands

The repository must converge on these stable commands:

```bash
make bootstrap          # install or verify development tooling
make hooks-install      # install pinned pre-commit hooks
make format             # apply safe formatting
make format-check       # verify formatting without modifying files
make lint               # language linters and type checking
make static-analysis    # SAST and deeper static checks
make test-unit
make test-component
make test-integration
make test-contract
make test-e2e
make test-race
make test-fuzz-smoke
make test-performance-smoke
make coverage
make security-scan
make build
make verify             # required PR-equivalent local checks
make verify-push        # broader pre-push checks
```

Task 001 may introduce a subset where later commands clearly report “not applicable yet”; by the relevant phase, each command must perform real checks.

## Live-data DoD for adapters and insights

Fixture-only adapter tests and mocked Playwright journeys are necessary but not
sufficient. A fully broken ingest or read path can still look green if DoD
stops at those boundaries (see issues #49 / #51).

For every **new or changed provider adapter** and every **new insight** that
claims to surface retained telemetry:

1. **Daemon live ingest→read gate** — POST a real captured (or synthetic
   wire-shaped) payload to the running receiver (`POST /v1/logs` today) and
   assert the HTTP read API serves the expected session/insight fields, with
   canary identity/secret values absent from the response. Follow the pattern
   in `internal/api/claude_ingest_test.go` and `internal/api/codex_ingest_test.go`.
2. **At least one non-mocked frontend e2e** — a Playwright spec that uses the
   daemon already started by `web/playwright.config.ts` (HTML UI on the daemon
   origin per ADR 0002), ingests live data, and asserts the UI renders that
   data (or an honest unavailable cell). Do **not** use `page.route().fulfill()`
   to mock API responses in that gate.
3. Existing mocked e2e specs may remain for fast UI coverage; they do not
   replace the live gate above.

`make test-integration` and `make test-e2e` must exercise these gates in CI.

## Pull-request gate

A change may merge only when:

- required CI jobs pass
- branch is up to date under the repository merge policy
- acceptance criteria are mapped in the PR description
- tests accompany behavioural changes
- live-data DoD items above are satisfied when the change touches adapters or insights
- no critical or high privacy/security finding is open
- coverage gates pass
- generated schemas and documentation are current
- suppressions have owner, reason, and expiry
- checkpoint evidence is updated when the change completes a phase

## Release gate

A release candidate requires:

- clean-checkout build reproduction
- supported-platform installation smoke tests
- upgrade and rollback test from the previous supported release
- full functional E2E suite
- privacy canary scan
- security scans and threat-model review status
- performance comparison against the prior release
- SBOM and checksums
- signed artifacts and provenance once release signing is enabled
- release notes, known limitations, and data-migration notes
- explicit GO/NO-GO decision

## Defect policy

- Critical: immediate NO-GO; fix before merge or release.
- High: blocking unless formally reclassified with security/privacy owner approval.
- Medium: owner and target milestone required.
- Low: may be accepted with rationale.

Privacy leakage, data corruption, unsafe default exposure, cross-tenant access, secret disclosure, or unreproducible release artifacts are always release-blocking.

## Checkpoint evidence

For each phase create `docs/checkpoints/phase-N.md` containing:

- scope and demo
- command outputs or CI links
- coverage summary
- static-analysis summary
- security/privacy summary
- performance summary
- accessibility summary
- known defects
- risks and owners
- GO, GO WITH CONDITIONS, or NO-GO
