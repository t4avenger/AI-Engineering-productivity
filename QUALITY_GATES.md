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

## Pull-request gate

A change may merge only when:

- required CI jobs pass
- branch is up to date under the repository merge policy
- acceptance criteria are mapped in the PR description
- tests accompany behavioural changes
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
