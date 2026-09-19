# UI design alignment audit — 2026-09-19

## Outcome

The [roadmap](UI_PRODUCT_ROADMAP.md) now targets the supplied designs rather than
optional visual inspiration. The [specification](DESIGN_IMPLEMENTATION_SPEC.md)
defines 29 numbered requirements, ownership, additive interface plans,
interactions, evidence rules and acceptance tests. The
[original images](references/README.md) are retained unchanged.

Owner-confirmed decisions: stage full governance; use the exact five design
destinations; show retained conversation content locally; deliver retained PR
evidence before authenticated GitHub integration.

## Findings corrected

| Previous mismatch | Correction |
|---|---|
| Four tabs and optional sidebar | Required dark sidebar, Overview/Sessions/Pull Requests/Models/Governance; utility destinations preserved |
| Models/PR destinations excluded | #186 Models and #187 retained-evidence PR page |
| Conversation excluded by stale privacy wording | T03/#188 shows retained text; PRODUCT_MAP precedence clarified; #173 still owns adapter gaps |
| Sparse multi-lane task with no interaction contract | T01–T10 and #156/#157/#159/#188–#190 define lanes, projections, inspector, breakdown and failures |
| Governance shells treated as sufficient | G01–G08 and #191–#194 require composed layout and real local editors/detectors |
| Mock implies runtime block/publish/team features | E01–E04/#196–#199 require separate architecture and product gates |
| Codex spans described as unsupported | #157 now uses #172's version-specific evidence without inventing session joins |
| #160 closed mistaken for shipped | PR #185 initially open; rechecked as merged at 2026-09-19 13:54:38 UTC and status corrected |

## Changed files

- [PRODUCT_MAP.md](../../PRODUCT_MAP.md): UI target and relevant privacy precedence.
- [local-dashboard.md](../architecture/local-dashboard.md): baseline versus planned behaviour.
- [ADR 0003](../decisions/0003-reference-aligned-ui.md): accepted visual/navigation decision.
- [UI_PRODUCT_ROADMAP.md](UI_PRODUCT_ROADMAP.md): status, dependency order and ownership.
- [DESIGN_IMPLEMENTATION_SPEC.md](DESIGN_IMPLEMENTATION_SPEC.md): implementation contracts.
- [Reference README](references/README.md) and its two PNGs: provenance, checksums, visual targets.
- This audit: completion evidence and limitations.

No runtime source, schema or test files were changed. Approximate added raster
size: 3.07 MB (1,534,321 + 1,532,557 bytes); both originals are 1672×941.
No runtime performance impact was introduced.

## GitHub updates

Updated existing issues:
[#148](https://github.com/t4avenger/AI-Engineering-productivity/issues/148),
[#156](https://github.com/t4avenger/AI-Engineering-productivity/issues/156),
[#157](https://github.com/t4avenger/AI-Engineering-productivity/issues/157),
[#159](https://github.com/t4avenger/AI-Engineering-productivity/issues/159),
[#161](https://github.com/t4avenger/AI-Engineering-productivity/issues/161);
added status/supersession notes to
[#160](https://github.com/t4avenger/AI-Engineering-productivity/issues/160) and
[#71](https://github.com/t4avenger/AI-Engineering-productivity/issues/71).

Created #186–#199 (14 issues), all linked and ordered in #148 and the roadmap.
Read-back compared all 21 complete bodies with the submitted drafts.
45 referenced issue identities and PR #185 were verified.
Closed historical child work was not reopened or rewritten into new scope.

## Verification

| Command/check | Result |
|---|---|
| `rtk git diff --check` | PASS, exit 0 |
| `rtk make format-check` | PASS, exit 0; existing Go files remain formatted |
| `rtk make security-scan` | PASS, exit 0: npm audit 0 vulnerabilities; Gitleaks 227 commits/no leaks; OSV 24 packages/no issues; Semgrep 590 rules/319 targets/0 findings; Trivy 0 findings; ShellCheck/actionlint completed |
| `rtk proxy /home/wayne/go/bin/gitleaks dir docs/ui --config .gitleaks.toml --no-banner` | PASS, exit 0; untracked UI documentation/assets included, no leaks |
| Document/asset validation | PASS: seven Markdown documents, 33 local links, 29 requirement IDs, two byte-identical PNGs/SHA-256 checks |
| GitHub read-back and referenced issue checks | PASS: 21 bodies, issue references and merged PR status verified |

A direct `gitleaks` invocation first failed because it was outside PATH; rerun
using the installed absolute path passed. The full security wrapper already sets
that tool path. Sandbox helper failures required elevated patch application;
they did not change the requested scope.

Semgrep used existing ignore rules and skipped the two >1 MB images; it is a
source-code scanner, not image validation. Original-byte and checksum checks
cover the assets. No new suppression was added.

Unit/component/integration/contract/race/fuzz/browser/build/coverage and standalone
lint/static-analysis commands were not run: this change contains only documents,
original PNG assets and GitHub issue text. Security scanning includes Semgrep.
Runtime coverage was not measured; no coverage threshold result is claimed.
Future implementation requires backend >=80%, new-code duplication <=3% and all
applicable QUALITY_GATES checks. No UI implementation phase is complete, so no
phase checkpoint or `make verify-push` completion is claimed.

## Remaining limits and next step

This documentation is submitted separately from the implementation PR on branch
`docs/ui-design-roadmap-alignment`, based on `main` after PR #185 merged.
GitHub issue bodies explicitly tell implementers to obtain this documentation
change before starting. The reference assets travel with the branch and become
available on `main` when its documentation PR merges. Existing GitHub issue/PR
links were verified. No external asset host is required.

Full enforcement/publishing/team/sharing functionality remains gated, and provider
gaps remain linked to capture owners. Older product defaults are explicitly
superseded for local raw capture; their runtime schema is not silently changed.

Next smallest implementation:
[#186 — Models destination](https://github.com/t4avenger/AI-Engineering-productivity/issues/186),
using existing performance readers before #161 links the new navigation.
