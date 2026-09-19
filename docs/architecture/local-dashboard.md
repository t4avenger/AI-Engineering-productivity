# Local dashboard

The local dashboard is served by the TelemetryIQ daemon using Go `html/template`
and HTMX (ADR 0002). It provides Home, Sessions, Session Detail, Governance,
Integrations, Insights, Models, Pull Requests, Privacy, and Costs pages over the
same loopback origin as the JSON API and OTLP intake. It does not send analytics
or dashboard data to third parties.

Primary navigation is Overview, Sessions, Pull Requests, Models, and Governance
in a persistent dark sidebar (#161 / ADR 0003). Integrations, Insights, Privacy,
and Costs remain utility destinations; Costs is omitted from Overview because
PRODUCT_MAP section 17.1 forbids cost labels and links there. The `/models`
destination (#186) reuses the existing model-performance insight reader and
sample-size ranking guard. The `/pull-requests` destination (#187 / N02) groups
retained session `pr_link` HTTP(S) URLs and linked sessions; it does not call
GitHub and does not invent PR rows from counters or branch/time. Provider URL
capture proof remains #183 and #184. The `/insights` route remains directly
available as a utility during the Home-based insight surface from issue #150.

Historical V1 used a four-tab light shell (Home · Sessions · Governance ·
Integrations) with Models/PRs secondary; that IA is superseded by ADR 0003.
See [ADR 0003](../decisions/0003-reference-aligned-ui.md), the
[roadmap](../ui/UI_PRODUCT_ROADMAP.md) and
[implementation specification](../ui/DESIGN_IMPLEMENTATION_SPEC.md).

Browser authentication uses `POST /unlock` with the token from `make auth-token`.
A successful unlock sets an httpOnly **Secure** session cookie. Use the
`localhost` hostname (the daemon default) so browsers accept that cookie over
local HTTP; bare `127.0.0.1` may reject Secure cookies. JSON management
endpoints continue to accept `Authorization: Bearer` and also accept the
session cookie.

The Sessions page uses the stable Task 009 read API data and renders retained
sessions as an evidence browser: the primary row label combines tool and
relative start time, while the provider-prefixed native session ID remains
visible as secondary detail. A session detail is loaded only after navigation to
`/sessions/{id}`. The detail view renders glossary-backed availability badges,
friendly timeline event titles, and token values with units, all backed by the
event store. Deletion requires an in-app confirmation before
`POST /sessions/{id}/delete`; the repository removes the session and all retained
events transactionally.

The default Sessions view and Home counts use the primary identity scope:
provider-backed sessions plus legacy rows whose boundary is not yet proven.
Content-derived and trace-only rows remain retained and inspectable through the
Sessions `Observations` view; `All retained rows` combines both scopes. The
identity column labels the scope and evidence source. No view merges observations
into a conversation without provider correlation evidence.

In the local-only edition, session lists and details display the raw
provider-native session, conversation, and request IDs with a stable provider
prefix (`codex:`, `claude-code:`, `cursor-agent:`). Epic #87 removed ingest-time
hiding: no opaque fingerprints or class/boundary tokens are shown — the front end
renders real data only.

The dashboard never converts unavailable telemetry into zero. Examples include
an unavailable model or completion time. Integration status is derived only
from observed local sessions: with none observed it says `Awaiting telemetry`,
rather than claiming that a provider has been detected.

Home emphasises orchestration usage (sessions today, tools observed, insight
highlights) and does not surface cost labels or links. Costs remains a utility
page available at `/costs` from other authenticated pages, outside the primary
navigation.

The Governance primary destination renders detect-and-report findings from the
existing risky-access and unapproved-MCP engines over retained events. Each
finding section shows outcome and visibility badges, evidence tables (raw
path/command or MCP server name), and session links when a finding carries a
session id. An empty MCP allowlist is labelled allowlist-not-configured /
indeterminate.

Below Findings, an Access Rules tablist offers MCP servers, Skills, Files &
Paths, and Prompt Keywords (`?rules=`). MCP servers is the only editable
control: named observed MCP servers and configured-only entries are rendered as
checkboxes, and an authenticated Save atomically writes the validated local YAML
and reloads both HTML and JSON findings in-process. Skills, Files & Paths, and
Prompt Keywords are honest unavailable shells until config schema keys
(`governance.skills_allowlist`, `governance.path_rules`,
`governance.prompt_keywords` or equivalents) and detectors exist — no fake
allow/block counts and no Publish/Enforce controls. The page makes no
enforcement claim; the local edition remains detect-and-report only.

The preceding Access Rules shell description reflects #160, whose PR #185
merged on 2026-09-19 at 13:54:38 UTC (rechecked during this audit).
The full design remains follow-up work. Follow-up #191 composes the
reference layout; #192–#194 own real skill/path/prompt schemas and editors.
Runtime interception, publishing and sharing remain separately gated.

The Insights page reads MCP inventory, skill usage, model-performance, and
context-pressure summaries from retained canonical events. It renders the raw
provider-reported MCP server names (the correlation identity — epic #87 removed
server fingerprints), glossary labels for enum states, an MCP "Unused connection"
column for connected-but-unused servers, and request-level token context labelled
as not exact per-MCP allocation. Context pressure rows link back to session detail
so the retained evidence can be inspected.

The Costs page reads local calculation summaries, shows calculated estimates
and cost statuses, and never represents an unknown price as zero.

The local-only product contract retains raw observed prompts, responses, source
content, provider-native IDs, paths and commands (epic #87; #94 content logs).
Account identifiers/email remain excluded at the wire boundary, diagnostics are
sanitised, and cloud/team sharing requires a separate field review. Historical
Privacy-page/default copy claiming all content is disabled must be corrected
under #188; it is not a reason to hide already retained evidence. JSONL/prior
adapter gaps remain #173, #104 and #105, not a claim of universal capture.
The default retention period is 30 days. Bulk deletion requires typing
`DELETE ALL`. The current MCP allowlist is file-backed and validated by the
daemon; the new local editors are planned interfaces, not implemented here.
