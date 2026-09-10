# Ingest Pipeline

TelemetryIQ is local-only. Epic #87 (issue #88) removed ingest-time hiding: provider payloads
are normalised and persisted with their raw provider-native identifiers, file paths, and command
lines intact, and those raw values are shown to the local user.

## Transformations

- Raw provider payloads decode into a generic map and are passed straight to the provider
  normalisers — there is no sanitiser choke point and no HMAC/redaction pass.
- Provider-native session/conversation/request IDs and MCP server names are retained raw with
  stable provider prefixes (`codex:`, `claude-code:`, `cursor-agent:`). When a native ID is
  absent, a non-keyed positional/content ID is used for uniqueness only.
- File paths and command lines are retained verbatim. `privacy.ClassifyPath` /
  `privacy.ClassifyCommandAccess` run over those raw values to add a governance class + project
  boundary signal; they do not replace the value.
- Prompt/response/source-code content is not captured by default: the normalisers declare it
  unavailable and refuse content at the ingest boundary. Configurable capture is tracked in #94
  and gated by `collection.prompts` / `collection.responses` / `collection.source_code`.
- Other fields are retained as operational metadata, including unknown provider-extension fields.

## Storage

SQLite stores the canonical event verbatim — no storage-side sanitiser. Migration 3 rebuilds the
`events` table without the legacy `provenance_json` column (existing rows are copied across and
remain queryable); see `internal/storage/sqlite/sqlite.go` and `migrate_test.go`.

## Verification

Raw-survival tests seed synthetic identifiers, file paths, and command lines and assert they
reach storage and read APIs verbatim, with no `hmac-`/`path-class:`/`command-access:` placeholder.
Governance tests assert risky-access findings carry the raw path/command as evidence. Content
tests assert prompt/response/source-code content is not captured by default.
