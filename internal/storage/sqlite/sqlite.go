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

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

type Repository struct {
	db        *sql.DB
	sanitizer *privacy.Sanitizer
}

func Open(path string, sanitizer *privacy.Sanitizer) (*Repository, error) {
	if sanitizer == nil {
		return nil, errors.New("privacy sanitizer is required")
	}
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
	r := &Repository{db: db, sanitizer: sanitizer}
	if err := r.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
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
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, occurred_at TEXT NOT NULL, event_json BLOB NOT NULL, provenance_json BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS events_session_occurred ON events(session_id, occurred_at, event_id);
CREATE TABLE IF NOT EXISTS sessions (session_id TEXT PRIMARY KEY, session_json BLOB NOT NULL);`)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	_, err = r.db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (1)")
	return err
}

func (r *Repository) SaveEvents(ctx context.Context, events []canonical.Event) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ids := map[string]struct{}{}
	for _, event := range events {
		safe, provenance, err := r.safeEvent(event)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(safe)
		if err != nil {
			return fmt.Errorf("marshal sanitized event: %w", err)
		}
		provenanceJSON, err := json.Marshal(provenance)
		if err != nil {
			return fmt.Errorf("marshal sanitization provenance: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO events(event_id,session_id,occurred_at,event_json,provenance_json) VALUES(?,?,?,?,?)", safe.EventID, safe.SessionID, safe.OccurredAt.UTC().Format(timeFormat), payload, provenanceJSON); err != nil {
			return err
		}
		ids[safe.SessionID] = struct{}{}
	}
	for id := range ids {
		if err := r.rebuildSession(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func (r *Repository) safeEvent(event canonical.Event) (canonical.Event, []privacy.Provenance, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return canonical.Event{}, nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return canonical.Event{}, nil, err
	}
	result := r.sanitizer.Sanitize(raw)
	data, err = json.Marshal(result.Value)
	if err != nil {
		return canonical.Event{}, nil, err
	}
	var safe canonical.Event
	if err := json.Unmarshal(data, &safe); err != nil {
		return canonical.Event{}, nil, err
	}
	return safe, result.Provenance, nil
}

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
	return session
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
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}
func (r *Repository) DeleteSession(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE session_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE session_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
