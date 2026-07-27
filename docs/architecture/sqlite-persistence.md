# SQLite Persistence

Task 008 stores canonical events through a storage-agnostic repository
interface. The SQLite implementation runs the privacy sanitizer before every
write and stores provenance beside safe event JSON. Event IDs are unique, so
replay is idempotent. Sessions are rebuilt from timestamp-ordered events;
unsupported lifecycle data is explicitly unknown.

Migration version 1 creates event and session tables. Deleting a session
removes its events and reconstructed session in one transaction.
