# Gate-check: model-performance scorecard (#29)

## Decision

**PASS — ready to implement** against PRODUCT_MAP §13.9, privacy invariants, and
QUALITY_GATES live-data DoD, given committed outcome-contract fixtures.

## Coverage

| Requirement | Status |
| --- | --- |
| Outcome contracts not session state | PASS — sources listed in tech-spec |
| Per-model metrics + sample size | PASS — architecture aggregation rules |
| Ranking guard sample_size &lt; 10 | PASS — MinRankingSampleSize = 10 |
| Privacy / no content | PASS — fixtures sanitised; args stripped |
| Capability matrix update | PASS — Task outcome → partial with evidence |
| Live ingest + Playwright DoD | PASS — planned for Claude/Codex OTLP |
| Cursor full adapter | N/A — P5; fixture-only matrix raise |

## Gaps accepted for this ticket

- Abandoned / PR / revert contracts not observed → honest zeros / absent sources
- Claude live success OTLP re-capture blocked by account rate limit; success
  contract shape taken from reviewed `api_request` observations already proven
  in prior fixtures and restated in dedicated outcome fixtures
- Cursor ingest deferred; stream-json fixture documents model+completion only

## Sign-off

Implementation proceeds on branch `feature/29-model-performance-scorecard` per
user directive to execute the approved plan.
