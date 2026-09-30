# Codex Phase 0 Findings

The live synthetic run confirmed Codex 0.145.0 exports OTLP JSON logs to the
loopback receiver. It exposed operational attributes for model and token usage,
plus account, email, host, and provider conversation identifiers. The current
local-only policy retains every emitted field raw. Recognized operator and
environment identity is also projected into a present-only environment block;
provider conversation identifiers use a stable `codex:` prefix for correlation.

The opt-in development inspector is a raw local troubleshooting view. The
authenticated diagnostic preview/export is a separate metadata-only boundary
that never reads retained event values. Checked-in observed fixtures replace
all real values with reviewed synthetic equivalents.

The Codex normaliser retains complete raw log/resource/metric/span evidence and
projects only fixture-backed stable semantics. Absence of a canonical projection
does not authorize dropping provider evidence.
