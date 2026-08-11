package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

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
	if filter.Cursor != nil {
		conditions = append(conditions, "(occurred_at > ? OR (occurred_at = ? AND event_id > ?))")
		cursorTime := filter.Cursor.OccurredAt.UTC().Format(timeFormat)
		args = append(args, cursorTime, cursorTime, filter.Cursor.EventID)
	}
	query := "SELECT event_json FROM events WHERE " + strings.Join(conditions, " AND ") + " ORDER BY occurred_at ASC, event_id ASC LIMIT ?"
	return query, append(args, filter.Limit+1)
}

// EventProvenance returns the safe transformation record stored with one event.
func (r *Repository) EventProvenance(ctx context.Context, id string) ([]privacy.Provenance, bool, error) {
	var data []byte
	err := r.db.QueryRowContext(ctx, "SELECT provenance_json FROM events WHERE event_id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var provenance []privacy.Provenance
	if err := json.Unmarshal(data, &provenance); err != nil {
		return nil, false, fmt.Errorf("decode event provenance: %w", err)
	}
	return provenance, true, nil
}
