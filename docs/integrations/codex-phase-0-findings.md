# Codex Phase 0 Findings

The live synthetic run confirmed Codex 0.145.0 exports OTLP JSON logs to the
loopback receiver. It exposed operational attributes for model and token usage,
plus sensitive account, email, host, and conversation identifiers.

The development inspector sanitizes OTLP attribute-pair values before
in-memory inspection. Regression tests cover the observed sensitive categories.
Raw payloads are never persisted, and the checked-in observed fixture replaces
all retained values with synthetic equivalents.

The initial normalizer supports trace fixtures only; the observed log fixture
is Phase 0 evidence, not a claim that logs have Phase 1 canonical support.
