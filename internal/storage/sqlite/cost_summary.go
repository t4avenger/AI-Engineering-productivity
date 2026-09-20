package sqlite

import (
	"context"
	"database/sql"

	"github.com/wayne/telemetryiq/internal/cost"
)

// SummarizeCosts aggregates retained cost rows in SQL without loading each record.
func (r *Repository) SummarizeCosts(ctx context.Context) (cost.Summary, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(currency, ''), COALESCE(status, ''), COUNT(*), SUM(amount_microusd)
FROM cost_records
GROUP BY currency, status
ORDER BY currency, status`)
	if err != nil {
		return cost.Summary{}, err
	}
	defer func() { _ = rows.Close() }()

	summary := cost.Summary{Statuses: map[string]int{}}
	var amount int64
	hasAmount := false
	for rows.Next() {
		var currency, status string
		var count int
		var sum sql.NullInt64
		if err := rows.Scan(&currency, &status, &count, &sum); err != nil {
			return cost.Summary{}, err
		}
		if summary.Currency == "" && currency != "" {
			summary.Currency = currency
		}
		if status != "" {
			summary.Statuses[status] += count
		}
		if sum.Valid {
			hasAmount = true
			amount += sum.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return cost.Summary{}, err
	}
	if hasAmount {
		summary.CalculatedAmountMicrousd = &amount
	}
	return summary, nil
}
