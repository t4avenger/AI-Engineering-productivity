# SQLite Persistence

Task 008 stores canonical events through a storage-agnostic repository
interface. The SQLite implementation stores the canonical event JSON verbatim —
epic #87 removed the storage-side privacy sanitizer, so raw provider-native IDs,
paths, and commands are persisted as normalised. Event IDs are unique, so replay
is idempotent. Sessions are rebuilt from timestamp-ordered events on write;
unsupported lifecycle data is explicitly unknown.

## Local database role

SQLite remains the local-only store for ingestion, application state, recent raw
telemetry, derived sessions, and cost records. DuckDB / PostgreSQL / ClickHouse
are not part of the individual-install MVP; optional analytics or enterprise
backends stay a later product track.

## Connection pragmas

On-disk databases open with WAL mode, `synchronous=NORMAL`, and
`busy_timeout=5000` (modernc URI `_pragma` parameters). The pool stays at
`MaxOpenConns(1)` until a multi-connection soak is justified on SQLite ≥ 3.51.3.
In-memory test databases skip WAL and only set `busy_timeout`.

## Schema notes

Migration version 1 creates event and session tables. Migration version 3
rebuilds the events table without the legacy `provenance_json` column, copying
existing rows across so they remain queryable (see `migrate_test.go`). Migration
6 denormalises session list columns (`started_at`, `tool`, `state`,
`identity_scope`, `model`) and cost summary columns (`currency`, `status`,
`amount_microusd`) with compound indexes so dashboard list/filter/order queries
do not `json_extract`-scan the whole table. Deleting a session removes its
events and reconstructed session in one transaction.

## Retention

`storage.retention_days` (default 30) is enforced at daemon start and hourly:
sessions with `started_at` older than the cutoff are deleted with cascaded
events, costs, operations, and agent relations. Sessions with a NULL
`started_at` are retained.
