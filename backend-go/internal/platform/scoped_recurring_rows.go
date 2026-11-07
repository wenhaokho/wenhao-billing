package platform

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

func (h scopedProfileRouter) recurringRows(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)

	// The single SELECT reads templates, customers and ranked children from
	// the same statement snapshot.
	rows, err := scopedinvoices.RecurringTemplateRows(ctx, tx, h.scope)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	legacy := make([]dbquery.RecurringTemplateRowsRow, len(rows))
	for i, row := range rows {
		// The scoped LEFT JOIN can miss only if a non-null reference is
		// damaged. Customer names are NOT NULL.
		if row.CustomerID.Valid && !row.CustomerName.Valid {
			authError(w, 500, "internal server error")
			return
		}
		legacy[i] = dbquery.RecurringTemplateRowsRow(row)
	}
	writeRecurringRows(w, legacy)
}
