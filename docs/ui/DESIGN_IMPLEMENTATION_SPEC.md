# UI design implementation specification

Status: accepted target, 2026-09-19. Parent: [#148](https://github.com/t4avenger/AI-Engineering-productivity/issues/148).
Delivery status and issue ownership live in [the roadmap](UI_PRODUCT_ROADMAP.md).
The source images are [Governance Policies](references/governance-policies.png)
and [Session Trace](references/session-trace.png); both are visual acceptance references.

## Authority and execution contract

The images govern composition, density, hierarchy and interaction affordances.
PRODUCT_MAP governs evidence, privacy and product behaviour. Synthetic image
labels, counts, timestamps, brand, and permissions are illustrative, never seed data.
TelemetryIQ remains a local Go/HTMX application. No CDN, SPA rewrite, telemetry
upload, or new account requirement is implied.

Each issue must cite requirement IDs below and supply source fixtures, dependency
issues, entry points, observable acceptance criteria and verification evidence.
Complete prerequisites before enabling dependent controls. A missing provider
field is an availability state, not permission to invent a field or downgrade a
capability to unsupported. A closed capture issue proves only its stated fixtures.

## Shared shell and destination contract

| ID | Requirement | Acceptance |
|---|---|---|
| S01 | Persistent dark sidebar, TelemetryIQ identity, five primary destinations in image order: Overview, Sessions, Pull Requests, Models, Governance. Utility group: Integrations, Insights, Privacy, Costs, unlock/logout as appropriate. | Overview uses existing `/`; Sessions `/sessions`; new PR `/pull-requests`; new Models `/models`; Governance `/governance`. Active destination has text, icon and blue accent. Costs is omitted on Overview, including utility navigation. Existing deep links continue working. |
| S02 | Match the navy background, layered panels, subtle blue-grey borders, compact spacing and cyan/blue selection from both images. | Use shared CSS variables and locally available sans/mono fonts. Initial tokens: canvas #0b1722, panel #101f2d, selected #153450, border #294356, text #eef4ff, muted #adc5df, accent #38bdf8. Adjust tokens only as needed to pass contrast; no light default or optional sidebar. |
| S03 | Desktop shell and responsive behaviour. | At 1680×945 and 1440×900: 200px sidebar, 300px right rail, remaining width for content; 16px panel gaps, approximately 44px table rows. At 1024×768: 64px labelled-on-focus navigation and right rail below content. Below 768px: keyboard-operable navigation disclosure and single-column panels. Horizontal scroll stays inside timelines/tables, never the entire page. |
| S04 | WCAG 2.2 AA and semantic controls. | Body text at least 14px; small metadata at least 12px. Text contrast 4.5:1, UI/focus 3:1; text/icon status cues; 24px minimum targets; skip link; visible focus; reduced motion. Tabs support arrows/Home/End, panels labelled; disclosure buttons expose expanded state. |
| N01 | Overview preserves behaviour, efficiency, governance and integration highlights from #150. | Rename Home in visible navigation; no cost figures, labels, links or CTA. Existing detailed Insights links work. No fabricated headline statistics. |
| N02 | Pull Requests shows retained PR identity and linked sessions, not a GitHub client in its first delivery. | Group only explicit retained HTTP(S) PR URLs; reject executable URL schemes. Show provider/repository/branch only when evidenced, distinct session count and session links. Search retained URL/repository/branch text. Preserve original URL as evidence; no merge by branch/time alone. Missing URL has an explained empty/partial state, not an invented PR number. No merged/status/author claims without evidence. |
| N03 | Models exposes existing model-performance evidence. | Reuse insight reader, definitions and sample thresholds; show model/provider, sample size, outcome coverage, retries/errors, token efficiency and latency with availability. Reuse existing filters only; link to detailed Insights evidence. No new ranking formula or session-status-derived task success. |

## Session Trace: Image #2

| ID | Image region and behaviour | Evidence / acceptance |
|---|---|---|
| T01 | Compact Session Trace title/header: session breadcrumb, outcome, model, branch, PR, duration, tokens, optional estimated cost; timestamp and overflow menu. | Reuse #158 metadata and availability. Multi-model sessions labelled as such; no arbitrary single-model claim. Cost remains secondary and exposes status/calculation provenance. Preserve deletion confirmation/data-retention access in overflow. Share is deferred under E03, not a working upload control. |
| T02 | One relative time axis for all five lanes; fixed lane labels; shared horizontal scrolling and interval overlap stacking. | Offset from observed session start, otherwise earliest retained timestamp labelled as an observed-window origin. Missing timestamps appear in an Unplaced events list. Point events stay points; no inferred duration. Stable order timestamp then event ID, source provenance visible. Pagination/load-more says when only part of the trace is loaded. |
| T03 | Conversation lane with user/assistant cards showing role, timestamp and retained text preview; full text on selection. | Escape text as data; preserve newlines. Display provider-redacted sentinel as received with explanation. Length-only records never become invented content. Do not render raw API payloads as duplicate messages unless explicit identity proves their relationship; keep them inspectable as evidence. Provider/source coverage visible. |
| T04 | Agent lane shows observed agent turns, explicit plan events and model work. | Use actual event labels and intervals. No generated plan, chain-of-thought, inferred stage sequence, or screenshot's Plan/Inspect/Implement/Test/Revise labels without evidence. Missing planning telemetry has an explicit availability state. |
| T05 | Tools & MCP lane distinguishes tools, external MCP calls and skills, using icons and text. | Reuse canonical operations and explicit skill evidence. Show observed tool/server/skill identity, duration and outcome; metric-only anonymous skill signals do not become named invocations. Link to related files/spans only through provider IDs. |
| T06 | Files lane contains observed path and operation cards; selected tool's files appear in inspector. | Raw retained path, read/write/delete/unknown action, source event and proven operation link. Show +/- counts only when per-file evidence exists; aggregate lines-of-code counters are not file diffs. Unknown path, action, relationship or duration remains separately labelled. #156 owns proof/projection. |
| T07 | Expandable nested Spans lane with parent/child bars and selected-span focus. | #157 projects raw trace/span/parent IDs, timing, status and evidence. Group by trace ID; never join Codex trace-only observations to conversation IDs by time/model proximity. Missing parent is a labelled orphan; invalid cycles cannot cause recursion failure. |
| T08 | Bottom inspector: title/type, start/end/duration, outcome, close; Details, Attributes, Events tabs; related file operations. | Selected event highlighted across lanes via stable event identity. Details shows source IDs/links and availability; Attributes shows escaped retained values including unknown provider extensions; Events shows only proven related events, not all nearby events. Unknown/removed IDs give an actionable state. Close/Escape restores focus. Deep link uses `?event=<event_id>&inspector=details|attributes|events`; preserve paging/filter state. |
| T09 | Right rail Session Breakdown, Governance checklist and Event Legend. | Breakdown uses the interval contract below, with duration and percentage denominator visible. Governance uses session-scoped engines and evidence links; unconfigured/missing coverage is indeterminate, never a green 'No secrets' or 'Approved model' claim. Legend maps actual rendered event kinds to icon/text; unavailable kinds explained. |
| T10 | Dense traces stay usable. | Render bounded pages using existing cursor conventions, preserve selection when loading more, never report loaded-page totals as full-session totals. Server error/retry, empty session, unsupported lane, partial capture and deleted session each have distinct text. Provide an accessible chronological list alternative to the lanes. |

### Additive read interfaces

Do not change existing event, operation, session or cost contracts to match a
screenshot. Add session-scoped authenticated projections below; share application
readers between HTML and JSON, not business logic in templates. Preserve existing
`data`/`pagination` and structured error envelopes. Document schema/examples and
contract tests with the owning issue before its dependent UI lands.

- #156: `GET /api/v1/sessions/{id}/files`, cursor-paged evidence entries with
  event ID, nullable operation ID/path/action/timestamp/duration and per-file
  additions/deletions only if observed. Include availability and source provenance.
- #157: `GET /api/v1/sessions/{id}/spans`, flat cursor-paged nodes keyed by
  trace ID + span ID, nullable parent, original times, duration/status,
  source event IDs and proven operation links. The client derives nesting.
  Distinguish parent not loaded from parent absent in full retained data.
- Conversation projection: `GET /api/v1/sessions/{id}/conversation`, cursor-paged
  role/time/text/source-event records with per-record content availability.
  Filter only against retained content; never fetch provider content externally.
- Inspector: `GET /api/v1/sessions/{id}/events/{event_id}`, selected retained
  event and proven relationships. Check session ownership, use structured 404
  for missing or foreign event IDs. Large values use explicit truncation with a
  user action to retrieve more; full retained text remains reachable. This is
  separate from the development-only last-ingest inspector.
- Breakdown: `GET /api/v1/sessions/{id}/breakdown`, full retained-session
  calculation, independent of UI pagination. Returns availability, wall interval,
  exclusive category durations, overlap/unclassified duration, coverage and
  calculation version. Nullable values stay null.

New PR/Models pages initially use existing readers; do not add public APIs
solely to render them. Policy write interfaces are defined under G06.

### Breakdown interval contract

Use only valid explicit starts/ends or explicit duration with a documented start
anchor. Ignore inverted intervals and mark coverage partial; do not repair them
silently. Deduplicate the same observed interval by its source identity.
Calculate the union within the observed session window. Divide into disjoint
segments: one proven category -> that category; multiple different categories ->
Overlap; no evidence -> Unclassified. Same-category nested intervals count once.
Planning and User wait require explicit evidence; absence of activity is not user
wait. Show Planning, Tool calls, Model generation, User wait only where proven,
plus Overlap and Unclassified where nonzero. If the window has no positive
duration or no valid classified intervals, show unavailable instead of a donut.
Percentages use observed window duration, not summed overlapping span durations.
Label partial window/coverage and calculation version. Never reproduce the
mock's internally inconsistent percentages as actual data.

## Governance Policies: Image #1

| ID | Region and local delivery | Future target / acceptance |
|---|---|---|
| G01 | Header 'Governance Policies', current local configuration and 'Detect and report' status; Save local changes when dirty. | Policy-set selector, version, published time, production environment, user/repository counts, audit link and Publish belong to E02/E03. Do not display mock numbers or 'Enforced'. |
| G02 | Main tabs Access Rules, Prompt Protection, Enforcement, Exceptions; summary cards for MCP, Skills, File rules, Prompt rules; category tabs MCP Servers, Skills, Files & Paths, Prompt Keywords. | Implement available tabs; future tabs render an explanation of availability and no active enforcement controls. Counts come from configured rules with labelled meaning, not observed usage or placeholder values. Preserve `?rules=` deep links. Findings remain accessible below configuration with evidence/session links. |
| G03 | MCP access table: search, status filter, identity, access policy, scope, applies-to, last changed and row edit. | Local access means allowlisted/not allowlisted/unconfigured, never runtime Allowed/Blocked. Scope is local daemon; unsupported read/write or team scope is unavailable. Filter All/Allowlisted/Not allowlisted. Configured-only entries remain editable. Add rule accepts explicit server identity via existing validated config path. Last-changed unavailable until backed by recorded history. |
| G04 | Files & Paths expandable editor with policy modes, allowed/blocked path chips and add/remove rules. | In local delivery, label modes Monitor all / Approved paths only / Flag all; show 'detect only' beside disallowed-path rules. No claim of blocked writes. Exact/glob matching, precedence and safe preview are specified below; runtime Allow/Restrict/Block waits for E01. |
| G05 | Right rail: Policy Preview, prompt keyword panel, unsaved-change summary. | Preview is a branching decision explanation, not the screenshot's linear Allow → Approval → Block execution. Local branches are no finding / finding / indeterminate, with source/policy evidence. Dirty count and diff match actual local edits. Failed save keeps edits; success clears dirty state and updates findings. Navigation away offers keep/discard; no background publish. |
| G06 | Skills, path and prompt policy editors are real schema + detector + persistence features, not permanently empty shells. | Each has its own issue and atomic config save/reload, validation errors, reset and preview; no ingest-time hiding. New fields optional so existing YAML still loads. Rule changes operate over retained data and record detector/config provenance. |
| G07 | Prompt Protection tab/right-rail credentials, customer-data and custom keyword groups; patterns, regex choice and action description. | Local name/description says 'Prompt findings — after capture'; action is Record finding. No 'before prompts leave device' guarantee, blocking or external alerts. Match retained prompt evidence only; missing bodies are indeterminate. Pattern labels and matched synthetic evidence are accessible locally; values never enter diagnostics. |
| G08 | Existing risky-access/MCP findings remain first-class. | Preserve severity, outcome, visibility, raw local evidence, remediation and session navigation. Saving empty allowlists remains unconfigured/indeterminate; no clean result from absent observations. |

### Local policy implementation decisions

These are planned interfaces, not assertions that schemas/detectors exist today.

- Reuse `governance.mcp_allowlist` and `POST /governance/mcp-allowlist`.
  Preserve today's semantics and config-only entries.
- Add optional `governance.skills_allowlist` string list; edit via
  `POST /governance/skills-allowlist`. Match exact explicit provider skill
  identities; unnamed/inferred usage is indeterminate, not a violation.
- Add optional `governance.path_rules`: mode `monitor|approved_only|flag_all`,
  `allowed` and `blocked` pattern lists. Each pattern has
  `kind: exact|glob` and `value`. Save via `POST /governance/path-rules`.
  Match retained path strings only, without filesystem reads or symlink resolution.
  Glob syntax: * within a segment, ** across segments, ? one non-separator
  character; reject other metacharacters rather than guessing. Forward slash is
  the glob separator; preserve raw path for display. Do not expand ~, variables or
  reinterpret relative paths using the daemon's working directory.
  Blocked match wins; approved_only requires an allowed match; flag_all flags
  every observed path; monitor flags only explicit blocked matches. Missing path
  is indeterminate. Case-sensitive matching and literal exact rules must be
  documented in help; unsupported provider path semantics remain partial.
- Add optional `governance.prompt_keywords` rule list of stable id, label,
  group (`credentials|customer_data|custom`), enabled, match kind
  (`literal|regex`), and value. Save via `POST /governance/prompt-keywords`.
  Literal matching is case-sensitive; regex uses Go RE2 (case folding only by
  explicit regex flags). Reject invalid/empty patterns and duplicate IDs.
  Start with no enabled built-ins; category chips are configured rules, not
  unsupported promises of comprehensive secret/medical-data detection.
- All new forms require existing management authentication and origin protection;
  preserve authentication and explicitly test/reject cross-origin mutation requests.
  Use the current 64 KiB form body cap, atomic persistence and in-process reload.
  Validate the whole submission before writes, preserve unrelated settings,
  and never log rule values or matched content. Limit each new rule list to 100
  entries and each pattern/identity to 1024 UTF-8 bytes; labels to 120 bytes and
  prompt-rule IDs to 64 ASCII letters/digits/underscore/hyphen characters.
  Document these additive limits in the schema and test boundary values.
  POST without HTMX must work; HTMX returns the same validation/result semantics.
- Findings retain `violation|not_violation|indeterminate`, policy identity/version,
  detector version, source IDs and provenance. Additive insight endpoints:
  `/api/v1/insights/unapproved-skills`, `/api/v1/insights/path-rules`,
  `/api/v1/insights/prompt-keywords`, following existing paging/envelopes.
  These are observation reports and cannot intercept provider requests.

## Gated enterprise target

| ID | Features in the reference | Required gate before implementation |
|---|---|---|
| E01 | Enforced status, Allow/Restrict/Block, Require approval, blocked-write guarantee, pre-send prompt protection. | Provider-specific interception proof; architecture/product revision; supported operations; fail-open/fail-closed and offline behaviour; approvals timeout/replay/cancellation; bypass tests; threat model. Detect-and-report MVP remains unchanged until this gate is accepted. |
| E02 | Policy sets/versions, publish, audit log, exceptions, reviewable change list, rollback. | Versioned policy schema; draft vs active semantics; conflict detection; atomic activation/rollback; immutable audit records; exception scope/expiry; migration and compatibility contracts. Local Save is not Publish. |
| E03 | Environment selector, users/repositories, shared policies and Session Share. | Identity/authorisation, tenancy, least-privilege repository access, retention and upload-field review, consent/revocation, sanitised preview and cross-tenant tests. Sharing cannot reuse local raw visibility rules. |
| E04 | GitHub synchronisation for richer Pull Requests. | Auth/token storage, repository permissions, offline/cache behaviour, pagination/rate limits, deleted/revoked access, correlation proof and trust-boundary design. Does not block N02's local retained-evidence view. |

Gate issues deliver architecture/contracts and bounded implementation children.
They do not authorise implementing runtime enforcement or multi-tenant services
as part of this documentation update.

## Verification contract

Run applicable QUALITY_GATES checks for each implementation:
`rtk make format-check`, `rtk make lint`, `rtk make static-analysis`,
`rtk make test-unit`, `rtk make test-component`,
`rtk make test-integration`, `rtk make coverage`,
`rtk make security-scan`, `rtk make build`.
UI changes also run `rtk make test-e2e`; changed contracts run
`rtk make test-contract` plus the new endpoint-specific contract tests.
Shared state changes run `rtk make test-race`; ingest/parsing changes run
`rtk make test-fuzz-smoke`. Phase exit requires checkpoint evidence and
`rtk make verify-push`, not just a screenshot.

Use existing `web/tests/e2e/live-ingest-helpers.ts` and sibling fixture builders.
Do not duplicate payloads/assertions to satisfy these requirements. New-code
duplication must remain at or below 3%; backend coverage at least 80%.

Visual acceptance: compare both pages side-by-side with the original references
at 1680×945, and capture deterministic synthetic-data baselines at 1440×900,
1024×768 and 390×844. Review geometry, density, hierarchy, selected states,
inspector and rail placement rather than requiring identical synthetic text.
Use Playwright screenshot assertions for subsequent regressions; no live user
data in screenshots. Document each intentional deviation by requirement ID.
Exercise keyboard-only operation, 200% zoom, reduced motion, contrast, long
paths/content, many overlapping spans, absent fields, errors and empty datasets.
At least one non-mocked daemon ingest→read→UI journey per newly surfaced
capability; golden fixtures alone are insufficient.
