# Phase 9 technical planning: observed skills and service degradation

## Status

Planning only. No collection, persistence, dashboard, score, alert, or policy
behaviour is authorised by this document.

## Purpose

Determine whether TelemetryIQ can safely and accurately report two currently
unscheduled capabilities:

1. observed skill usage from supported AI coding tools; and
2. service degradation under high-context workloads.

The work must preserve the local-first privacy invariants. In particular,
prompts, responses, source code, raw command arguments, and sensitive values
must not be retained merely to support either capability.

## Scope and non-goals

In scope:

- evidence capture, sanitisation review, and fixture versioning;
- provider capability assessment for skill evidence;
- measurement design for context pressure and service degradation; and
- implementation-ready requirements, test contracts, and decision records.

Out of scope:

- implementing collection, persistence, UI, alerts, policies, or scores;
- inferring skills from prompt content, filenames, command arguments, or model
  behaviour;
- claiming that a large context caused degraded service; and
- developer ranking or productivity scoring.

## Functional requirements

FR-1 — The discovery process shall record tool and provider versions for every
sanitised fixture and identify the fixture origin.

FR-2 — The capability matrix shall report skill evidence independently from
MCP, tool invocation, token, cache, latency, error, retry, and availability
evidence using the states `supported`, `partial`, `unsupported`, `unknown`, or
`version-dependent`.

FR-3 — The plan shall specify a field-level retention decision and provenance
for every candidate field before it can be persisted.

FR-4 — A skill may be reported only from an explicit provider field or event.
Missing, ambiguous, or inferred data shall be represented as unavailable.

FR-5 — The service-degradation measurement shall define its baseline workload,
comparison window, thresholds, confidence, and explanatory evidence.

FR-6 — A high-context observation shall be reported separately from service
degradation. It may use observed input-token growth or cache ratio, but shall
not imply causation.

## Non-functional requirements

NFR-1 — Sanitisation shall occur before fixture storage, diagnostics, logging,
or persistence, and tests shall use synthetic values only.

NFR-2 — Every approved metric shall have deterministic golden fixtures for
normal, high-context, delayed, error, retry, and unavailable-data cases.

NFR-3 — Any future displayed degradation result shall retain enough safe
evidence to reproduce its calculation locally.

## Discovery work and decision gates

1. Capture and sanitise representative supported-tool fixtures. Reject likely
   secrets and retain no content fields.
2. Map observed fields to canonical capabilities and record all unknown fields
   in sanitised provider extensions.
3. Assess whether skill identity is explicit, stable, and privacy-safe. If not,
   retain no skill record and mark the capability unsupported or unknown.
4. Define candidate measurements: request duration distribution, error rate,
   retry rate, availability, input-token growth, and cached-token ratio.
5. Create synthetic workload fixtures and calculate candidate baselines without
   making causal claims.
6. Produce an ADR covering field retention, session correlation, metric
   semantics, uncertainty wording, and threshold governance.
7. Obtain product, privacy, and architecture approval before creating an
   implementation story.

## Acceptance criteria for planning completion

- Each candidate capability has versioned sanitised fixtures, expected
  canonical output, expected warnings, and a documented retention decision.
- The capability matrix identifies exactly which tools and versions support
  skill evidence and degradation inputs.
- The measurement specification describes baselines and confidence and proves
  that unknown values are never converted to zero or healthy status.
- An ADR and implementation story exist; implementation starts only after the
  required approvals.

## Architecture handoff

Next intent: `bmad:architecture` to review the canonical-model, privacy, and
measurement implications after evidence capture is complete.
