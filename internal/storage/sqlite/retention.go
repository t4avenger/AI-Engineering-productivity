package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ApplyRetention deletes sessions whose started_at is older than retentionDays
// before now, cascading events, costs, operations, and agent relations. Sessions
// with a NULL started_at are retained (unknown age must not be treated as zero).
func (r *Repository) ApplyRetention(ctx context.Context, retentionDays int, now time.Time) (int, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	cutoff := now.UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339Nano)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	ids, err := listExpiredSessionIDs(ctx, tx, cutoff)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := deleteSessionRows(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func listExpiredSessionIDs(ctx context.Context, tx *sql.Tx, cutoff string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT session_id FROM sessions WHERE started_at IS NOT NULL AND started_at < ?", cutoff)
	if err != nil {
		return nil, fmt.Errorf("list expired sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func deleteSessionRows(ctx context.Context, tx *sql.Tx, id string) error {
	for _, statement := range []string{
		"DELETE FROM cost_records WHERE session_id=?",
		"DELETE FROM operations WHERE session_id=?",
		"DELETE FROM agent_relations WHERE session_id=?",
		"DELETE FROM events WHERE session_id=?",
		"DELETE FROM sessions WHERE session_id=?",
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return nil
}
