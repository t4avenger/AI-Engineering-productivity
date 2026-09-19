# UI Product Roadmap: reference-aligned TelemetryIQ

Accepted target: 2026-09-19. Stack: Go `html/template` + HTMX.
Parent: [#148](https://github.com/t4avenger/AI-Engineering-productivity/issues/148). Product authority: [PRODUCT_MAP](../../PRODUCT_MAP.md) §§11,17.
Visual decision: [ADR 0003](../decisions/0003-reference-aligned-ui.md).

Alignment evidence: [2026-09-19 audit](DESIGN_ALIGNMENT_AUDIT.md).

## 1. Read before implementation

1. Inspect the originals: [Governance Policies](references/governance-policies.png)
   and [Session Trace](references/session-trace.png); [provenance/checksums](references/README.md).
2. Read [DESIGN_IMPLEMENTATION_SPEC.md](DESIGN_IMPLEMENTATION_SPEC.md). Its
   requirement IDs, interaction rules, interfaces and tests are normative.
3. Read the owning GitHub issue and prerequisite fixture/contracts. Report any
   conflict instead of silently implementing a different product direction.
4. Reuse existing code, fixture builders and browser assertions. No invented
   telemetry or duplicate test blocks; Sonar new-code duplication must be <=3%.

The target is a close visual match, including the five destinations in the
images. TelemetryIQ branding, evidence honesty, accessibility and staged
enterprise behaviour are the deliberate differences. The earlier four-tab,
light/optional-sidebar and no-conversation directions are superseded.

## 2. Decisions and navigation

| Decision | Accepted target |
|---|---|
| Shell | Dark navy, persistent desktop sidebar, dense panels/tables, selected-state blue; responsive and WCAG 2.2 AA |
| Primary nav | Overview `/` · Sessions `/sessions` · Pull Requests `/pull-requests` · Models `/models` · Governance `/governance` |
| Utilities | Integrations, Insights, Privacy, Costs, authentication; preserve URLs. Costs omitted on Overview |
| Conversation | Retained prompt/response previews and full local inspector content; provider omissions/redactions explicit |
| Local governance | Detect-and-report with real local saves and previews; no blocked-action guarantee |
| Enterprise | Full reference tracked through explicit architecture gates; no fake controls or counts |
| Pull Requests | Retained fixture-backed URLs/session correlation first; authenticated sync later |
| Models | Existing model-performance contracts, thresholds and availability |
| Runtime | Same-origin Go/HTMX; no SPA, CDN, new account requirement or automatic sharing |

## 3. Baseline versus remaining work

Status verified against GitHub on 2026-09-19; recheck before starting work.

- V1 foundations [#149](https://github.com/t4avenger/AI-Engineering-productivity/issues/149)–[#155](https://github.com/t4avenger/AI-Engineering-productivity/issues/155) are closed: four-tab navigation,
  Overview/Home highlights, findings, MCP save, operations timeline and live tests.
- [#158](https://github.com/t4avenger/AI-Engineering-productivity/issues/158) header metadata and [#163](https://github.com/t4avenger/AI-Engineering-productivity/issues/163) primary/observation session scopes
  are closed. PR URL proof remains [#183](https://github.com/t4avenger/AI-Engineering-productivity/issues/183)/[#184](https://github.com/t4avenger/AI-Engineering-productivity/issues/184); never derive a link
  from `pull_request.count`.
- [#160](https://github.com/t4avenger/AI-Engineering-productivity/issues/160) is **closed** and its implementation
  [PR #185](https://github.com/t4avenger/AI-Engineering-productivity/pull/185)
  **merged on 2026-09-19 at 13:54:38 UTC**, rechecked during this audit.
  Its shells are a completed foundation, not the full Governance reference.
- Claude content is retained on the #94 log surface; JSONL/other prior drops
  remain coordinated with [#173](https://github.com/t4avenger/AI-Engineering-productivity/issues/173), #104 and #105. Missing content is not a
  product prohibition on local display.
- Codex 0.154.0 span capture shipped in [#172](https://github.com/t4avenger/AI-Engineering-productivity/issues/172). Spans can be trace-only
  observations, with no proven conversation join. Claude spans are available;
  neither fact supplies the missing common projection/UI.
- File-operation evidence/projection [#156](https://github.com/t4avenger/AI-Engineering-productivity/issues/156) and span projection
  [#157](https://github.com/t4avenger/AI-Engineering-productivity/issues/157) remain open. The image's per-file +/- counts and task stages
  must not be inferred from aggregate metrics.
- [#186](https://github.com/t4avenger/AI-Engineering-productivity/issues/186) Models
  destination is delivered as secondary navigation over the existing
  model-performance reader; five-destination primary shell remains [#161](https://github.com/t4avenger/AI-Engineering-productivity/issues/161).
- No Pull Requests primary page, full trace, matching dark shell, local
  skill/path/prompt editors or enterprise controls are declared delivered here.

## 4. Delivery order and ownership

Every reference region has a numbered specification requirement and owner below.
Dependencies indicate required contracts, not permission to skip unavailable states.

| Stage | Issue / requirement | Prerequisites and completion boundary |
|---|---|---|
| Evidence | [#156](https://github.com/t4avenger/AI-Engineering-productivity/issues/156) — T06 Files projection | Review #87/#109/#105 fixtures; prove each field; add read contract and live gate |
| Evidence | [#157](https://github.com/t4avenger/AI-Engineering-productivity/issues/157) — T07 span projection | Reuse #172 and Claude fixtures; keep trace-only observations separate |
| Evidence | [#188](https://github.com/t4avenger/AI-Engineering-productivity/issues/188) — T03 conversation projection | #94 retained bodies; coordinate #173 missing sources; do not block supported log content on all providers |
| Destination | [#186](https://github.com/t4avenger/AI-Engineering-productivity/issues/186) — N03 Models | Existing model-performance readers; independent of shell |
| Destination | [#187](https://github.com/t4avenger/AI-Engineering-productivity/issues/187) — N02 Pull Requests | Empty state can land now; populated-provider claims depend on #183/#184 evidence |
| Shell | [#161](https://github.com/t4avenger/AI-Engineering-productivity/issues/161) — S01–S04, N01 | Land Models/PR routes before adding their nav links; ADR 0003 |
| Trace part | [#189](https://github.com/t4avenger/AI-Engineering-productivity/issues/189) — T08 inspector | Existing timeline integration first; consume file/span projections as they land |
| Trace part | [#190](https://github.com/t4avenger/AI-Engineering-productivity/issues/190) — T09 interval breakdown | #157 projection/interval evidence; full-session calculation |
| Trace assembly | [#159](https://github.com/t4avenger/AI-Engineering-productivity/issues/159) — T01–T10 | #156, #157, #161, #188, #189, #190; all five lanes and honest partial states |
| Local governance | [#191](https://github.com/t4avenger/AI-Engineering-productivity/issues/191) — G01–G03, G05, G08 | #161 and integration of #160/PR #185; preserve working MCP Save |
| Local policy | [#192](https://github.com/t4avenger/AI-Engineering-productivity/issues/192) — G06 skills | #191 plus explicit skill fixtures; schema/detector/save/editor |
| Local policy | [#193](https://github.com/t4avenger/AI-Engineering-productivity/issues/193) — G04/G06 files & paths | #191 and #156; exact/glob semantics and detect-only preview |
| Local policy | [#194](https://github.com/t4avenger/AI-Engineering-productivity/issues/194) — G07 prompts | #191 and #188; RE2/literal rules over retained content |
| Local phase exit | [#195](https://github.com/t4avenger/AI-Engineering-productivity/issues/195) — all local requirements | Local destinations, shell, trace and policy editors complete; checkpoint + full verification |
| Future gate | [#196](https://github.com/t4avenger/AI-Engineering-productivity/issues/196) — E01 enforcement/approvals | Provider interception proof + deliberate product/architecture revision |
| Future gate | [#197](https://github.com/t4avenger/AI-Engineering-productivity/issues/197) — E02 policy lifecycle | Draft/version/publish/audit/rollback/exception contracts; precedes active-version enforcement |
| Future gate | [#198](https://github.com/t4avenger/AI-Engineering-productivity/issues/198) — E03 environment/team/share | #197 plus separate upload, identity/authorisation and privacy review |
| Future gate | [#199](https://github.com/t4avenger/AI-Engineering-productivity/issues/199) — E04 GitHub sync | #187 local page; optional authenticated enrichment design |

The [#160](https://github.com/t4avenger/AI-Engineering-productivity/issues/160) issue/PR retains its original shell acceptance criteria. New work
belongs to the follow-ups, not a retroactive expansion of that implementation.
Historical [#71](https://github.com/t4avenger/AI-Engineering-productivity/issues/71) receives a supersession notice; closed children stay intact.

## 5. Acceptance and phase gates

### Local reference-aligned phase

- [ ] S01–S04: five destinations, dark shell, desktop density and responsive a11y.
- [ ] N01–N03: existing Overview preserved; evidence-backed Models and PR pages.
- [ ] T01–T10: five aligned trace lanes, conversation text, inspector and right rail;
  missing evidence, timestamps, correlations and durations are explicit.
- [ ] G01–G08: reference Governance composition with real MCP/skill/path/prompt
  saves, findings, safe local previews and dirty-state feedback.
- [ ] Both originals compared with deterministic implementation screenshots;
  every deviation recorded by requirement ID.
- [ ] No green 'clean' status without coverage, no fabricated numbers, no
  double-counted breakdown, no claims of active runtime blocking.
- [ ] Applicable QUALITY_GATES pass; backend coverage >=80%, duplication <=3%;
  live daemon ingest→read→UI coverage and WCAG 2.2 AA evidence recorded.
- [ ] #195 checkpoint and `rtk make verify-push` completed before phase closure.

### Full future reference

E01–E04 remain separately gated. Each architecture issue must produce reviewed
contracts and bounded implementation children before enabling runtime controls,
publishing, environments, shared scope, sharing or GitHub synchronisation.
Finishing local visual work cannot close these requirements.

## 6. Next smallest task

Implement [#187](https://github.com/t4avenger/AI-Engineering-productivity/issues/187)
(Pull Requests destination) with an honest empty/unavailable state now; populated
provider claims wait on #183/#184. In parallel only when explicitly assigned,
#156/#157 can establish trace evidence contracts.

This documentation update changes no runtime APIs or schemas. Planned additive
interfaces are specified in the implementation specification and owned by the
issues above. No UI delivery phase is declared complete by writing this roadmap.
