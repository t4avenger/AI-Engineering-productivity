# Schema compatibility

TelemetryIQ canonical schemas use JSON Schema Draft 2020-12. Each schema has a stable identifier and requires `schema_version` so a stored record can always be validated against the contract that produced it.

## Version 0.1.0

The MVP schemas are:

- `schemas/canonical-event.schema.json`
- `schemas/session.schema.json`
- `schemas/policy.schema.json`
- `schemas/config.schema.json`

Their synthetic valid and invalid fixtures live in `fixtures/schemas`. Fixtures contain no real prompts, responses, source code, credentials, filenames, or command arguments.

## Evolution rules

- A schema version is immutable once records have been persisted or exported.
- A compatible evolution may add an optional canonical field only when older readers can safely ignore it. Old schemas remain available for historical records.
- Removing a field, changing a field type or meaning, making an optional field required, or changing an allowed value is breaking and requires a new schema version plus an explicit migration plan.
- Canonical envelope fields are closed (`additionalProperties: false`) to catch accidental contract drift. Provider-specific and unknown observed fields must be preserved under `provider_extensions`; they must not be silently discarded or promoted to invented canonical fields.
- A consumer that does not support a record's schema version must report it as unsupported or unknown. It must not coerce missing or unknown values to zero.

## Validation

`internal/schema/schema_test.go` compiles every schema and validates every fixture. Valid fixtures must pass; invalid fixtures must fail. Adapter and storage work added in later tasks must add conformance fixtures whenever a canonical contract changes.
