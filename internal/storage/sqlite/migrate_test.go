package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wayne/telemetryiq/internal/storage"
)

// TestMigrationDropsProvenanceFromV2Database opens a database seeded at the v2
// (provenance-bearing) schema and asserts migration 3 rebuilds the events table
// without provenance_json while preserving existing rows and keeping queries
// working — the real upgrade path for epic #87's no-hiding reversal.
func TestMigrationDropsProvenanceFromV2Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")

	seedV2Database(t, path)

	repo, err := Open(path)
	if err != nil {
		t.Fatalf("open v2 database: %v", err)
	}
	defer func() { _ = repo.Close() }()

	if has, err := repo.eventsHasProvenance(ctx); err != nil || has {
		t.Fatalf("provenance_json column must be dropped after migration, has=%v err=%v", has, err)
	}

	var version int
	if err := repo.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d, want 3", version)
	}

	// The pre-existing row survives the rebuild and remains queryable.
	page, err := repo.ListEvents(ctx, storage.EventFilter{SessionID: "legacy-session", Limit: 10})
	if err != nil {
		t.Fatalf("list migrated events: %v", err)
	}
	if len(page) != 1 || page[0].EventID != "legacy-event" {
		t.Fatalf("migrated events = %#v", page)
	}
}

// seedV2Database creates a database with the historical v2 events table (which
// carried a provenance_json column) and one row, recorded at migration 2.
func seedV2Database(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seed database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, occurred_at TEXT NOT NULL, event_json BLOB NOT NULL, provenance_json BLOB);
CREATE INDEX events_session_occurred ON events(session_id, occurred_at, event_id);
CREATE TABLE sessions (session_id TEXT PRIMARY KEY, session_json BLOB NOT NULL);
CREATE TABLE cost_records (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, cost_json BLOB NOT NULL);
CREATE INDEX cost_records_session ON cost_records(session_id);
INSERT INTO schema_migrations(version) VALUES (1),(2);`); err != nil {
		t.Fatalf("seed v2 schema: %v", err)
	}

	eventJSON := `{"schema_version":"0.1.0","event_id":"legacy-event","event_type":"session.completed","occurred_at":"2026-01-02T10:00:00Z","received_at":"2026-01-02T10:00:00Z","provider":"openai","tool":"codex","source_schema":"otel","source_version":"test","actor_id":"unavailable","device_id":"unavailable","session_id":"legacy-session","privacy_level":"operational","attributes":{},"provider_extensions":{}}`
	if _, err := db.Exec(`INSERT INTO events(event_id,session_id,occurred_at,event_json,provenance_json) VALUES(?,?,?,?,?)`,
		"legacy-event", "legacy-session", "2026-01-02T10:00:00Z", eventJSON, `{"session_id":"hashed"}`); err != nil {
		t.Fatalf("seed v2 row: %v", err)
	}
}
