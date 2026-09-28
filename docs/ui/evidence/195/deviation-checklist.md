# Deviation checklist for #195

Compared with `docs/ui/references/governance-policies.png` and
`docs/ui/references/session-trace.png`. Geometry in
`web/tests/e2e/reference-design.spec.ts` checks the shell and panel sizes.
Playwright `toHaveScreenshot` baselines are not updated until the live pages
are visually approved.

| IDs | What matches the reference | Intentional difference |
|---|---|---|
| S01 | Persistent dark sidebar, five destinations, TelemetryIQ name. | TraceLens branding and the marketing footer line are not copied. |
| S02 | Navy canvas, panel, border, selected row, and accent tokens. | No light theme. |
| S03 | 200px sidebar and 300px rail at desktop for Governance and Session Trace. Other pages do not keep an empty gutter. 44px rule rows. Tablet rail drops under the content. Narrow layout uses the menu disclosure. | Horizontal scroll stays inside the trace, not the page. |
| S04 | Visible text labels, focus, 12px metadata floor, reduced-motion, and rendered contrast checks. | Decorative icons are hidden from the accessibility tree. |
| T01 | Compact Session Trace title, breadcrumb, state, model, branch, PR, duration, tokens, timestamp, and overflow. | No Share upload and no invented cost. Delete stays in Actions. Extra metadata is behind a closed disclosure. |
| T02 | Shared ruler with five clock ticks and five fixed lanes. | Tick labels follow the observed window, not a fixed four-minute demo. |
| T03 | Conversation cards show role text, offset, and retained preview. | Point events use a readable card anchored at the timestamp and say they have no duration. |
| T04 | Agent lane shows observed model work only. | No Plan, Inspect, Implement, Test, or Revise stages and no connectors. |
| T05 | Tools, MCP, and skills use distinct icons and text. | Names come from retained evidence. |
| T06 | File cards use retained paths. Related files stay in the inspector. | Line-change counts appear only when the record already has them. |
| T07 | Span lane uses duration bars and nesting depth. | Missing parents stay labelled orphans. |
| T08 | Inspector is docked under the matrix with Details, Attributes, and Events. Escape, close, and deep links stay. | Attributes remain escaped retained JSON. Temporal neighbours are not linked. |
| T09 | Breakdown, session governance, and a legend of rendered kinds. | Indeterminate checks stay labelled. There is no green "approved model" or "no secrets" claim without an engine result. |
| T10 | Chronological lists remain, closed by default. | Loaded pages are not reported as full-session totals. |
| G01 | Header is Governance Policies with Detect and report and Save local changes when dirty. | No policy set, version, Enforced, user counts, audit log, or Publish. |
| G02 | Access Rules plus unavailable Prompt Protection, Enforcement, and Exceptions. Four summary cards. Category tabs. Findings sit below the workspace. `?rules=` deep links stay. | Counts are configured rules, not the screenshot's allowed/blocked usage. |
| G03 | MCP is the default workspace: search, All/Allowlisted/Not allowlisted, identity, local policy, Local daemon scope, applies-to, unavailable last changed, allowlist edit. | No Allowed/Blocked/Approval required and no free-text add that the save handler rejects. |
| G04 | Files & Paths is the workspace on `?rules=paths`, with Monitor all / Approved paths only / Flag all and path chips. | No Allow/Restrict/Block and no blocked-write claim. |
| G05 | Policy Preview rail states no finding, finding, or indeterminate for MCP, paths, and prompts. Unsaved text mirrors the editor. | Not the screenshot's Allow, Require approval, Block sequence. |
| G06 | Skills, paths, and prompt editors remain real forms with validation, reset, and save when their controllers exist. | Missing controllers stay explicitly unavailable. |
| G07 | Prompt workspace says Prompt findings — after capture and Record finding. | No pre-send protection. |
| G08 | Risky access and unapproved MCP stay below the workspace with evidence links. | Empty allowlists stay unconfigured. |
| E01–E04 | Not implemented. | Enforcement, publishing, sharing, and GitHub sync remain gated. |
