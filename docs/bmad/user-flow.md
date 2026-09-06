# User Flow: TelemetryIQ Local Dashboard

- **Project:** TelemetryIQ
- **Date:** 2026-09-06
- **Designer:** BMAD UX (Cursor)

---

## Flow Overview

**Flow Name:** Local orchestration audit dashboard

**Goal:** Unlock the local daemon UI, understand orchestration-tool usage from retained telemetry, inspect evidence, and delete data when finished.

**User Persona:** Local AI developer

**Entry Points:**
- Open `http://127.0.0.1:8080/` (or configured loopback addr)
- Deep link to `/sessions/{id}`, `/insights`, etc. (redirects to unlock if unauthenticated)

**Success Criteria:** Authenticated user can complete Home → Sessions → Detail and Insights journeys; destructive deletes require confirmation.

**Estimated Time:** Unlock &lt;1 min; browse session evidence 1–3 min

---

## Flow 1: Unlock

### Happy Path

```
[Start: GET / or protected route]
         |
         v
[Redirect: GET /unlock]
         |
         v
[Screen: Unlock form]
  • Password-style token field
  • Submit Unlock
         |
         v
[POST /unlock]
  • Validate token equals daemon management token
         |
        OK
         |
         v
[Set httpOnly cookie] → [302 GET /]
         |
         v
[End: Home]
```

### Alternate: Invalid token

```
[POST /unlock] → mismatch
         |
         v
[200 Unlock with role=alert error]
  • “Token rejected. Run make auth-token and try again.”
  • Field retains focus guidance; value cleared
```

### Alternate: Already unlocked

```
[GET /unlock] with valid cookie → [302 /]
```

---

## Flow 2: Home orchestration snapshot

```
[GET /]
         |
         v
[Load health + sessions + insight totals]
         |
         v
[Render Home]
  • Sessions today outcome counts
  • Tools observed (or Awaiting telemetry)
  • Insight highlights with links
  • No cost headline
```

**Empty state:** No sessions → short guidance to send OTLP to the daemon; tools/insights say awaiting/unavailable honestly.

**Error state:** Partial failure → alert for failed section; other sections still render when possible.

---

## Flow 3: Sessions → detail → delete

### Happy Path

```
[GET /sessions]
         |
         v
[User opens session row → GET /sessions/{id}]
         |
         v
[Detail: availability + timeline page 1]
         |
         v
[Optional: HTMX GET timeline?cursor=… → append]
         |
         v
[Optional: open provenance for event]
         |
         v
[User: Delete this session]
         |
         v
[Confirm dialog / confirm form]
         |
         v
[POST /sessions/{id}/delete] → [302 /sessions]
```

### Cancel delete

```
[Confirm] → Cancel → remain on detail; no mutation
```

### 401 / missing session

```
Missing → 404 page with link back to Sessions
401 cookie → redirect Unlock
```

---

## Flow 4: Insights (educate)

```
[GET /insights]
         |
         v
[MCP inventory section]
  • totals, servers, notes
         |
         v
[Skill usage section]
  • skills, coverage, notes
```

**Unavailable:** Provider without skill identity shows coverage `unavailable`, never a silent zero skill count presented as “none used.”

---

## Flow 5: Integrations

```
[GET /integrations]
         |
         v
[Derive distinct tool/provider from sessions]
         |
    empty?
   /       \
 Yes        No
  |          |
  v          v
Awaiting   List observed tools only
telemetry  (no false “connected” claims)
```

---

## Flow 6: Privacy bulk delete

```
[GET /privacy]
         |
         v
[Read-only defaults copy]
         |
         v
[User opens DELETE ALL]
         |
         v
[Typed confirmation: must match DELETE ALL]
         |
    match?
   /      \
 No        Yes
  |         |
  v         v
Error     POST /privacy/delete-all
alert       |
            v
        [302 /sessions or / with empty state]
```

---

## Flow 7: Costs (secondary)

```
[GET /costs]
         |
         v
[Cost summary statuses]
  • Calculated estimates where known
  • Unknown never rendered as zero currency
```

No entry from Home primary CTAs.

---

## Edge States (global)

| State | Behaviour |
|-------|-----------|
| Daemon down | Unlock/Home may fail health; show unhealthy text |
| Cookie expired / wrong token | Redirect unlock; clear cookie |
| HTMX request without JS | Full-page fallback links still work for primary nav |
| Partial insight API failure | Section error alert; other section still shown |

---

## Accessibility checkpoints for flows

- Unlock error announced via `role="alert"`
- Confirm dialogs keyboard-dismissible (Escape) when using `<dialog>`
- Delete All requires typed phrase, not click-only
- Session table and insight tables have column headers
