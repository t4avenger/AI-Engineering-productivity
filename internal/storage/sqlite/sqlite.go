// Package sqlite implements the local SQLite repository.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Register the pure-Go SQLite driver with database/sql.
	_ "modernc.org/sqlite"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/storage"
)

type Repository struct {
	db         *sql.DB
	calculator *cost.Calculator
}

// memoryDBPath is the SQLite in-memory DSN used by tests and ephemeral opens.
const memoryDBPath = ":memory:"

func Open(path string, calculators ...*cost.Calculator) (*Repository, error) {
	if path != memoryDBPath {
		directory := filepath.Dir(path)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if directory != "." {
			if err := os.Chmod(directory, 0o700); err != nil {
				return nil, fmt.Errorf("secure database directory: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", openDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	r := &Repository{db: db}
	if len(calculators) > 0 {
		r.calculator = calculators[0]
	}
	if err := r.applyConnectionPragmas(context.Background(), path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := r.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != memoryDBPath {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("secure database file: %w", err)
		}
	}
	return r, nil
}

// openDSN attaches modernc pragma URI parameters for on-disk databases. In-memory
// databases skip WAL (unsupported) but still get busy_timeout via Exec.
func openDSN(path string) string {
	if path == memoryDBPath {
		return path
	}
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

func (r *Repository) applyConnectionPragmas(ctx context.Context, path string) error {
	if path == memoryDBPath {
		if _, err := r.db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
			return fmt.Errorf("set busy_timeout: %w", err)
		}
		return nil
	}
	var journal string
	if err := r.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, "wal") {
		return fmt.Errorf("journal_mode=%q, want wal", journal)
	}
	return nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) migrate(ctx context.Context) error {
	// Fresh databases get the current events shape directly (no provenance_json);
	// existing v2 databases keep their provenance-bearing table here and have it
	// rebuilt by migration 3 below.
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, occurred_at TEXT NOT NULL, event_json BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS events_session_occurred ON events(session_id, occurred_at, event_id);
CREATE TABLE IF NOT EXISTS sessions (session_id TEXT PRIMARY KEY, session_json BLOB NOT NULL); CREATE TABLE IF NOT EXISTS cost_records (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, cost_json BLOB NOT NULL); CREATE INDEX IF NOT EXISTS cost_records_session ON cost_records(session_id); CREATE TABLE IF NOT EXISTS operations (session_id TEXT NOT NULL, operation_id TEXT NOT NULL, operation_json BLOB NOT NULL, PRIMARY KEY(session_id, operation_id)); CREATE INDEX IF NOT EXISTS operations_session ON operations(session_id);`)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	_, err = r.db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (1)")
	if err != nil {
		return fmt.Errorf("record migration 1: %w", err)
	}
	if _, err = r.db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (2)"); err != nil {
		return fmt.Errorf("record migration 2: %w", err)
	}
	if err := r.dropEventProvenance(ctx); err != nil {
		return err
	}
	if err := r.ensureOperationsTable(ctx); err != nil {
		return err
	}
	if err := r.ensureAgentRelationsTable(ctx); err != nil {
		return err
	}
	if err := r.ensureListAndCostColumns(ctx); err != nil {
		return err
	}
	if err := r.ensureInsightSignals(ctx); err != nil {
		return err
	}
	return nil
}

// dropEventProvenance is migration 3: it removes the ingest-time-hiding
// provenance surface (epic #87) by rebuilding the events table without the
// provenance_json column on any database that still carries it. Fresh databases
// already have the current shape, so the rebuild is skipped and only the
// migration version is recorded.
func (r *Repository) dropEventProvenance(ctx context.Context) error {
	hasColumn, err := r.eventsHasProvenance(ctx)
	if err != nil {
		return err
	}
	// Rebuild and version bump run in one transaction so a failure or crash
	// mid-migration cannot leave the events table half-swapped (SQLite applies
	// DDL transactionally).
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 3: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if hasColumn {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE events_new (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, occurred_at TEXT NOT NULL, event_json BLOB NOT NULL);
INSERT INTO events_new(event_id,session_id,occurred_at,event_json) SELECT event_id,session_id,occurred_at,event_json FROM events;
DROP TABLE events;
ALTER TABLE events_new RENAME TO events;
CREATE INDEX IF NOT EXISTS events_session_occurred ON events(session_id, occurred_at, event_id);`); err != nil {
			return fmt.Errorf("rebuild events without provenance: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (3)"); err != nil {
		return fmt.Errorf("record migration 3: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 3: %w", err)
	}
	return nil
}

// eventsHasProvenance reports whether the events table still carries the
// removed provenance_json column.
func (r *Repository) eventsHasProvenance(ctx context.Context) (bool, error) {
	rows, err := r.db.QueryContext(ctx, "PRAGMA table_info(events)")
	if err != nil {
		return false, fmt.Errorf("inspect events schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan events schema: %w", err)
		}
		if name == "provenance_json" {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

func (r *Repository) ensureOperationsTable(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 4: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS operations (session_id TEXT NOT NULL, operation_id TEXT NOT NULL, operation_json BLOB NOT NULL, PRIMARY KEY(session_id, operation_id));
CREATE INDEX IF NOT EXISTS operations_session ON operations(session_id);`); err != nil {
		return fmt.Errorf("create operations table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (4)"); err != nil {
		return fmt.Errorf("record migration 4: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 4: %w", err)
	}
	return nil
}

// ensureAgentRelationsTable is migration 5: it creates the derived
// agent_relations table (#102) that holds the reconstructed sub-agent tree. The
// row is keyed by (session_id, trace_id, agent_id) — an agent_id is unique only
// within its trace — and rebuildSession REPLACEs a session's rows from its full
// event set, so the table stays complete and idempotent across OTLP batches.
func (r *Repository) ensureAgentRelationsTable(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 5: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS agent_relations (session_id TEXT NOT NULL, trace_id TEXT NOT NULL, agent_id TEXT NOT NULL, relation_json BLOB NOT NULL, PRIMARY KEY(session_id, trace_id, agent_id));
CREATE INDEX IF NOT EXISTS agent_relations_session ON agent_relations(session_id);`); err != nil {
		return fmt.Errorf("create agent_relations table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (5)"); err != nil {
		return fmt.Errorf("record migration 5: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 5: %w", err)
	}
	return nil
}

// ensureListAndCostColumns is migration 6: denormalizes session list filters and
// cost summary fields so dashboard queries avoid json_extract full-table sorts
// and in-process aggregation over every cost row. Already-applied migrations are
// skipped so Open does not re-scan sessions/cost_records on every restart.
func (r *Repository) ensureListAndCostColumns(ctx context.Context) error {
	applied, err := r.migrationApplied(ctx, 6)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 6: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := migrateSessionListColumns(ctx, tx); err != nil {
		return err
	}
	if err := migrateCostSummaryColumns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (6)"); err != nil {
		return fmt.Errorf("record migration 6: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 6: %w", err)
	}
	return nil
}

func (r *Repository) migrationApplied(ctx context.Context, version int) (bool, error) {
	var recorded int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version=?", version).Scan(&recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return true, nil
}

func migrateSessionListColumns(ctx context.Context, tx *sql.Tx) error {
	if err := addMissingColumns(ctx, tx, "sessions", [][2]string{
		{"started_at", "ALTER TABLE sessions ADD COLUMN started_at TEXT"},
		{"tool", "ALTER TABLE sessions ADD COLUMN tool TEXT"},
		{"state", "ALTER TABLE sessions ADD COLUMN state TEXT"},
		{"identity_scope", "ALTER TABLE sessions ADD COLUMN identity_scope TEXT"},
		{"model", "ALTER TABLE sessions ADD COLUMN model TEXT"},
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET
started_at = COALESCE(started_at, json_extract(session_json, '$.started_at')),
tool = COALESCE(tool, json_extract(session_json, '$.tool')),
state = COALESCE(state, json_extract(session_json, '$.state')),
identity_scope = COALESCE(identity_scope, json_extract(session_json, '$.attributes.identity_scope'), CASE
	WHEN session_id GLOB 'codex-log:*' OR session_id GLOB 'codex:token:*' OR session_id GLOB 'codex:skill:*'
		OR session_id GLOB 'codex:skill-turn:*' OR session_id GLOB 'codex:trace:*' OR session_id GLOB 'cursor:token:*'
		OR session_id GLOB 'claude-code:trace:*' OR session_id GLOB '*:unknown' THEN 'observation'
	ELSE 'unknown' END),
model = COALESCE(model, json_extract(session_json, '$.attributes.model'))`); err != nil {
		return fmt.Errorf("backfill session list columns: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS sessions_started_id ON sessions(started_at DESC, session_id DESC);
CREATE INDEX IF NOT EXISTS sessions_tool_started ON sessions(tool, started_at DESC, session_id DESC);
CREATE INDEX IF NOT EXISTS sessions_state_started ON sessions(state, started_at DESC, session_id DESC);
CREATE INDEX IF NOT EXISTS sessions_scope_started ON sessions(identity_scope, started_at DESC, session_id DESC);
CREATE INDEX IF NOT EXISTS sessions_model_started ON sessions(model, started_at DESC, session_id DESC)`); err != nil {
		return fmt.Errorf("create session list indexes: %w", err)
	}
	return nil
}

func migrateCostSummaryColumns(ctx context.Context, tx *sql.Tx) error {
	if err := addMissingColumns(ctx, tx, "cost_records", [][2]string{
		{"currency", "ALTER TABLE cost_records ADD COLUMN currency TEXT"},
		{"status", "ALTER TABLE cost_records ADD COLUMN status TEXT"},
		{"amount_microusd", "ALTER TABLE cost_records ADD COLUMN amount_microusd INTEGER"},
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cost_records SET
currency = COALESCE(currency, json_extract(cost_json, '$.currency')),
status = COALESCE(status, json_extract(cost_json, '$.status')),
amount_microusd = COALESCE(amount_microusd, CAST(json_extract(cost_json, '$.amount_microusd') AS INTEGER))`); err != nil {
		return fmt.Errorf("backfill cost summary columns: %w", err)
	}
	return nil
}

func addMissingColumns(ctx context.Context, tx *sql.Tx, table string, columns [][2]string) error {
	for _, column := range columns {
		has, err := tableHasColumn(ctx, tx, table, column[0])
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := tx.ExecContext(ctx, column[1]); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, column[0], err)
		}
	}
	return nil
}

func tableHasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

func (r *Repository) SaveEvents(ctx context.Context, events []canonical.Event) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.saveEventsTx(ctx, tx, events); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) SaveEventsAndOperations(ctx context.Context, events []canonical.Event, operations []canonical.Operation) error {
	if len(events) == 0 && len(operations) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.saveEventsTx(ctx, tx, events); err != nil {
		return err
	}
	if err := r.saveOperationsTx(ctx, tx, operations); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) SaveOperations(ctx context.Context, operations []canonical.Operation) error {
	if len(operations) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.saveOperationsTx(ctx, tx, operations); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) saveEventsTx(ctx context.Context, tx *sql.Tx, events []canonical.Event) error {
	ids := map[string]struct{}{}
	for _, event := range events {
		if err := r.saveEventTx(ctx, tx, event); err != nil {
			return err
		}
		ids[event.SessionID] = struct{}{}
	}
	for id := range ids {
		if err := r.rebuildSession(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) saveEventTx(ctx context.Context, tx *sql.Tx, event canonical.Event) error {
	// Events are persisted verbatim — no ingest-time hiding is applied
	// (epic #87); raw provider-native identifiers, paths and commands reach
	// storage and the UI.
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO events(event_id,session_id,occurred_at,event_type,event_json) VALUES(?,?,?,?,?)", event.EventID, event.SessionID, event.OccurredAt.UTC().Format(timeFormat), event.EventType, payload)
	if err != nil {
		return err
	}
	return r.saveCostForInsertedEvent(ctx, tx, event, result)
}

func (r *Repository) saveCostForInsertedEvent(ctx context.Context, tx *sql.Tx, event canonical.Event, result sql.Result) error {
	if r.calculator == nil {
		return nil
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("determine event insertion: %w", err)
	}
	if inserted == 0 {
		return nil
	}
	return r.saveCostRecord(ctx, tx, r.calculator.Calculate(event))
}

func (r *Repository) saveOperationsTx(ctx context.Context, tx *sql.Tx, operations []canonical.Operation) error {
	for _, operation := range operations {
		if err := saveOperationTx(ctx, tx, operation); err != nil {
			return err
		}
	}
	return nil
}

// saveOperationTx inserts an operation, or — when a row for the same
// (session_id, operation_id) already exists — enriches the stored row with the
// incoming observation instead of discarding it. The same MCP call is observed on
// two ingest routes under one operation ID (#104): the OTLP tool_result carries
// timing and size counters, while the JSONL transcript carries the raw arguments
// and result. A plain INSERT OR IGNORE would let whichever landed first win and
// silently drop the other route's evidence — an epic #87 raw-capture violation.
// The merge is order-independent (both orderings converge to the union) and
// identical re-ingest is a no-op, preserving the idempotency other providers rely
// on.
func saveOperationTx(ctx context.Context, tx *sql.Tx, operation canonical.Operation) error {
	payload, err := json.Marshal(operation)
	if err != nil {
		return fmt.Errorf("marshal operation: %w", err)
	}
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO operations(session_id,operation_id,operation_json) VALUES(?,?,?)", operation.SessionID, operation.OperationID, payload)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("determine operation insertion: %w", err)
	}
	if inserted > 0 {
		return nil
	}
	var storedJSON []byte
	if err := tx.QueryRowContext(ctx, "SELECT operation_json FROM operations WHERE session_id=? AND operation_id=?", operation.SessionID, operation.OperationID).Scan(&storedJSON); err != nil {
		return fmt.Errorf("load stored operation: %w", err)
	}
	var stored canonical.Operation
	if err := json.Unmarshal(storedJSON, &stored); err != nil {
		return fmt.Errorf("decode stored operation: %w", err)
	}
	mergedJSON, err := json.Marshal(mergeOperationEvidence(stored, operation))
	if err != nil {
		return fmt.Errorf("marshal merged operation: %w", err)
	}
	if bytes.Equal(mergedJSON, storedJSON) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE operations SET operation_json=? WHERE session_id=? AND operation_id=?", mergedJSON, operation.SessionID, operation.OperationID); err != nil {
		return err
	}
	return nil
}

// mergeOperationEvidence unions an incoming observation into the stored operation
// for the same operation ID, never downgrading: a known outcome or category
// replaces an unset/unknown one, provenance rises toward observed, and
// provider_extensions are deep-merged so the incoming observation fills gaps
// (notably the raw mcp_call arguments/result the OTLP tool_result path cannot
// carry) without clobbering a value the stored row already proved.
func mergeOperationEvidence(stored, incoming canonical.Operation) canonical.Operation {
	merged := stored
	if merged.Outcome == "" || merged.Outcome == "unknown" {
		merged.Outcome = incoming.Outcome
	}
	if merged.Category == "" || merged.Category == canonical.OperationCategoryUnknown {
		merged.Category = incoming.Category
	}
	merged.Provenance = higherProvenance(stored.Provenance, incoming.Provenance)
	merged.ProviderExtensions = deepMergeMaps(stored.ProviderExtensions, incoming.ProviderExtensions)
	return merged
}

// higherProvenance returns the more authoritative of two provenance values
// (observed > inferred > unknown/unset), so a merge never weakens a proven signal.
func higherProvenance(a, b canonical.Provenance) canonical.Provenance {
	if provenanceRank(b) > provenanceRank(a) {
		return b
	}
	return a
}

func provenanceRank(p canonical.Provenance) int {
	switch p {
	case canonical.ProvenanceObserved:
		return 2
	case canonical.ProvenanceInferred:
		return 1
	default:
		return 0
	}
}

// deepMergeMaps returns a recursive union of two generic maps: the incoming map
// fills keys the base lacks and nested maps are merged, but a leaf value already
// set in the base is kept — the base is the stored row, so the route that landed
// first is never silently overwritten. A nil base yields the incoming map's keys.
func deepMergeMaps(base, incoming map[string]any) map[string]any {
	if base == nil && incoming == nil {
		return nil
	}
	merged := make(map[string]any, len(base)+len(incoming))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range incoming {
		existing, present := merged[key]
		if !present {
			merged[key] = value
			continue
		}
		existingMap, existingIsMap := existing.(map[string]any)
		incomingMap, incomingIsMap := value.(map[string]any)
		if existingIsMap && incomingIsMap {
			merged[key] = deepMergeMaps(existingMap, incomingMap)
		}
	}
	return merged
}

func (r *Repository) saveCostRecord(ctx context.Context, tx *sql.Tx, record cost.Record) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal cost record: %w", err)
	}
	var amount any
	if record.AmountMicrousd != nil {
		amount = *record.AmountMicrousd
	}
	if _, err = tx.ExecContext(ctx,
		"INSERT INTO cost_records(event_id,session_id,cost_json,currency,status,amount_microusd) VALUES(?,?,?,?,?,?)",
		record.EventID, record.SessionID, payload, record.Currency, record.Status, amount); err != nil {
		return fmt.Errorf("insert cost record: %w", err)
	}
	return nil
}

const (
	timeFormat                = "2006-01-02T15:04:05.999999999Z07:00"
	whereSessionIDClause      = " WHERE session_id=?"
	codexSessionPrefix        = "codex:"
	codexConversationIDSource = "conversation.id"
	identityScopeKey          = "identity_scope"
	identitySourceKey         = "identity_source"
	identityProvider          = "provider"
	identityObservation       = "observation"
	identityUnknown           = "unknown"
)

func (r *Repository) rebuildSession(ctx context.Context, tx *sql.Tx, id string) error {
	events, err := loadSessionEvents(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	session := reconstructSession(id, events)
	if last := lastEventAt(events); !last.IsZero() {
		session.Attributes["last_event_at"] = last.UTC().Format(time.RFC3339Nano)
	}
	if err := upsertSession(ctx, tx, session, lastEventAt(events)); err != nil {
		return err
	}
	if err := rebuildInsightSignals(ctx, tx, id, events); err != nil {
		return err
	}
	return rebuildAgentRelations(ctx, tx, id, events)
}

// rebuildAgentRelations re-derives the session's sub-agent tree (#102) from its
// full event set and REPLACEs the stored rows (DELETE then INSERT), so a rollup
// is complete and idempotent no matter how the trace's spans were split across
// OTLP batches — an INSERT OR IGNORE would let a first partial row win and
// undercount. Only claude-code sessions carry sub-agent spans; other tools skip
// the reconstruction but still clear any stale rows.
func rebuildAgentRelations(ctx context.Context, tx *sql.Tx, id string, events []canonical.Event) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM agent_relations WHERE session_id=?", id); err != nil {
		return fmt.Errorf("clear agent relations: %w", err)
	}
	if events[0].Tool != "claude-code" {
		return nil
	}
	for _, relation := range claude.ReconstructSubAgentRelations(events) {
		payload, err := json.Marshal(relation)
		if err != nil {
			return fmt.Errorf("marshal agent relation: %w", err)
		}
		// Key the row on the authoritative id being rebuilt (the same value the
		// DELETE above cleared), not relation.SessionID, so a malformed relation
		// can never insert rows for a different session than the one in flight.
		if _, err := tx.ExecContext(ctx, "INSERT INTO agent_relations(session_id,trace_id,agent_id,relation_json) VALUES(?,?,?,?)", id, relation.TraceID, relation.AgentID, payload); err != nil {
			return fmt.Errorf("insert agent relation: %w", err)
		}
	}
	return nil
}

func loadSessionEvents(ctx context.Context, tx *sql.Tx, id string) ([]canonical.Event, error) {
	rows, err := tx.QueryContext(ctx, "SELECT event_json FROM events WHERE session_id=? ORDER BY occurred_at,event_id", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []canonical.Event
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var e canonical.Event
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func reconstructSession(id string, events []canonical.Event) canonical.Session {
	first := events[0]
	session := canonical.Session{SchemaVersion: first.SchemaVersion, SessionID: id, Provider: first.Provider, Tool: first.Tool, State: "unknown", StartedAt: first.OccurredAt, Attributes: map[string]any{"event_count": len(events)}, ProviderExtensions: map[string]any{}}
	for _, e := range events {
		if _, observed := session.Attributes["model"]; !observed {
			if model, ok := e.Attributes["model"].(string); ok && model != "" {
				session.Attributes["model"] = model
			}
		}
		attachSessionEnvironment(&session, e)
		state := lifecycle(e.EventType)
		if state != "" {
			session.State = state
			if terminal(state) {
				at := e.OccurredAt
				session.CompletedAt = &at
			} else {
				session.CompletedAt = nil
			}
		}
	}
	attachSessionPRLink(&session, events)
	scope, source := sessionIdentity(events)
	session.Attributes[identityScopeKey] = scope
	session.Attributes[identitySourceKey] = source
	return session
}

func attachSessionPRLink(session *canonical.Session, events []canonical.Event) {
	links := map[string]struct{}{}
	for _, event := range events {
		for _, link := range stringSliceAttribute(event.Attributes["pr_link_candidates"]) {
			links[link] = struct{}{}
		}
	}
	if len(links) == 1 {
		for link := range links {
			session.Attributes["pr_link"] = link
		}
	} else if len(links) > 1 {
		session.Attributes["pr_link_candidate_count"] = len(links)
	}
}

func stringSliceAttribute(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func sessionIdentity(events []canonical.Event) (string, string) {
	if len(events) == 0 {
		return identityUnknown, "unproven"
	}
	first := events[0]
	id := first.SessionID
	if scope, source, ok := observationIdentity(id); ok {
		return scope, source
	}
	switch {
	case first.Tool == "codex" && hasCodexLogSessionID(first):
		return identityProvider, codexConversationIDSource
	case first.Tool == "claude-code" && strings.HasPrefix(id, "claude-code:"):
		return identityProvider, "session.id"
	case first.Tool == "cursor-agent" && strings.HasPrefix(id, "cursor-agent:"):
		return identityProvider, "session_id"
	case first.Tool == "cursor" && strings.HasPrefix(id, "cursor:"):
		return identityProvider, "cursor.conversation.id"
	default:
		return identityUnknown, "unproven"
	}
}

func observationIdentity(id string) (string, string, bool) {
	switch {
	case strings.HasPrefix(id, "codex-log:"), strings.HasPrefix(id, "codex:token:"),
		strings.HasPrefix(id, "codex:skill:"), strings.HasPrefix(id, "codex:skill-turn:"),
		strings.HasPrefix(id, "cursor:token:"):
		return identityObservation, "content-derived", true
	case strings.HasPrefix(id, "codex:trace:"):
		return identityObservation, "trace.id", true
	case strings.HasPrefix(id, "claude-code:trace:"):
		return identityObservation, "trace.id", true
	case strings.HasSuffix(id, ":unknown"):
		return identityObservation, "provider-id-absent", true
	default:
		return "", "", false
	}
}

func ensureSessionIdentity(session *canonical.Session) {
	if session.Attributes == nil {
		session.Attributes = map[string]any{}
	}
	_, hasScope := session.Attributes[identityScopeKey].(string)
	_, hasSource := session.Attributes[identitySourceKey].(string)
	if hasScope && hasSource {
		return
	}
	scope, source, observed := observationIdentity(session.SessionID)
	if !observed {
		scope, source = identityUnknown, "unproven"
	}
	if !hasScope {
		session.Attributes[identityScopeKey] = scope
	}
	if !hasSource {
		session.Attributes[identitySourceKey] = source
	}
}

func attachSessionEnvironment(session *canonical.Session, event canonical.Event) {
	resource := sessionResourceAttributes(event)
	if serviceName := sessionString(resource["service.name"]); serviceName != "" {
		observeSessionAttribute(session, "service_name", serviceName)
		observeSessionResource(session, "service.name", serviceName)
		if entrypoint := sessionEntrypoint(event.Tool, serviceName); entrypoint != "" {
			observeSessionAttribute(session, "entrypoint", entrypoint)
		}
	}
	if version := sessionString(resource["service.version"]); version != "" {
		observeSessionAttribute(session, "service_version", version)
		observeSessionResource(session, "service.version", version)
	}
	if entrypoint := sessionString(event.Attributes["entrypoint"]); entrypoint != "" {
		observeSessionAttribute(session, "entrypoint", entrypoint)
	}
	if transcript := sessionTranscriptEnvelope(event); transcript != nil {
		if entrypoint := sessionString(transcript["entrypoint"]); entrypoint != "" {
			observeSessionAttribute(session, "entrypoint", entrypoint)
		}
		if branch := sessionString(transcript["git_branch"]); branch != "" {
			observeSessionAttribute(session, "git_branch", branch)
		}
	}
	if hasCodexLogSessionID(event) {
		observeSessionCorrelation(session, map[string]any{
			"session_id_source":   codexConversationIDSource,
			"provider_prefix":     codexSessionPrefix,
			"provider_session_id": strings.TrimPrefix(event.SessionID, codexSessionPrefix),
		})
	}
}

func sessionTranscriptEnvelope(event canonical.Event) map[string]any {
	transcript, ok := event.ProviderExtensions["transcript"].(map[string]any)
	if !ok {
		return nil
	}
	return transcript
}

func sessionResourceAttributes(event canonical.Event) map[string]any {
	for _, key := range []string{"resource_attributes", "resource"} {
		resource, ok := event.ProviderExtensions[key].(map[string]any)
		if ok {
			return resource
		}
	}
	return nil
}

func hasCodexLogSessionID(event canonical.Event) bool {
	if event.Tool != "codex" || !strings.HasPrefix(event.SessionID, codexSessionPrefix) {
		return false
	}
	if _, ok := event.ProviderExtensions["log_attributes"].(map[string]any); ok {
		return true
	}
	correlation, ok := event.ProviderExtensions["correlation"].(map[string]any)
	return ok && correlation["session_id_source"] == codexConversationIDSource
}

func observeSessionAttribute(session *canonical.Session, key, value string) {
	if value == "" || value == "unavailable" {
		return
	}
	if _, exists := session.Attributes[key]; exists {
		return
	}
	session.Attributes[key] = value
}

func observeSessionResource(session *canonical.Session, key, value string) {
	if value == "" || value == "unavailable" {
		return
	}
	resource, _ := session.ProviderExtensions["resource_attributes"].(map[string]any)
	if resource == nil {
		resource = map[string]any{}
		session.ProviderExtensions["resource_attributes"] = resource
	}
	if _, exists := resource[key]; !exists {
		resource[key] = value
	}
}

func observeSessionCorrelation(session *canonical.Session, values map[string]any) {
	correlation, _ := session.ProviderExtensions["correlation"].(map[string]any)
	if correlation == nil {
		correlation = map[string]any{}
		session.ProviderExtensions["correlation"] = correlation
	}
	for key, value := range values {
		if _, exists := correlation[key]; !exists {
			correlation[key] = value
		}
	}
}

func sessionEntrypoint(tool, serviceName string) string {
	if tool != "codex" {
		return ""
	}
	switch serviceName {
	case "codex_cli_rs":
		return "interactive"
	case "codex_exec":
		return "codex exec"
	default:
		return serviceName
	}
}

func sessionString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func upsertSession(ctx context.Context, tx *sql.Tx, session canonical.Session, lastEvent time.Time) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal reconstructed session: %w", err)
	}
	model, _ := session.Attributes["model"].(string)
	scope, _ := session.Attributes[identityScopeKey].(string)
	var lastEventValue any
	if !lastEvent.IsZero() {
		lastEventValue = lastEvent.UTC().Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO sessions(session_id,session_json,started_at,tool,state,identity_scope,model,last_event_at)
VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET
session_json=excluded.session_json,
started_at=excluded.started_at,
tool=excluded.tool,
state=excluded.state,
identity_scope=excluded.identity_scope,
model=excluded.model,
last_event_at=excluded.last_event_at`,
		session.SessionID, data, session.StartedAt.UTC().Format(time.RFC3339Nano), session.Tool, session.State, scope, model, lastEventValue)
	return err
}
func lifecycle(t string) string {
	switch {
	case strings.HasSuffix(t, ".created"):
		return "created"
	case strings.HasSuffix(t, ".active") || strings.HasSuffix(t, ".started"):
		return "active"
	case strings.HasSuffix(t, ".completed"):
		return "completed"
	case strings.HasSuffix(t, ".failed"):
		return "failed"
	case strings.HasSuffix(t, ".cancelled"):
		return "cancelled"
	case strings.HasSuffix(t, ".abandoned"):
		return "abandoned"
	}
	return ""
}
func terminal(s string) bool {
	return s == "completed" || s == "failed" || s == "cancelled" || s == "abandoned"
}
func (r *Repository) Session(ctx context.Context, id string) (canonical.Session, bool, error) {
	var data []byte
	var lastEventAt sql.NullString
	err := r.db.QueryRowContext(ctx, "SELECT session_json, last_event_at FROM sessions WHERE session_id=?", id).Scan(&data, &lastEventAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonical.Session{}, false, nil
	}
	if err != nil {
		return canonical.Session{}, false, err
	}
	var s canonical.Session
	err = json.Unmarshal(data, &s)
	if err == nil {
		ensureSessionIdentity(&s)
		hydrateLastEventAt(&s, lastEventAt)
	}
	return s, true, err
}

// ListSessions returns sessions in deterministic reverse chronological order.
func (r *Repository) ListSessions(ctx context.Context, filter storage.SessionFilter) ([]canonical.Session, error) {
	query, args, err := sessionListQuery(filter)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return decodeSessions(rows, filter.Limit)
}

func sessionListQuery(filter storage.SessionFilter) (string, []any, error) {
	if filter.Limit < 1 {
		return "", nil, errors.New("session query limit must be positive")
	}
	var conditions []string
	var args []any
	appendCondition := func(condition, value string) {
		conditions = append(conditions, condition)
		args = append(args, value)
	}
	if filter.Tool != "" {
		appendCondition("tool = ?", filter.Tool)
	}
	if filter.Outcome != "" {
		appendCondition("state = ?", filter.Outcome)
	}
	switch filter.Scope {
	case storage.SessionScopePrimary:
		conditions = append(conditions, "(identity_scope IS NULL OR identity_scope != 'observation')")
	case storage.SessionScopeObservation:
		conditions = append(conditions, "identity_scope = 'observation'")
	case "":
	default:
		return "", nil, errors.New("session scope must be primary, observation, or empty")
	}
	if filter.Model != "" {
		// Match any observed event model (pre-existing API semantics). The
		// denormalized sessions.model column stores only the first observed
		// model for display, so a multi-model session must still hit events.
		appendCondition("EXISTS (SELECT 1 FROM events WHERE events.session_id=sessions.session_id AND json_extract(events.event_json, '$.attributes.model') = ?)", filter.Model)
	}
	if filter.StartedAfter != nil {
		appendCondition("started_at >= ?", filter.StartedAfter.UTC().Format(time.RFC3339Nano))
	}
	if filter.StartedBefore != nil {
		appendCondition("started_at < ?", filter.StartedBefore.UTC().Format(time.RFC3339Nano))
	}
	if filter.Cursor != nil {
		conditions = append(conditions, "(started_at < ? OR (started_at = ? AND session_id < ?))")
		cursorTime := filter.Cursor.StartedAt.UTC().Format(time.RFC3339Nano)
		args = append(args, cursorTime, cursorTime, filter.Cursor.SessionID)
	}
	query := "SELECT session_json, last_event_at FROM sessions"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY started_at DESC, session_id DESC LIMIT ?"
	return query, append(args, filter.Limit+1), nil
}

func decodeSessions(rows *sql.Rows, limit int) ([]canonical.Session, error) {
	sessions := make([]canonical.Session, 0, limit+1)
	for rows.Next() {
		var data []byte
		var lastEventAt sql.NullString
		if err := rows.Scan(&data, &lastEventAt); err != nil {
			return nil, err
		}
		var session canonical.Session
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, err
		}
		ensureSessionIdentity(&session)
		hydrateLastEventAt(&session, lastEventAt)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func hydrateLastEventAt(session *canonical.Session, lastEventAt sql.NullString) {
	if !lastEventAt.Valid || strings.TrimSpace(lastEventAt.String) == "" {
		return
	}
	if session.Attributes == nil {
		session.Attributes = map[string]any{}
	}
	session.Attributes["last_event_at"] = lastEventAt.String
}

// ListOperations returns retained stable-primitive operation records.
func (r *Repository) ListOperations(ctx context.Context, filter storage.OperationFilter) ([]canonical.Operation, error) {
	query := "SELECT operation_json FROM operations"
	args := []any{}
	if filter.SessionID != "" {
		query += whereSessionIDClause
		args = append(args, filter.SessionID)
	}
	query += " ORDER BY session_id, operation_id"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	operations := []canonical.Operation{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var operation canonical.Operation
		if err := json.Unmarshal(data, &operation); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// ListAgentRelations returns the reconstructed sub-agent tree (#102), ordered
// deterministically by (session_id, trace_id, agent_id) to match the golden.
func (r *Repository) ListAgentRelations(ctx context.Context, filter storage.AgentRelationFilter) ([]canonical.AgentRelation, error) {
	query := "SELECT relation_json FROM agent_relations"
	args := []any{}
	if filter.SessionID != "" {
		query += whereSessionIDClause
		args = append(args, filter.SessionID)
	}
	query += " ORDER BY session_id, trace_id, agent_id"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	relations := []canonical.AgentRelation{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var relation canonical.AgentRelation
		if err := json.Unmarshal(data, &relation); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (r *Repository) ListCostRecords(ctx context.Context, sessionID string) ([]cost.Record, error) {
	query := "SELECT cost_json FROM cost_records"
	args := []any{}
	if sessionID != "" {
		query += whereSessionIDClause
		args = append(args, sessionID)
	}
	query += " ORDER BY event_id"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []cost.Record{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record cost.Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *Repository) DeleteSession(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteSessionRows(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
