package platform

import (
	"context"
	"errors"
	"hash/crc32"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ScanRecurring visits each template once and returns the number of due cycles,
// including cycles already generated. The supplied clock uses local date semantics.
func ScanRecurring(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	const job = "recurring-scan-and-generate"
	key := int64(crc32.ChecksumIEEE([]byte(job)))
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	// Capture the run's recipients once. Deletions and email changes are
	// revalidated by the delivery claim; new users join the next run.
	recipients := recurringRecipients{ids: []pgtype.UUID{}, emails: []string{}}
	userRows, err := conn.Query(ctx, `SELECT user_id,email FROM users ORDER BY user_id`)
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
	rows, err := conn.Query(ctx, `SELECT invoice_id FROM invoices WHERE invoice_type='RECURRING' AND is_template IS TRUE ORDER BY invoice_id`)
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
		v, _ := id.Value()
		result := &scanResponse{header: make(http.Header)}
		recurringTriggerCore(result, ctx, conn, id, v.(string), "", nil, &now, legacyRecurringTrigger(batch, &recipients))
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

type scanResponse struct {
	header http.Header
	status int
}

func (r *scanResponse) Header() http.Header    { return r.header }
func (r *scanResponse) WriteHeader(status int) { r.status = status }
func (r *scanResponse) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return len(p), nil
}

// recurringRecipients is an immutable snapshot for one scan.
type recurringRecipients struct {
	ids    []pgtype.UUID
	emails []string
}
