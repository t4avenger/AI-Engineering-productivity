# ADR 0002: Go HTML templates and HTMX for the local dashboard

## Status

Accepted — 2026-09-06

Supersedes [ADR 0001](0001-mantine-local-dashboard.md) for the local dashboard presentation layer.

## Context

The React/Vite/Mantine dashboard (ADR 0001) grew into a single large client component with no real URLs, mixed design systems, and a dual-process local setup (daemon + Node SPA). The product goal is a local-first tool that helps developers audit and educate themselves on orchestration-tool usage. A same-origin server-rendered UI matches that goal with less surface area.

BMAD UX artifacts in `docs/bmad/ux-design.md` and `docs/bmad/user-flow.md` define the information architecture and flows for this change.

## Decision

Serve the local dashboard from the TelemetryIQ Go daemon using `html/template` and HTMX:

1. HTML routes and static assets (CSS, vendored HTMX) are served same-origin with the JSON API and OTLP intake.
2. Browser authentication uses an httpOnly session cookie set by `POST /unlock` after validating the local management token. JSON `/api/v1/*` management endpoints continue to accept `Authorization: Bearer` for tests and machine clients; they also accept the session cookie.
3. Templates reuse domain data from existing session and insight readers; business logic is not reimplemented in the UI layer.
4. The React/Vite/Mantine application under `web/` is removed; Playwright e2e targets the daemon origin.
5. PRODUCT_MAP web-stack choices are updated to match this ADR.

## Consequences

- Local usage is a single process (`make run-daemon` / `make run`).
- Frontend quality gates shift from Node/Vitest/ESLint to Go UI handler tests plus Playwright.
- CORS allowances for Vite origins are no longer required for normal dashboard use.
- Accessibility and live-data DoD requirements remain; implementations change to server-rendered markup.
- ADR 0001 remains historical context; new work follows ADR 0002.
