# ADR 0001: Use Mantine for the local dashboard component layer

## Status

Accepted — 2026-08-09

## Context

The React/Vite dashboard had hand-built forms, cards, buttons, and destructive
confirmation dialogs. That made the local-token setup and retained-telemetry
deletion journeys visually inconsistent and increased the accessibility burden
of maintaining interactive components ourselves.

## Decision

Use Mantine Core and Hooks as the dashboard component layer. Keep React and
Vite, the existing local API client, and the local-only privacy architecture.

Mantine supplies the provider, card, password input, buttons, text, badge, and
modal primitives. The dashboard retains its own content and privacy wording;
Mantine does not add analytics, network services, or persisted user data.

## Consequences

- The dashboard gets consistent, responsive, accessible primitives and managed
  modal behaviour.
- Tests must render Mantine components under `MantineProvider` and provide a
  `matchMedia` test shim.
- The package adds a maintained frontend dependency surface that must be kept
  current and audited with the existing security gates.
