# Phase 0 Checkpoint

Status: complete

## Scope Demo

- Codex 0.145.0 synthetic session exported OTLP logs to the loopback receiver.
- Claude Code 2.1.251 synthetic session exported OTLP HTTP/JSON logs to the loopback receiver with content logging disabled.
- Cursor Agent 2026.05.16-0338208 authenticated synthetic print-mode run returned structured local JSON usage data; local OTLP emission was not observed.
- Sanitized observed fixtures and capability findings are recorded under docs/integrations.

## Command Outputs Or CI Links

- make verify-push passed on 2026-07-27.
- make verify-push passed on 2026-08-31 for the multi-provider capability matrix update.
- Isolated live confirmation passed with sensitive OTLP attribute values absent from inspector output.

## Coverage Summary

- Backend and frontend configured thresholds passed in make verify-push.
- Web component coverage on 2026-08-31: statements 90.99%, branches 80.22%, functions 92.2%, lines 92.46%.

## Static-Analysis Summary

- Static analysis reported 0 issues.

## Security/Privacy Summary

- No leaks reported by security scan. OTLP email, account ID, hostname, conversation ID, and log body regression tests pass.
- Claude and Cursor fixtures use synthetic placeholders for session/request identifiers and do not retain prompts, responses, source code, raw paths, command arguments, or secret values.

## Performance Summary

- Not applicable: no user-facing performance path changed.

## Accessibility Summary

- Not applicable: no web UI changed.

## Known Defects

- None open for Phase 0. Remaining unknown capability cells reflect absent observed provider evidence rather than defects.

## Risks Owners

- Product owner: privacy boundary and observed fixture review complete.
- Engineering: GitHub Projects status movement still requires a token with project scope; issue status was documented with a work-start comment.

## Decision

GO: Phase 0 telemetry feasibility exit criteria are met.
