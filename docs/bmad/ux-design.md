# UX Design Document

- **Project:** TelemetryIQ Local Dashboard
- **Date:** 2026-09-06
- **Designer:** BMAD UX (Cursor)
- **Version:** 1.0
- **Stack target:** Go `html/template` + HTMX (same-origin daemon)

---

## Design Overview

### Project Summary

Replace the React/Mantine SPA with a server-rendered local dashboard that helps a developer **audit and educate themselves** on how they use AI orchestration tools (Claude Code, Codex, Cursor): which tools/models/MCP servers/skills run, whether context is wasted, and what evidence backs each claim—without implying cost is the primary product story.

### Design Goals

1. Make orchestration behaviour the Home headline (sessions, tools, insights)—never cost.
2. Keep every screen simple: one job, real URLs, honest `unavailable` / `unknown` labels (never zero for missing data).
3. Prefer progressive enhancement (HTML forms + HTMX partials) over a client SPA.

### Success Metrics

- Primary journeys completable without a JS framework beyond HTMX.
- WCAG 2.2 AA for landmarks, focus, labels, and destructive confirms.
- Live Playwright e2e: ingest → visible session/insight (no API mocks for that gate).
- Initial usable Home render under 2s on the reference machine (server-rendered HTML).

### Design Principles Applied

- **Evidence before scoring:** every insight links to sessions/events when data exists.
- **Honest availability:** missing signals use explicit availability vocabulary.
- **Local-first privacy:** unlock token never logged; cookie is httpOnly on loopback; Privacy page documents defaults only.
- **One job per section:** no card grids of competing metrics on Home.

### Target Devices

- [x] Desktop (1024px+) — primary
- [x] Tablet (768px - 1023px)
- [x] Mobile (320px - 767px) — usable, stacked nav
- [ ] Native app

---

## User Personas

### Primary Persona: Local AI developer

**Demographics:**
- Occupation: Individual developer using one or more AI coding agents
- Tech savviness: High
- Context: Runs TelemetryIQ daemon on loopback only

**Goals:**
- See which orchestration tools/models/MCPs/skills they actually use
- Spot wasted context (connected-but-unused MCP)
- Inspect evidence for a session and delete retained telemetry when done

**Pain Points:**
- Opaque agent behaviour across tools
- Dashboards that invent zeros or claim parity the capability matrix does not support
- Heavy SPA setup for a local-only utility

---

## Information Architecture

| Route | Screen | One job |
|-------|--------|---------|
| `GET /unlock` | Unlock | Paste `make auth-token` once |
| `GET /` | Home | Orchestration snapshot |
| `GET /sessions` | Sessions | Find and open sessions |
| `GET /sessions/{id}` | Session detail | Evidence + delete |
| `GET /insights` | Insights | Educate on MCP + skills |
| `GET /integrations` | Integrations | Observed tools only |
| `GET /privacy` | Privacy | Defaults + DELETE ALL |
| `GET /costs` | Costs | Secondary estimates only |

Nav order: Home · Sessions · Insights · Integrations · Privacy · Costs (Costs last).

---

## User Flows (summary)

See [user-flow.md](user-flow.md) for full paths. Primary flows:

1. Unlock → Home
2. Browse sessions → detail → timeline load-more → delete
3. Review MCP/skill insights
4. Typed confirmation bulk delete
5. Optional cost summary (never from Home CTA)

---

## Wireframes

### Unlock

```
┌─────────────────────────────────────────────────────────┐
│  TelemetryIQ                                            │
│  Local developer edition                                │
├─────────────────────────────────────────────────────────┤
│  Unlock local dashboard                                 │
│  Run make auth-token and paste the token below.         │
│  Token stays in an httpOnly cookie for this browser     │
│  session on loopback only.                              │
│                                                         │
│  Token [________________________]  (password input)     │
│  [ Unlock ]                                             │
│                                                         │
│  (error region if invalid)                              │
└─────────────────────────────────────────────────────────┘
```

### Home

```
┌─────────────────────────────────────────────────────────┐
│ TelemetryIQ Local          Daemon: Healthy              │
│ Home Sessions Insights Integrations Privacy             │
├─────────────────────────────────────────────────────────┤
│ Orchestration overview                                  │
│ Privacy mode: local-only · Retention: 30 days           │
│                                                         │
│ Sessions today                                          │
│   Successful N · Failed N · Abandoned N · Other N       │
│   (counts from retained sessions; empty → guidance)     │
│                                                         │
│ Tools observed                                          │
│   · Codex · Claude Code … or “Awaiting telemetry”       │
│                                                         │
│ Insight highlights                                      │
│   · MCP: N unused connected servers (link Insights)     │
│   · Skills: N observed (link Insights)                  │
│   · Honest note if insight data unavailable             │
│                                                         │
│ Cost is not shown here.                                 │
└─────────────────────────────────────────────────────────┘
```

### Sessions

```
┌─────────────────────────────────────────────────────────┐
│ Sessions                                                │
│ Newest first. Open a row for evidence.                  │
│                                                         │
│ ┌ tool │ provider │ state │ started │ model avail. ──┐ │
│ │ …    │ …        │ …     │ …       │ observed|…     │ │
│ └────────────────────────────────────────────────────┘ │
│ Empty: “No retained sessions yet.”                      │
└─────────────────────────────────────────────────────────┘
```

MVP filters: document date/tool/provider as later if API query params are not yet exposed in HTML; list uses existing limit API.

### Session detail

```
┌─────────────────────────────────────────────────────────┐
│ ← Sessions                                              │
│ Session {id}                                            │
│ Availability table (field → state)                      │
│ Timeline (HTMX load more)                               │
│ Provenance expand per event                             │
│ [ Delete this session ] → confirm dialog                │
└─────────────────────────────────────────────────────────┘
```

### Insights

```
┌─────────────────────────────────────────────────────────┐
│ Insights — learn from retained telemetry                │
│ MCP inventory                                           │
│   Totals + server rows (used / unused / waste states)   │
│   Token context labelled “not exact per-MCP allocation” │
│ Skill usage                                             │
│   Skills + detection coverage per provider/tool         │
│ Notes / evidence limits                                 │
└─────────────────────────────────────────────────────────┘
```

### Integrations / Privacy / Costs

- **Integrations:** tools derived only from retained sessions; else “Awaiting telemetry”.
- **Privacy:** static enforced defaults + typed `DELETE ALL` confirm (must type phrase).
- **Costs:** aggregate summary and statuses; unknown never shown as `$0`.

---

## Component Specifications

| Component | Behaviour |
|-----------|-----------|
| App shell | Skip link, header (brand + health), nav with `aria-current="page"`, `<main>` |
| Availability badge | Text label for `observed\|partial\|unavailable\|unsupported\|unknown` |
| Flash / error | `role="alert"` region after failed unlock or mutations |
| Confirm delete | Native `<dialog>` or HTMX swap with confirm form; focus trap via dialog |
| Load more | `hx-get` partial append into timeline list |
| Unlock form | `POST /unlock` → Set-Cookie → redirect `/` |

### HTMX interaction rules

- Full navigation: normal links (real URLs, back button works).
- Partial updates: timeline pages, delete success → redirect or swap list.
- No CDN: HTMX and CSS served as static files from the daemon.
- Forms use POST + Redirect-GET for unlock and deletes.

---

## Accessibility Annotations

- Target: **WCAG 2.2 AA**
- Landmarks: `header`, `nav`, `main`; skip link first focusable
- Headings: one `h1` per page; nested `h2` for sections
- Focus: visible 3px outline on interactive controls
- Auth token field: labelled, `autocomplete="off"`, type password
- Destructive actions: explicit confirm; typed phrase for bulk delete
- Colour not sole status cue (text + class)
- Tables: `<th scope="col">` for session/insight grids

---

## Responsive Behavior

- **≥1024px:** max-width content ~1120px, horizontal nav
- **768–1023px:** same stack, tighter padding
- **&lt;768px:** nav wraps; tables scroll horizontally in a region with accessible name

---

## Design Tokens

| Token | Value |
|-------|-------|
| `--color-text` | `#172026` |
| `--color-muted` | `#53616b` |
| `--color-bg` | `#f6f8fa` |
| `--color-border` | `#d8dee4` |
| `--color-accent` | `#0969da` |
| `--color-ok` | `#087443` |
| `--color-danger` | `#cf222e` |
| Font | system-ui, ui-sans-serif stack (no Inter dependency) |
| Focus ring | 3px solid accent, 3px offset |

Avoid purple gradients, cream/serif marketing looks, and decorative card chrome. Prefer plain sections and tables.

---

## Developer Handoff Notes

1. Implement screens as Go templates; reuse JSON domain types from session/insight readers—do not duplicate business rules in templates.
2. HTML auth: httpOnly cookie after unlock; JSON `/api/v1/*` retains Bearer for tests/machine clients.
3. Preserve privacy copy from current Privacy page; configuration remains file-based (not editable in UI).
4. Home must not surface cost aggregates.
5. Playwright targets the daemon origin (single process).
6. Replace React `web/` SPA entirely after parity.
