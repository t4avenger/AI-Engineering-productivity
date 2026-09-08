# Local dashboard

The local dashboard is served by the TelemetryIQ daemon using Go `html/template`
and HTMX (ADR 0002). It provides Home, Sessions, Session Detail, Insights,
Integrations, Privacy, and Costs pages over the same loopback origin as the
JSON API and OTLP intake. It does not send analytics or dashboard data to third
parties.

Browser authentication uses `POST /unlock` with the token from `make auth-token`.
A successful unlock sets an httpOnly **Secure** session cookie. Use the
`localhost` hostname (the daemon default) so browsers accept that cookie over
local HTTP; bare `127.0.0.1` may reject Secure cookies. JSON management
endpoints continue to accept `Authorization: Bearer` and also accept the
session cookie.

The Sessions page uses the stable Task 009 read API data. A session detail is
loaded only after navigation to `/sessions/{id}`. Deletion requires an in-app
confirmation before `POST /sessions/{id}/delete`; the repository removes the
session and all retained events transactionally.

In the local-only edition, session lists and details may display provider-native
session or conversation IDs with a stable provider prefix (`codex:`,
`claude-code:`, `cursor-agent:`). Opaque fingerprints are reserved for protected
identifiers outside this narrow local correlation-key exception.

The dashboard never converts unavailable telemetry into zero. Examples include
an unavailable model or completion time. Integration status is derived only
from observed local sessions: with none observed it says `Awaiting telemetry`,
rather than claiming that a provider has been detected.

Home emphasises orchestration usage (sessions today, tools observed, insight
highlights) and does not surface cost labels or links. Costs remain a secondary
page available at `/costs`, outside the primary navigation.

The Insights page reads MCP inventory, skill usage, model-performance, and
context-pressure summaries from retained canonical events. It renders
provider-reported MCP server names when available, privacy-safe server
fingerprints only as a fallback, glossary labels for enum states, an MCP
"Unused connection" column for connected-but-unused servers, and request-level
token context labelled as not exact per-MCP allocation. Context pressure rows
link back to session detail so the retained evidence can be inspected.

The Costs page reads local calculation summaries, shows calculated estimates
and cost statuses, and never represents an unknown price as zero.

The Privacy page documents the enforced local-only defaults: no prompt,
response, or source-code retention; raw file paths reduced to coarse path-class
tokens; redacted command arguments; no sharing; and a default 30-day retention
period. These values are
not editable in the dashboard because configuration remains file-based and is
validated by the daemon. Bulk deletion requires typing `DELETE ALL`.
