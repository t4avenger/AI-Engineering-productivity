# Reference-alignment evidence for #195

`reference-design.spec.ts` drives the local daemon with existing synthetic,
wire-shaped fixtures and records deterministic screenshots for the five primary
destinations. The test also maintains Playwright screenshot assertions, so later
visual changes require an intentional baseline review.

## Captures

- `overview`, `sessions`, `pull-requests`, `models`, and `governance` at
  1440×900, 1024×768, and 390×844.
- `sessions` and `governance` at 1680×945 for comparison with the two source
  images (whose native dimensions are 1672×941).

The browser scenario selects a retained conversation event to show the Session
Trace inspector and opens the Files & Paths editor on Governance. It uses
synthetic fixture values only. Screenshot text is evidence, not product data.

Playwright pins Chromium for the screenshot baseline. Its `system-ui` font still
varies slightly between supported Linux image versions, so the regression
assertion permits at most a 5% pixel difference. That bound is calibrated above
the observed 4% glyph-rasterisation difference on Ubuntu 24.04; layout, colour
and larger visual regressions remain test failures.

## Intentional deviations checklist

| Requirement IDs | Deliberate difference from source images | Reason |
|---|---|---|
| S01, S02 | TelemetryIQ replaces TraceLens branding. | Product identity. |
| T01, T04, T09 | No Share control, generated plan stages, model approval, or clean governance claims. | These require explicit retained evidence or E03; the UI shows availability/indeterminate states instead. |
| G01, G03–G05, G07 | No environment selector, Publish, Enforced/Blocked/approval claims, users/repositories, or pre-send prompt blocking. | Local detect-and-report is the accepted scope; E01–E03 remain gated. |
| G02, G06 | Rule counts and rows come only from saved configuration or observed synthetic evidence. | The source image's values are illustrative and must not become defaults. |
