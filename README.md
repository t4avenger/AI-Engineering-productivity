# TelemetryIQ

Local-first AI engineering intelligence and governance. Task 001 provides only the repository scaffold, a loopback Go daemon, and a React health page. It does not ingest telemetry, use a database, or implement authentication yet.

## Requirements

- Go 1.25.12 or newer
- Node.js 18.20.8 or newer
- npm 10.x or newer
- Optional local tools: `pre-commit`, `staticcheck`, `golangci-lint`, `govulncheck`, `gitleaks`, `osv-scanner`, `semgrep`, `trivy`, `shellcheck`, `actionlint`

## Local Setup

```bash
make bootstrap
make hooks-install
```

Run the daemon and web app in separate terminals:

```bash
make run-daemon
make run-web
```

The daemon binds to `127.0.0.1:8080` by default and exposes:

```text
GET http://127.0.0.1:8080/api/v1/health
```

Configuration uses environment variables:

- `TELEMETRYIQ_HOST`, default `127.0.0.1`
- `TELEMETRYIQ_PORT`, default `8080`
- `VITE_HEALTH_URL`, default `http://127.0.0.1:8080/api/v1/health`

Authentication is intentionally not implemented in Task 001. TODO for Task 002+: introduce local-only authentication token handling without weakening loopback defaults.

## Verification Commands

- `make format`: apply Go and frontend formatting.
- `make format-check`: verify Go and frontend formatting.
- `make lint`: run `go vet` and ESLint with type-aware TypeScript rules.
- `make static-analysis`: run Go vet, optional Go static tools when installed, TypeScript checking, and `knip`.
- `make test-unit`: run Go unit tests, including the health endpoint.
- `make test-component`: run Vitest component/unit tests with coverage for loading, healthy, and unhealthy UI states.
- `make test-integration`: currently reports not applicable for Task 001.
- `make test-contract`: currently reports not applicable for Task 001.
- `make test-e2e`: run Playwright against a production-like local daemon and Vite preview.
- `make test-race`: run Go tests with the race detector.
- `make test-fuzz-smoke`: currently reports not applicable for Task 001.
- `make test-performance-smoke`: starts the daemon and checks health endpoint latency.
- `make coverage`: runs backend and frontend coverage reports.
- `make security-scan`: runs optional installed security tools and shell/script/workflow lint checks.
- `make build`: builds the daemon and frontend.
- `make precommit`: fast commit-time checks.
- `make verify`: required PR-equivalent local checks aligned to `QUALITY_GATES.md`.
- `make verify-push`: broader pre-push checks including E2E and race tests.

## Static Analysis And Security Tools

- `gofmt`: canonical Go formatting.
- `go vet`: Go correctness checks included in the standard toolchain.
- `staticcheck`: deeper Go static analysis; optional locally, installed in CI.
- `golangci-lint`: pinned Go lint aggregator; optional locally, installed in CI and pre-commit.
- `govulncheck`: Go vulnerability analysis; optional locally, installed in CI.
- `tsc --noEmit`: strict TypeScript type checking.
- `ESLint`: type-aware TypeScript and React linting.
- `Prettier`: deterministic frontend formatting.
- `knip`: unused frontend dependency and file checks.
- `Vitest`: frontend unit/component tests and coverage.
- `Playwright`: production-like browser smoke test.
- `gitleaks`: secret scanning.
- `osv-scanner`: dependency vulnerability scanning.
- `Semgrep`: security-focused static analysis.
- `Trivy`: filesystem, dependency, secret, and misconfiguration scanning.
- `ShellCheck`: shell-script linting when shell scripts exist.
- `actionlint`: GitHub Actions workflow linting.

## Current Scope

Task 001 deliberately excludes telemetry ingestion, database persistence, provider adapters, analytics, and third-party telemetry. Placeholder directories exist only to match the planned product layout where useful.
