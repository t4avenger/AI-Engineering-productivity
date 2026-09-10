# SQLite Persistence

Task 008 stores canonical events through a storage-agnostic repository
interface. The SQLite implementation stores the canonical event JSON verbatim —
epic #87 removed the storage-side privacy sanitizer, so raw provider-native IDs,
paths, and commands are persisted as normalised. Event IDs are unique, so replay
is idempotent. Sessions are rebuilt from timestamp-ordered events; unsupported
lifecycle data is explicitly unknown.

Migration version 1 creates event and session tables. Migration version 3
rebuilds the events table without the legacy `provenance_json` column, copying
existing rows across so they remain queryable (see `migrate_test.go`). Deleting a
session removes its events and reconstructed session in one transaction.
