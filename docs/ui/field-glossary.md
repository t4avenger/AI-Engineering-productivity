# UI Field Glossary

This glossary is a presentation map for dashboard templates and helpers. The
semantic source of truth remains `PRODUCT_MAP.md` section 13 and the implemented
insight notes; this document does not define new metrics or calculation rules.

## Display Rules

| Value type | Display convention | Unavailable convention |
| --- | --- | --- |
| Availability and detection states | Use the plain labels in the enum table. Keep the machine value in a `title` attribute only when useful for debugging. | Render an explicit labelled cell or badge; never render blank, zero, or "healthy". |
| Ratios | Percent with context, for example `75% cached`. | Show the state label, for example `Not available from this provider`. |
| Growth factors | One decimal place plus multiplication sign, for example `2.0×`. | Show the state label. |
| Latency | Whole milliseconds, for example `1234 ms`. | Show the state label. |
| Retry rate | Percent of outcome contracts with retry evidence, for example `20% retried`. | Show `Not available from this provider` when retry linkage is absent. |
| Token counts | Whole numbers with `tokens`, for example `1200 tokens`. Timeline columns should read `Input tokens` and `Output tokens`, not only `In` and `Out`. | Show the state label; unknown token values are not zero. |
| Currency and cost | Currency code plus formatted estimate when calculated. | Show `Unavailable` or the cost status label; unknown prices are not `$0`. |

## Recommended UI Titles

| Concept | Current / machine wording | Recommended UI title | Reason |
| --- | --- | --- | --- |
| MCP connected but not invoked | `context_waste_state=connected_but_unused` | Unused connection | Distinguishes unused MCP configuration from token context growth. |
| Token/cache context pressure | Context waste insight, `cached_context_ratio`, `input_token_growth` | Context pressure | Avoids colliding with the MCP unused-connection column while preserving the section 13.10 meaning. |

If later UI work keeps the section title "Context waste", the MCP table column
must still avoid that exact title and should use "Unused connection".

## Status Badge Primitive

Templates render availability and detection enums through the shared
`status-badge` partial (`internal/ui/templates/partials.html`), backed by the
`statusLabel` and `statusClass` helpers in `internal/ui/ui.go`. `statusLabel`
maps each machine value to the plain label defined in the Enum Label Map below;
`statusClass` selects the badge CSS in `internal/ui/static/app.css`, where the
label is always paired with a leading glyph so state is never conveyed by colour
alone. Use `{{template "status-badge" $state}}` rather than printing raw enum
strings.

## Enum Label Map

| Machine value | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `observed` | Seen in telemetry | The signal was present in retained canonical telemetry or provider evidence. | None | Not applicable. |
| `partial` | Partially seen | Some, but not all, expected parts of the signal are available for this provider/tool. | None | Render the available detail and label missing parts explicitly. |
| `unavailable` | Not available from this provider | Reviewed telemetry shows the supported provider surface cannot emit this signal, or the current row has no value for an optional signal. | None | Use this label; do not infer or substitute zero. |
| `unsupported` | Not supported | The product or provider capability matrix says this signal is outside the supported surface. | None | Use this label and avoid implying the feature exists. |
| `unknown` | Not proven yet | No committed fixture or retained event proves whether the signal is available. | None | Use this label; schedule or reference fixture work rather than downgrading to unavailable. |
| `not_observed` | Not seen in telemetry | MCP invocation evidence was not seen for a known connected server. | None | If invocation telemetry itself is absent, use `usage_unavailable` instead. |
| `connected_but_unused` | Connected, never invoked | A server connection was observed and no matching explicit invocation was observed. | None | If usage cannot be checked, use `usage_unavailable`. |
| `used` | Invoked | Explicit MCP invocation evidence matched the server. | None | Not applicable. |
| `usage_unavailable` | Usage not available | The provider data does not prove whether the connected MCP server was invoked. | None | Use this label instead of "unused". |
| `fingerprinted` | Fingerprint only | The server identity is represented by a privacy-safe fingerprint because a provider name was unavailable. | None | Prefer `server_name` when present; fall back to the fingerprint label only when needed. |
| `explicit` | Explicitly identified | The provider stamped the skill identity directly. | None | Not applicable. |
| `inferred` | Inferred by provider | The provider marked skill detection as inferred, but TelemetryIQ must not create named skill records from it. | None | Show in coverage only. |
| `success` | Succeeded | An outcome contract reported successful completion. | Count | If no outcome contract exists, do not borrow session lifecycle state. |
| `failed` | Failed | An outcome contract reported a failed completion. | Count | If no outcome contract exists, do not borrow session lifecycle state. |
| `abandoned` | Abandoned | An outcome contract reported an abandoned task. | Count | Keep unobserved abandoned contracts absent, not zero unless within an observed sample. |
| `calculated` | Calculated estimate | Pricing data was available and an amount was calculated. | Currency / micro-USD internally | Not applicable. |
| `unknown_price` | Price unknown | The usage was retained but no matching price was available. | Count/status | Show unknown price, never `$0`. |
| `not_calculable` | Not calculable | Required usage or currency inputs were missing for cost calculation. | Count/status | Show not calculable, never `$0`. |

## MCP Inventory Fields

| Machine field | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `connected_servers` | Connected servers | Count of MCP servers observed from reviewed telemetry. | Servers | Show `0 servers` only when telemetry was read and no server rows exist; otherwise show an explicit unavailable/awaiting state. |
| `used_servers` | Invoked servers | Count of connected MCP servers with explicit matching invocation evidence. | Servers | Do not infer use from connection alone. |
| `unused_servers` | Unused connections | Count of connected MCP servers with `connected_but_unused` state. | Servers | Use `usage_unavailable_servers` when usage cannot be checked. |
| `usage_unavailable_servers` | Usage unavailable | Count of server rows whose invocation state cannot be proven. | Servers | This is the honest fallback for blind provider surfaces. |
| `server_name` | Server | Provider-reported MCP server name. | None | If absent, render the privacy-safe fingerprint as a fallback. |
| `server_fingerprint` | Server fingerprint | Privacy-safe correlation key for a server. | None | Use only as fallback display text or secondary detail when `server_name` is empty. |
| `identity_state` | Identity evidence | Whether display identity came from provider data or only a fingerprint. | Enum | Show `Fingerprint only` when the name is unavailable. |
| `usage_state` | Usage | Whether explicit invocation evidence was seen. | Enum | Show `Usage not available` when the telemetry cannot carry invocation evidence. |
| `context_waste_state` | Unused connection | MCP-specific unused-connection state; not the token context-pressure insight. | Enum | Show `Usage not available` when usage is blind. |
| `invocation_count` | Invocations | Count of explicit MCP invocation events matched to the server. | Invocations | Show `0 invocations` only for `connected_but_unused`; otherwise show usage unavailable. |
| `tool_names` | MCP tools | Provider-reported MCP tool names invoked through this server. | Names | Omit or show unavailable when no invocation evidence exists. |
| `request_input_tokens` | Request input tokens | Input tokens on requests where MCP telemetry was present. | Tokens | Show unavailable when token context was not observed. |
| `request_output_tokens` | Request output tokens | Output tokens on requests where MCP telemetry was present. | Tokens | Show unavailable when token context was not observed. |
| `request_cached_input_tokens` | Request cached input tokens | Cached input tokens on requests where MCP telemetry was present. | Tokens | Show unavailable when cache token context was not observed. |
| `request_cache_created_tokens` | Request cache-created tokens | Cache-created tokens on requests where MCP telemetry was present. | Tokens | Show unavailable when cache-created token context was not observed. |
| `token_context_label` | Token context note | Label explaining that token context is session/request-level and not exact per-MCP allocation. | None | Always show near token context fields. |

## Skill Usage Fields

| Machine field | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `observed_skills` | Observed skills | Distinct skills with provider-stamped explicit identity and at least one invocation. | Skills | A zero value means no explicit skill rows; coverage still explains whether detection was possible. |
| `invocations` | Skill invocations | Count of explicit skill invocation events. | Invocations | Do not infer from tool names or session state. |
| `explicit_detection` | Explicit surfaces | Count of provider/tool surfaces with explicit skill identity evidence. | Surfaces | Not applicable. |
| `inferred_detection` | Inferred surfaces | Count of surfaces where the provider marked detection as inferred. | Surfaces | Show in coverage; do not create named skill rows. |
| `unavailable_detection` | Unavailable surfaces | Count of surfaces proven unable to expose skill identity. | Surfaces | Show as unavailable, not as zero skills used. |
| `unknown_detection` | Unknown surfaces | Count of surfaces with no skill detection metadata yet. | Surfaces | Show as not proven yet; do not mark unavailable unless fixtures prove it. |
| `skill_name` | Skill | Provider-stamped skill name. | Name | No row is emitted without an explicit name. |
| `detection_state` | Detection | Skill-detection state for a row or provider/tool surface. | Enum | Render the enum label. |
| `outcomes` | Skill outcomes | Outcome counts reported by the skill payload itself. | Count by outcome | Do not borrow session or task outcome. |
| `outcome_state` | Skill outcome evidence | Whether skill-specific outcome data was observed. | Enum | Show `Not available from this provider` when no skill outcome field exists. |

## Model Performance Fields

| Machine field | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `model` | Model | Provider model identifier from an outcome contract. | Identifier | Rows without a proven model are omitted from the scorecard. |
| `sample_size` | Sample size | Number of outcome contracts for the model. | Contracts | Ranking is unavailable unless every compared model has at least 10. |
| `success` | Succeeded | Count of successful outcome contracts. | Contracts | Count only within observed sample size. |
| `failed` | Failed | Count of failed outcome contracts. | Contracts | Count only within observed sample size. |
| `abandoned` | Abandoned | Count of abandoned outcome contracts. | Contracts | Do not fabricate from session lifecycle. |
| `retry_rate` | Retry rate | Share of outcome contracts with retry evidence. | Percent | Show retry-rate state when no retry linkage is observed. |
| `retry_rate_state` | Retry evidence | Whether retry linkage was observed. | Enum | Show `Not available from this provider` when absent. |
| `error_codes` | Error codes | Histogram of provider error codes from outcome contracts. | Count by code | Empty means no error codes inside observed contracts. |
| `tokens_per_completed_task` | Tokens per completed task | Mean input plus output tokens for successful outcome contracts carrying token data. | Tokens/task | Show unavailable when token data is missing. |
| `latency_p50_ms` | Latency p50 | Median contract duration. | Milliseconds | Show unavailable when duration is missing. |
| `latency_p95_ms` | Latency p95 | 95th percentile contract duration. | Milliseconds | Show unavailable when duration is missing. |
| `ranking_available` | Ranking available | Whether cross-model ranking passed the minimum sample-size guard. | Boolean | Show "No, sample size below 10" or equivalent; do not rank. |
| `ranked_models` | Ranked models | Ordered model list when ranking is allowed. | Model identifiers | Hide or show unavailable when `ranking_available=false`. |
| `outcome_contract_sources` | Evidence sources | Provider contract sources used for the scorecard row. | Source labels | Show unavailable if no row exists. |

## Context Pressure Fields

| Machine field | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `cached_context_ratio` | Cached context | Sum of cached input tokens divided by sum of input tokens for events where both values are observed. | Percent | Show `Not available from this provider` when overlapping token values are absent. |
| `cached_context_ratio_state` | Cached context evidence | Whether cached-context ratio could be calculated. | Enum | Render the state label. |
| `input_token_growth` | Input growth | Maximum observed input tokens divided by the first observed input-token sample in the session. | Multiplier | Show unavailable when fewer than two input samples exist. |
| `input_token_growth_state` | Input growth evidence | Whether input-token growth could be calculated. | Enum | Render the state label. |
| `triggered` | Threshold triggered | Whether cached context or input growth met or exceeded the configured threshold. | Boolean | `No` means observed metrics did not trigger; if both metrics are unavailable, explain that no trigger could be evaluated. |
| `trigger_reasons` | Trigger reasons | Machine reason keys for metrics that crossed thresholds. | Enum list | Empty when no threshold was crossed. |
| `cached_context_ratio_threshold` | Cached-context threshold | Configured trigger threshold for cached context. | Percent | Show the configured value near the table. |
| `input_token_growth_threshold` | Input-growth threshold | Configured trigger threshold for input growth. | Multiplier | Show the configured value near the table. |
| `token_points` | Token points | Retained token-bearing events considered for the session. | Events | Show unavailable if no token-bearing events exist. |
| `input_token_samples` | Input samples | Token points with observed input-token values. | Samples | Show unavailable when none exist. |
| `first_input_tokens` | First input tokens | Earliest observed input-token sample in the session. | Tokens | Show unavailable when absent. |
| `max_input_tokens` | Max input tokens | Largest observed input-token sample in the session. | Tokens | Show unavailable when absent. |
| `overlap_input_tokens` | Overlap input tokens | Sum of input tokens from events that also have cached-input tokens. | Tokens | Show unavailable when no overlapping samples exist. |
| `overlap_cached_input_tokens` | Overlap cached tokens | Sum of cached-input tokens from events that also have input tokens. | Tokens | Show unavailable when no overlapping samples exist. |

## Session And Timeline Fields

| Machine field | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `session_id` | Session ID | Stable provider-prefixed local correlation key. | Identifier | Old rows may remain opaque until deleted or re-ingested. |
| `provider` | Provider | Model or tool vendor namespace, for example `openai` or `anthropic`. | Identifier | Show availability label if missing. |
| `tool` | Tool | AI coding tool name, for example `codex`, `claude-code`, or `cursor-agent`. | Identifier | Show availability label if missing. |
| `state` / `outcome` | Session state | Session lifecycle state, not a model-performance outcome contract. | Enum | `unknown` stays unknown; do not use it for scorecard outcomes. |
| `started_at` | Started | Session start timestamp. | Local time or RFC3339 in `title` | Show unavailable when missing. |
| `completed_at` | Completed | Session completion timestamp. | Local time or RFC3339 in `title` | Show unavailable for active, abandoned, or missing completion. |
| `model` | Model | Retained observed model attribute on the session or event. | Identifier | Show unavailable/unknown based on availability map. |
| `observed_events` | Observed events | Count of retained events associated with the session. | Events | Show unavailable if event count was not retained. |
| `token_usage` | Token usage | Session-level token availability summary. | Enum | `partial` means some token categories are available; do not fill missing categories with zero. |
| `event_type` | Event type | Canonical event type for a timeline entry. | Identifier / label | Humanize common event types in templates while preserving raw value as secondary detail. |
| `input_token_count` | Input tokens | Input tokens retained on an event. | Tokens | Show unavailable rather than `0` unless zero was explicitly observed. |
| `output_token_count` | Output tokens | Output tokens retained on an event. | Tokens | Show unavailable rather than `0` unless zero was explicitly observed. |
| `unavailable_fields` | Unavailable fields | Retained list of fields the provider/tool did not provide or TelemetryIQ intentionally did not retain. | Field labels | Humanize snake_case values where possible. |

## Cost Status Honesty

Cost remains secondary and optional. Cost UI must not appear on Home headline
surfaces and must never convert missing prices or missing usage into zero.

| Machine field/value | Plain label | Definition | Units | When unavailable |
| --- | --- | --- | --- | --- |
| `calculated_amount_microusd` | Calculated amount | Optional calculated estimate stored internally in micro-USD. | Currency | If nil, show unavailable; never `$0`. |
| `currency` | Currency | Currency code for calculated estimates. | ISO currency code | Show unavailable when absent. |
| `statuses` | Cost statuses | Counts of calculation statuses such as calculated or unknown price. | Count by status | Render status labels from the enum table. |
| `provider_cost` | Provider cost | Provider-native cost data, where reviewed and retained. | Currency | Current Codex log normalisation marks this unavailable when not present. |
