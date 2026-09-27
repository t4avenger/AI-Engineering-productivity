# Phase 4 checkpoint: local governance reporting and reference-aligned UI

Status: **GO WITH CONDITIONS** for the #195 local UI acceptance subphase.
This is not a declaration that the wider PRODUCT_MAP Phase 4 milestone or any
enterprise capability is complete.

## Scope and demonstrated journeys

- The five primary destinations render from the loopback daemon with a dark,
  responsive shell. Deterministic evidence is in
  [UI evidence #195](../ui/evidence/195/README.md).
- The Session Trace renders retained conversation, tools/files/spans, inspector,
  breakdown, legend, explicit unavailable states and a chronological fallback.
- Governance performs local detect-and-report configuration and findings for
  MCP, skills, paths and prompt rules; it does not publish, enforce, approve or
  block.
- Category tabs now support Arrow, Home and End keyboard navigation with focus
  restored after the destination reloads. Narrow governance grids and tables
  remain contained; 200% browser scaling is covered by Playwright.

## Acceptance, privacy and accessibility

- S01–S04, N01–N03, T01–T10 and G01–G08 are recorded complete in the UI
  roadmap. The evidence README records every intentional source-image
  difference by requirement ID.
- The browser journey performs non-mocked OTLP ingest, reader projection and UI
  rendering using synthetic wire-shaped data. It asserts focus navigation,
  reduced motion, token contrast, no page-wide overflow and all target
  viewports.
- Raw synthetic values are retained under PRODUCT_MAP §11.3. They remain
  synthetic and are not written to diagnostics or exports; no real credentials
  appear in fixtures, screenshots or logs.

## Verification evidence

| Check | Result |
|---|---|
| `make format-check`, `make lint`, `make static-analysis` | PASS |
| `make test-unit`, `make test-component`, `make test-integration`, `make test-contract` | PASS |
| `make test-e2e` | PASS — 36 Chromium tests, including #195 live visual evidence |
| `make test-race`, `make test-fuzz-smoke` | PASS |
| `make test-performance-smoke` | PASS — health endpoint 13 ms |
| `make coverage` | PASS — total 84.8%, above the 80% threshold |
| `make security-scan` | PASS — Gitleaks, npm audit, OSV, Semgrep and Trivy reported no findings |
| `make build` | PASS |
| `make verify-push` | PASS |

## Deferred scope and decision

- E01–E04 remain separate architecture gates: enforcement/approvals, policy
  lifecycle/audit, environment/team/sharing and GitHub synchronisation. The
  screenshots intentionally do not mimic those controls as working features.
- #118 remains Backlog because captured Codex telemetry does not yet prove both
  the effective pricing tier and request-level attribution needed for cost
  attribution.

**Decision:** accept the local reference-aligned UI subphase. Do not use this
checkpoint to enable gated enterprise behaviour.
