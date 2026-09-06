# Architecture: model-performance scorecard

## Drivers

- Evidence before scoring (PRODUCT_MAP §0)
- Outcome contracts ≠ session state (PRODUCT_MAP §13.9)
- Privacy: redaction before persist; no content/args

## Components

```text
OTLP logs (Claude/Codex)
    → privacy sanitiser
    → provider normaliser (stamp provider_extensions.outcome_contract)
    → SQLite event store
    → insights.ModelPerformanceFromEvents
    → GET /api/v1/insights/model-performance
    → /insights HTML section
```

## Outcome contract shape

Stored under `provider_extensions.outcome_contract`:

- `source`: `provider_completion` | `tool_result`
- `status`: `success` | `failed` | `abandoned`
- `error_code`: string or omitted
- `confidence`: `observed`
- `model`: string when observed (required for scorecard row attribution)
- optional: `duration_ms`, token counts, `retry_attempt`

Claude `api_request` / `api_error` and Codex `codex.api_request` /
`codex.tool_result` stamp this object. Cursor stream-json is fixture-only until
P5 ingest.

## Aggregation rules

- Group by `model` from the contract (skip contracts without model — do not
  invent `"unknown"` ranked peers).
- `sample_size` = number of contracts for that model.
- Retry rate = contracts with `retry_attempt > 0` / sample_size when any retry
  signal observed for that model; else `retry_rate_state = unavailable`.
- Tokens-per-completed-task: mean of (input+output) over **success** contracts
  that carry tokens; null when none.
- Latency p50/p95 from `duration_ms` on contracts that carry it.
- `ranking_available` true only if every model row has `sample_size >= 10`
  and there are at least two models; else `ranked_models` is empty with caveat.

## Ranking guard constant

`MinRankingSampleSize = 10`
