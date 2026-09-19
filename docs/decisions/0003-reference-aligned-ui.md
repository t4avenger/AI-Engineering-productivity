# ADR 0003: Reference-aligned dark sidebar and staged governance UI

## Status

Accepted — 2026-09-19; product direction approved by the repository owner.
Implementation is tracked by #148, not declared complete by this ADR.

## Context

The supplied Governance Policies and Session Trace designs show a dark sidebar,
dense panels, five navigation destinations and rich trace/policy interactions.
The earlier roadmap made these optional, omitted Models/Pull Requests and
excluded retained conversation content. That is not the accepted target.
Local runtime interception and a cloud trust model are not implemented.

## Decision

1. Retain ADR 0002's Go templates, HTMX, same-origin auth and local assets.
2. Require the dark sidebar and reference composition. Primary destinations:
   Overview, Sessions, Pull Requests, Models, Governance. Preserve existing
   routes and utility Integrations, Insights, Privacy and Costs access.
   Overview uses `/` and never renders cost labels or links.
3. Use [the originals](../ui/references/README.md) and
   [numbered specification](../ui/DESIGN_IMPLEMENTATION_SPEC.md) as acceptance
   references with TelemetryIQ branding, responsiveness and WCAG 2.2 AA.
4. Show retained prompt/response previews and full local inspector content,
   with honest availability. This does not authorise uploads or diagnostic leaks.
5. Deliver local detect-and-report editors first. Architecture gates precede
   enforcement/approvals, version/publish/audit/exception lifecycle, team/environment
   scope/sharing and authenticated GitHub enrichment.
6. Pull Requests initially uses retained evidence; Models reuses existing
   performance contracts. Neither page requires a network account.

## Consequences

#161 replaces the optional sidebar/light-theme direction inherited from #71/#76
and older BMAD visual guidance. Closed work remains historical evidence.
ADR 0001 remains superseded; ADR 0002's stack/auth decisions remain accepted.
Theme switching and exact synthetic image text are not required.

Enforced, Publish, Share and pre-send protection cannot be enabled by a styling
task. Local-stage omissions are explicitly tracked, not called complete.
Capture and read contracts precede trace claims; missing data stays unavailable.
