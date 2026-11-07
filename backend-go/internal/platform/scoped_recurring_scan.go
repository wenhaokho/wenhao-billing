package platform

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

func scopedRecurringJob(scope business.ID) string {
	return "billing:v8:recurring-scan-and-generate:" + scope.String()
}

// ScanScopedRecurring visits each template once and returns the number of due cycles,
// including cycles already generated. The supplied clock uses local date semantics.
func ScanScopedRecurring(ctx context.Context, pool *pgxpool.Pool, scope business.ID, now time.Time) (int, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	job := scopedRecurringJob(scope)
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, job).Scan(&locked); err != nil {
		// The server may have acquired the session lock before cancellation.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanup)
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, job); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	// Capture the run's recipients once. Deletions and email changes are
	// revalidated by the delivery claim; new users join the next run.
	recipients := scopedRecurringRecipients{scope: scope, recurringRecipients: recurringRecipients{ids: []pgtype.UUID{}, emails: []string{}}}
	userRows, err := conn.Query(ctx, `SELECT user_id,email FROM users WHERE business_id=$1 ORDER BY user_id`, scope.UUID())
	if err != nil {
		return 0, err
	}
	for userRows.Next() {
		var id pgtype.UUID
		var email string
		if err = userRows.Scan(&id, &email); err != nil {
			userRows.Close()
			return 0, err
		}
		recipients.ids = append(recipients.ids, id)
		recipients.emails = append(recipients.emails, email)
	}
	err = userRows.Err()
	userRows.Close()
	if err != nil {
		return 0, err
	}
	rows, err := conn.Query(ctx, `SELECT invoice_id FROM invoices WHERE business_id=$1 AND invoice_type='RECURRING' AND is_template IS TRUE ORDER BY invoice_id`, scope.UUID())
	if err != nil {
		return 0, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	batch, err := itemUUID()
	if err != nil {
		return 0, err
	}
	count := 0
	failed := false
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return count, err
		}
		result := &scanResponse{header: make(http.Header)}
		recurringTriggerCore(result, ctx, candidateTransactions{db: conn, scope: scope}, id, id.String(), "", nil, &now, func(tx pgx.Tx) recurringTriggerQueries {
			return scopedRecurringScanQueries{scopedInvoiceWriteQueries{tx: tx, scope: scope}, batch, &recipients}
		})
		switch result.status {
		case 0, 200:
			count++
		case 204, 400: // Invalid schedule is skipped, as in Python.
		default:
			failed = true
		}
	}
	if failed {
		return count, errors.New("one or more recurring templates failed")
	}
	return count, nil
}

type scopedRecurringRecipients struct {
	scope business.ID
	recurringRecipients
}

type scopedRecurringScanQueries struct {
	scopedInvoiceWriteQueries
	batch      pgtype.UUID
	recipients *scopedRecurringRecipients
}

func (q scopedRecurringScanQueries) enqueueRecurringDraft(ctx context.Context, invoice pgtype.UUID, key string) error {
	return q.enqueueScopedRecurringDraft(ctx, invoice, key, q.batch, q.recipients)
}
