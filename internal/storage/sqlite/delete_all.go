package sqlite

import "context"

// DeleteAllSessions removes every retained event, its provenance, and every
// reconstructed session atomically. Schema metadata remains for future local
// collection.
func (r *Repository) DeleteAllSessions(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "DELETE FROM cost_records"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM events"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}
