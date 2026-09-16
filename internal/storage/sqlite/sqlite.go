// Package sqlite implements the local SQLite repository.
package sqlite

import (
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
	"github.com/wayne/telemetryiq/internal/storage"
)

type Repository struct {
	db         *sql.DB
	calculator *cost.Calculator
}

func Open(path string, calculators ...*cost.Calculator) (*Repository, error) {
	if path != ":memory:" {
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	r := &Repository{db: db}
	if len(calculators) > 0 {
		r.calculator = calculators[0]
	}
	if err := r.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := r.rebuildAllSessions(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("rebuild derived sessions: %w", err)
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("secure database file: %w", err)
		}
	}
	return r, nil
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
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO events(event_id,session_id,occurred_at,event_json) VALUES(?,?,?,?)", event.EventID, event.SessionID, event.OccurredAt.UTC().Format(timeFormat), payload)
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
		payload, err := json.Marshal(operation)
		if err != nil {
			return fmt.Errorf("marshal operation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO operations(session_id,operation_id,operation_json) VALUES(?,?,?)", operation.SessionID, operation.OperationID, payload); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) rebuildAllSessions(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT session_id FROM events ORDER BY session_id")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := r.rebuildSession(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) saveCostRecord(ctx context.Context, tx *sql.Tx, record cost.Record) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal cost record: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO cost_records(event_id,session_id,cost_json) VALUES(?,?,?)", record.EventID, record.SessionID, payload); err != nil {
		return fmt.Errorf("insert cost record: %w", err)
	}
	return nil
}

const (
	timeFormat          = "2006-01-02T15:04:05.999999999Z07:00"
	codexSessionPrefix  = "codex:"
	identityScopeKey    = "identity_scope"
	identitySourceKey   = "identity_source"
	identityProvider    = "provider"
	identityObservation = "observation"
	identityUnknown     = "unknown"
)

func (r *Repository) rebuildSession(ctx context.Context, tx *sql.Tx, id string) error {
	events, err := loadSessionEvents(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	return upsertSession(ctx, tx, reconstructSession(id, events))
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
	scope, source := sessionIdentity(events)
	session.Attributes[identityScopeKey] = scope
	session.Attributes[identitySourceKey] = source
	return session
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
		return identityProvider, "conversation.id"
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
	if hasCodexLogSessionID(event) {
		observeSessionCorrelation(session, map[string]any{
			"session_id_source":   "conversation.id",
			"provider_prefix":     codexSessionPrefix,
			"provider_session_id": strings.TrimPrefix(event.SessionID, codexSessionPrefix),
		})
	}
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
	_, ok := event.ProviderExtensions["log_attributes"].(map[string]any)
	return ok
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

func upsertSession(ctx context.Context, tx *sql.Tx, session canonical.Session) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal reconstructed session: %w", err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO sessions(session_id,session_json) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET session_json=excluded.session_json", session.SessionID, data)
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
	err := r.db.QueryRowContext(ctx, "SELECT session_json FROM sessions WHERE session_id=?", id).Scan(&data)
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
		appendCondition("json_extract(session_json, '$.tool') = ?", filter.Tool)
	}
	if filter.Outcome != "" {
		appendCondition("json_extract(session_json, '$.state') = ?", filter.Outcome)
	}
	identityScopeExpression := "COALESCE(json_extract(session_json, '$.attributes.identity_scope'), CASE WHEN session_id GLOB 'codex-log:*' OR session_id GLOB 'codex:token:*' OR session_id GLOB 'codex:skill:*' OR session_id GLOB 'codex:skill-turn:*' OR session_id GLOB 'cursor:token:*' OR session_id GLOB 'claude-code:trace:*' OR session_id GLOB '*:unknown' THEN 'observation' ELSE 'unknown' END)"
	switch filter.Scope {
	case storage.SessionScopePrimary:
		conditions = append(conditions, identityScopeExpression+" != 'observation'")
	case storage.SessionScopeObservation:
		conditions = append(conditions, identityScopeExpression+" = 'observation'")
	case "":
	default:
		return "", nil, errors.New("session scope must be primary, observation, or empty")
	}
	if filter.Model != "" {
		appendCondition("EXISTS (SELECT 1 FROM events WHERE events.session_id=sessions.session_id AND json_extract(events.event_json, '$.attributes.model') = ?)", filter.Model)
	}
	if filter.StartedAfter != nil {
		appendCondition("json_extract(session_json, '$.started_at') >= ?", filter.StartedAfter.UTC().Format(time.RFC3339Nano))
	}
	if filter.StartedBefore != nil {
		appendCondition("json_extract(session_json, '$.started_at') < ?", filter.StartedBefore.UTC().Format(time.RFC3339Nano))
	}
	if filter.Cursor != nil {
		conditions = append(conditions, "(json_extract(session_json, '$.started_at') < ? OR (json_extract(session_json, '$.started_at') = ? AND session_id < ?))")
		cursorTime := filter.Cursor.StartedAt.UTC().Format(time.RFC3339Nano)
		args = append(args, cursorTime, cursorTime, filter.Cursor.SessionID)
	}
	query := "SELECT session_json FROM sessions"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY json_extract(session_json, '$.started_at') DESC, session_id DESC LIMIT ?"
	return query, append(args, filter.Limit+1), nil
}

func decodeSessions(rows *sql.Rows, limit int) ([]canonical.Session, error) {
	sessions := make([]canonical.Session, 0, limit+1)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var session canonical.Session
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, err
		}
		ensureSessionIdentity(&session)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// ListOperations returns retained stable-primitive operation records.
func (r *Repository) ListOperations(ctx context.Context, filter storage.OperationFilter) ([]canonical.Operation, error) {
	query := "SELECT operation_json FROM operations"
	args := []any{}
	if filter.SessionID != "" {
		query += " WHERE session_id=?"
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

func (r *Repository) ListCostRecords(ctx context.Context, sessionID string) ([]cost.Record, error) {
	query := "SELECT cost_json FROM cost_records"
	args := []any{}
	if sessionID != "" {
		query += " WHERE session_id=?"
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
	if _, err = tx.ExecContext(ctx, "DELETE FROM cost_records WHERE session_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM operations WHERE session_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE session_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE session_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
