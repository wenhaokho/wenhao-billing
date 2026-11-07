package platform

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"html"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

// DeliverRecurringDrafts delivers each completed scan as one message per user.
// Delivery is at least once: acceptance followed by a crash may lead to a retry.
func DeliverRecurringDrafts(ctx context.Context, pool *pgxpool.Pool, sender email.Sender, baseURL string) error {
	if sender == nil {
		return errors.New("notification sender is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		claim, found, err := claimRecurringDraft(ctx, pool)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if claim.address == "" {
			continue
		}
		message, err := recurringDraftMessage(claim, baseURL)
		if err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		sendErr := sender.Send(sendCtx, message)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if sendErr == nil {
			_, err = pool.Exec(ctx, `UPDATE recurring_draft_deliveries SET delivered_at=now(),lease_token=NULL,lease_until=NULL,last_error=NULL WHERE user_id=$1 AND lease_token=$2`, claim.user, claim.token)
		} else {
			_, err = pool.Exec(ctx, `UPDATE recurring_draft_deliveries SET lease_token=NULL,lease_until=NULL,last_error='delivery failed',next_attempt_at=now()+make_interval(secs => LEAST(3600, 60 * power(2,LEAST(attempts-1,6))::int)) WHERE user_id=$1 AND lease_token=$2`, claim.user, claim.token)
		}
		if err != nil {
			return err
		}
	}
}

func recurringDraftMessage(claim recurringDraftClaim, baseURL string) (email.Message, error) {
	if baseURL == "" {
		baseURL = "http://localhost:5173"
	}
	var textItems, htmlItems strings.Builder
	for _, item := range claim.items {
		d, err := decimal.NewFromString(item.amount)
		if err != nil {
			return email.Message{}, err
		}
		formatted := d.RoundBank(2).StringFixed(2)
		if d.IsNegative() && d.RoundBank(2).IsZero() {
			formatted = "-" + formatted
		}
		parts := strings.SplitN(formatted, ".", 2)
		prefix := parts[0]
		sign := ""
		if strings.HasPrefix(prefix, "-") {
			sign = "-"
			prefix = prefix[1:]
		}
		for i := len(prefix) - 3; i > 0; i -= 3 {
			prefix = prefix[:i] + "," + prefix[i:]
		}
		formatted = sign + prefix + "." + parts[1]
		id := fmt.Sprintf("%x-%x-%x-%x-%x", item.invoice.Bytes[0:4], item.invoice.Bytes[4:6], item.invoice.Bytes[6:8], item.invoice.Bytes[8:10], item.invoice.Bytes[10:16])
		link := strings.TrimRight(baseURL, "/") + "/invoices/" + id + "/edit"
		fmt.Fprintf(&textItems, "- %s · %s · %s %s · cycle %s\n  %s\n", item.number, item.customer, item.currency, formatted, item.cycle, link)
		fmt.Fprintf(&htmlItems, "<li><a href=\"%s\">%s</a> — %s — %s %s — cycle %s</li>", html.EscapeString(link), html.EscapeString(item.number), html.EscapeString(item.customer), html.EscapeString(item.currency), formatted, html.EscapeString(item.cycle))
	}
	n := len(claim.items)
	noun := "invoice"
	if n != 1 {
		noun = "invoices"
	}
	intro := fmt.Sprintf("The recurring billing scan generated %d draft %s that need your review and approval:", n, noun)
	outro := "Each invoice stays in the Awaiting Finalization queue until you approve and issue it."
	text := fmt.Sprintf("Hi,\n\n%s\n\n%s\n%s\n", intro, textItems.String(), outro)
	body := fmt.Sprintf("<p>Hi,</p><p>%s</p><ul>%s</ul><p>%s</p>", intro, htmlItems.String(), outro)
	return email.Message{To: []string{claim.address}, Subject: fmt.Sprintf("%d draft %s awaiting your approval", n, noun), Text: text, HTML: body}, nil
}

type recurringDraftItem struct {
	invoice                                   pgtype.UUID
	number, customer, amount, currency, cycle string
}
type recurringDraftClaim struct {
	user, token pgtype.UUID
	address     string
	items       []recurringDraftItem
}

// The transaction-level job lock prevents a worker claiming a batch while its
// scanner is still producing members. A crashed scanner releases the lock, so
// its committed members remain deliverable as one partial run.
func claimRecurringDraft(ctx context.Context, pool *pgxpool.Pool) (recurringDraftClaim, bool, error) {
	var c recurringDraftClaim
	tx, err := pool.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback(context.Background())
	var locked bool
	key := int64(crc32.ChecksumIEEE([]byte("recurring-scan-and-generate")))
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, key).Scan(&locked); err != nil {
		return c, false, err
	}
	if !locked {
		return c, false, nil
	}
	var invoice, batch pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT d.invoice_id,d.user_id,e.scan_batch_id FROM recurring_draft_deliveries d JOIN recurring_draft_events e ON e.invoice_id=d.invoice_id WHERE d.delivered_at IS NULL AND d.cancelled_at IS NULL AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now()) ORDER BY d.next_attempt_at,d.invoice_id,d.user_id FOR UPDATE OF d SKIP LOCKED LIMIT 1`).Scan(&invoice, &c.user, &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	err = tx.QueryRow(ctx, `SELECT email FROM users WHERE user_id=$1 FOR SHARE`, c.user).Scan(&c.address)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE recurring_draft_deliveries d SET cancelled_at=now(),email='',lease_token=NULL,lease_until=NULL,last_error='recipient unavailable' FROM recurring_draft_events e WHERE d.invoice_id=e.invoice_id AND d.user_id=$1 AND (d.invoice_id=$2 OR ($3::uuid IS NOT NULL AND e.scan_batch_id=$3)) AND d.delivered_at IS NULL AND d.cancelled_at IS NULL`, c.user, invoice, batch)
		if err != nil {
			return c, false, err
		}
		err = tx.Commit(ctx)
		return c, true, err
	}
	if err != nil {
		return c, false, err
	}
	c.token, err = itemUUID()
	if err != nil {
		return c, false, err
	}
	rows, err := tx.Query(ctx, `UPDATE recurring_draft_deliveries d SET email=$4,lease_token=$5,lease_until=now()+interval '2 minutes',attempts=attempts+1 FROM recurring_draft_events e WHERE d.invoice_id=e.invoice_id AND d.user_id=$1 AND (d.invoice_id=$2 OR ($3::uuid IS NOT NULL AND e.scan_batch_id=$3)) AND d.delivered_at IS NULL AND d.cancelled_at IS NULL AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now()) RETURNING d.invoice_id,e.invoice_number,e.customer_name,e.amount::text,e.currency,e.cycle_key`, c.user, invoice, batch, c.address, c.token)
	if err != nil {
		return c, false, err
	}
	for rows.Next() {
		var item recurringDraftItem
		if err = rows.Scan(&item.invoice, &item.number, &item.customer, &item.amount, &item.currency, &item.cycle); err != nil {
			rows.Close()
			return c, false, err
		}
		c.items = append(c.items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return c, false, err
	}
	return c, true, nil
}
