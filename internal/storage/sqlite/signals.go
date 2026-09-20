package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const (
	signalKindInsightEvent  = "insight_event"
	migrationVersionSignals = 7
)

// ensureInsightSignals is migration 7: last_event_at, events.event_type, and
// insight_signals for dashboard corpus reads without scanning event_json.
func (r *Repository) ensureInsightSignals(ctx context.Context) error {
	applied, err := r.migrationApplied(ctx, migrationVersionSignals)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 7: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := addMissingColumns(ctx, tx, "sessions", [][2]string{
		{"last_event_at", "ALTER TABLE sessions ADD COLUMN last_event_at TEXT"},
	}); err != nil {
		return err
	}
	if err := addMissingColumns(ctx, tx, "events", [][2]string{
		{"event_type", "ALTER TABLE events ADD COLUMN event_type TEXT"},
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE events SET event_type = COALESCE(event_type, json_extract(event_json, '$.event_type'))`); err != nil {
		return fmt.Errorf("backfill events.event_type: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET last_event_at = COALESCE(last_event_at, (
		SELECT MAX(occurred_at) FROM events WHERE events.session_id = sessions.session_id
	))`); err != nil {
		return fmt.Errorf("backfill sessions.last_event_at: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS insight_signals (
signal_id TEXT PRIMARY KEY,
session_id TEXT NOT NULL,
occurred_at TEXT NOT NULL,
kind TEXT NOT NULL,
payload_json BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS insight_signals_kind_time ON insight_signals(kind, occurred_at);
CREATE INDEX IF NOT EXISTS insight_signals_session_kind ON insight_signals(session_id, kind, occurred_at);
CREATE INDEX IF NOT EXISTS events_session_type_occurred ON events(session_id, event_type, occurred_at, event_id)`); err != nil {
		return fmt.Errorf("create insight signal indexes: %w", err)
	}
	if err := r.backfillInsightSignals(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (7)"); err != nil {
		return fmt.Errorf("record migration 7: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 7: %w", err)
	}
	return nil
}

func (r *Repository) backfillInsightSignals(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT session_id FROM events ORDER BY session_id")
	if err != nil {
		return fmt.Errorf("list sessions for signal backfill: %w", err)
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
		// Full rebuild writes last_event_at into session_json as well as the
		// denormalized column and insight_signals — ListSessions only returns JSON.
		if err := r.rebuildSession(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func rebuildInsightSignals(ctx context.Context, tx *sql.Tx, sessionID string, events []canonical.Event) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM insight_signals WHERE session_id=?", sessionID); err != nil {
		return fmt.Errorf("clear insight signals: %w", err)
	}
	sources := insights.SourceEventsFromSession(events)
	sources = append(sources, governance.ThinAccessEvents(events)...)
	for _, event := range sources {
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal insight signal: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO insight_signals(signal_id,session_id,occurred_at,kind,payload_json) VALUES(?,?,?,?,?)`,
			event.EventID, sessionID, event.OccurredAt.UTC().Format(timeFormat), signalKindInsightEvent, payload); err != nil {
			return fmt.Errorf("insert insight signal: %w", err)
		}
	}
	return nil
}

func lastEventAt(events []canonical.Event) time.Time {
	var latest time.Time
	for _, event := range events {
		if latest.IsZero() || event.OccurredAt.After(latest) {
			latest = event.OccurredAt
		}
	}
	return latest
}

// ListInsightSourceEvents returns thin canonical events for dashboard insights.
func (r *Repository) ListInsightSourceEvents(ctx context.Context) ([]canonical.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT payload_json FROM insight_signals WHERE kind=? ORDER BY occurred_at ASC, signal_id ASC`,
		signalKindInsightEvent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []canonical.Event{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var event canonical.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("decode insight signal: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// Ensure Repository satisfies the insight source reader used by UI/API.
var _ storage.InsightSourceReader = (*Repository)(nil)
