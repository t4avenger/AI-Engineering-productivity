package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

// GetEvent returns one retained event scoped to sessionID. A row that exists
// under a different session is reported as not found so callers cannot leak
// cross-session content (#189).
func (r *Repository) GetEvent(ctx context.Context, sessionID, eventID string) (canonical.Event, bool, error) {
	if sessionID == "" {
		return canonical.Event{}, false, errors.New("event session ID is required")
	}
	if eventID == "" {
		return canonical.Event{}, false, errors.New("event ID is required")
	}
	var data []byte
	err := r.db.QueryRowContext(ctx,
		"SELECT event_json FROM events WHERE session_id=? AND event_id=?",
		sessionID, eventID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return canonical.Event{}, false, nil
	}
	if err != nil {
		return canonical.Event{}, false, err
	}
	var event canonical.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return canonical.Event{}, false, fmt.Errorf("decode stored event: %w", err)
	}
	return event, true, nil
}

// ListEvents returns one session timeline in deterministic chronological order.
func (r *Repository) ListEvents(ctx context.Context, filter storage.EventFilter) ([]canonical.Event, error) {
	if filter.SessionID == "" {
		return nil, errors.New("event session ID is required")
	}
	if filter.Limit < 1 {
		return nil, errors.New("event query limit must be positive")
	}
	query, args := eventListQuery(filter)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]canonical.Event, 0, filter.Limit+1)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var event canonical.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("decode stored event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func eventListQuery(filter storage.EventFilter) (string, []any) {
	conditions := []string{"session_id=?"}
	args := []any{filter.SessionID}
	if len(filter.EventTypes) > 0 {
		placeholders := make([]string, 0, len(filter.EventTypes))
		for _, eventType := range filter.EventTypes {
			placeholders = append(placeholders, "?")
			args = append(args, eventType)
		}
		conditions = append(conditions, "event_type IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Cursor != nil {
		conditions = append(conditions, "(occurred_at > ? OR (occurred_at = ? AND event_id > ?))")
		cursorTime := filter.Cursor.OccurredAt.UTC().Format(timeFormat)
		args = append(args, cursorTime, cursorTime, filter.Cursor.EventID)
	}
	query := "SELECT event_json FROM events WHERE " + strings.Join(conditions, " AND ") + " ORDER BY occurred_at ASC, event_id ASC LIMIT ?"
	return query, append(args, filter.Limit+1)
}
