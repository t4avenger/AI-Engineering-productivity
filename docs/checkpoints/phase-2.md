# Phase 2 checkpoint: local API and session explorer

## Scope and demo

The reconciliation adds a chronological authenticated safe-event timeline and per-event sanitisation provenance view. Provider extensions and raw intake are never returned by these endpoints.

The local dashboard is served by the Go daemon with HTML templates and HTMX
(ADR 0002). It provides Home, Sessions, Session Detail, Insights, Integrations,
Privacy, and Costs pages on the same loopback origin as the JSON API. Home
emphasises orchestration usage (not cost). Unavailable telemetry is labelled
explicitly; integrations appear only from observed sessions; deletion requires
confirmation.

## Verification evidence

- Go/HTMX dashboard rewrite on branch `feature/go-htmx-dashboard` (2026-09-06):
  format-check, lint, static-analysis, unit, component, integration, contract,
  race, e2e (5 Chromium specs against daemon origin), coverage (≥80%), build
  passed. Security-scan: gitleaks/osv/semgrep/trivy with documented nosemgrep
  for loopback HTTP auth cookies (Secure follows TLS; expiry 2026-12-31).

Historical evidence: Phase 2 timeline reconciliation `make verify-push` passed
on 2026-08-11 (React/Mantine era, superseded by ADR 0002).

## Accessibility and privacy summary

The dashboard uses semantic landmarks, labelled navigation, heading hierarchy,
visible focus styles, a skip link, and `role="alert"` for errors. Bulk delete
requires typing `DELETE ALL`. The UI neither persists nor exposes prompts,
responses, source code, or raw command arguments; Privacy documents enforced
defaults.

## Known defects and risks

No blocking defects are known. Management JSON endpoints accept Bearer tokens;
the HTML UI uses an httpOnly session cookie after unlock.

## Decision

**GO** for Phase 2 acceptance with the Go/HTMX dashboard. Next smallest tasks
can continue behaviour/efficiency insights and provider adapters without a SPA.
