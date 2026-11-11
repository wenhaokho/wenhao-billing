-- name: CountAwaitingReview :one
SELECT count(*) FROM payments WHERE status = 'PENDING_MANUAL_REVIEW';

-- name: CountDrafts :one
-- Templates are DRAFT too and never count (the Awaiting Finalization queue
-- excludes them).
SELECT count(*) FROM invoices WHERE status = 'DRAFT' AND is_template = false;

-- name: CountSent :one
-- No template filter and no PARTIAL, as in Python.
SELECT count(*) FROM invoices WHERE status IN ('OPEN', 'SENT');

-- name: CountCustomers :one
-- Inactive customers included.
SELECT count(*) FROM customers;

-- name: CountAutoCleared :one
-- Every CLEARED payment created at or after since (a full timestamp, naive UTC).
SELECT count(*) FROM payments WHERE status = 'CLEARED' AND created_at >= @since::timestamp;

-- name: OpenARByCurrency :many
-- The one open-AR rule (Go change 2): OPEN, SENT and PARTIAL, never a
-- template, only a positive balance.
SELECT currency, COALESCE(SUM(balance_due), 0)::numeric AS amount
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
 GROUP BY currency
 ORDER BY currency;

-- name: CountOpenQuotations :one
SELECT count(*) FROM quotations WHERE status IN ('SENT', 'ACCEPTED');

-- name: OpenQuotationPipeline :many
SELECT currency, COALESCE(SUM(amount), 0)::numeric AS amount
  FROM quotations
 WHERE status IN ('SENT', 'ACCEPTED')
 GROUP BY currency
 ORDER BY currency;

-- name: ListRecurringTemplates :many
-- Python's MRR scan: RECURRING templates of any status.
SELECT invoice_id, currency, amount, billing_cycle_ref FROM invoices
 WHERE invoice_type = 'RECURRING' AND is_template = true
 ORDER BY invoice_id;

-- name: ListUpcomingRecurring :many
-- The upcoming list's recurring rows: the same templates as the MRR scan with
-- the customer, whose name is null when the invoice has none.
SELECT i.invoice_id, i.customer_id, c.name AS customer_name, i.currency, i.amount, i.billing_cycle_ref
  FROM invoices i
  LEFT JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.invoice_type = 'RECURRING' AND i.is_template = true
 ORDER BY i.invoice_id;

-- name: ListUpcomingUsage :many
-- DRAFT usage invoices, no template filter (Python's compute_upcoming_invoices).
SELECT i.invoice_id, i.customer_id, c.name AS customer_name, i.currency, i.amount, i.billing_cycle_ref
  FROM invoices i
  LEFT JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.invoice_type = 'USAGE' AND i.status = 'DRAFT'
 ORDER BY i.invoice_id;

-- name: RecentActivity :many
-- The newest reconciliation_log rows with their payment, invoice, customer
-- (the payment's own, else the invoice's) and actor. Python had no tie-break.
SELECT l.log_id, l.payment_id, l.action, l.reasons, l.actor_user_id, l.created_at,
       p.payer_name, p.amount, p.currency, p.payment_date, p.payer_reference, p.wht_amount,
       p.confidence_score, p.intake_source, p.invoice_id,
       i.invoice_number, c.name AS customer_name,
       u.display_name AS actor_display_name, u.email AS actor_email
  FROM reconciliation_log l
  LEFT JOIN payments p ON p.payment_id = l.payment_id
  LEFT JOIN invoices i ON i.invoice_id = p.invoice_id
  LEFT JOIN customers c ON c.customer_id = COALESCE(p.customer_id, i.customer_id)
  LEFT JOIN users u ON u.user_id = l.actor_user_id
 ORDER BY l.created_at DESC, l.log_id DESC
 LIMIT 10;

-- name: MonthlyRevenue :many
-- Invoiced revenue per issue month and currency from the window's first day
-- on: OPEN, SENT, PARTIAL and PAID, never a template. No upper bound (future
-- months are dropped by the caller), as in Python.
SELECT to_char(issue_date, 'YYYY-MM')::text AS month, currency,
       COALESCE(SUM(amount), 0)::numeric AS amount,
       COALESCE(SUM(balance_due), 0)::numeric AS unpaid
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL', 'PAID')
   AND issue_date IS NOT NULL AND issue_date >= @window_start::date
   AND is_template = false
 GROUP BY 1, currency
 ORDER BY 1, currency;

-- name: OverdueByCurrency :many
-- The open-AR rule (OPEN, SENT, PARTIAL, no template, positive balance) with
-- a due date before today.
SELECT currency, COALESCE(SUM(balance_due), 0)::numeric AS amount
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
   AND due_date IS NOT NULL AND due_date < @today::date
 GROUP BY currency
 ORDER BY currency;

-- name: DueWithinByCurrency :many
-- The open-AR rule with a due date from today to until, both inclusive.
SELECT currency, COALESCE(SUM(balance_due), 0)::numeric AS amount
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
   AND due_date IS NOT NULL AND due_date >= @today::date AND due_date <= @until::date
 GROUP BY currency
 ORDER BY currency;

-- name: DaysToPay :one
-- Whole days from issue to payment, summed and counted per CLEARED payment of
-- a PAID invoice paid on or after since (an invoice paid twice counts twice;
-- no upper bound; a negative delta counts).
SELECT COALESCE(SUM(p.payment_date - i.issue_date), 0)::bigint AS total_days, COUNT(*)::bigint AS payments
  FROM payments p
  JOIN invoices i ON i.invoice_id = p.invoice_id
 WHERE i.status = 'PAID' AND i.issue_date IS NOT NULL
   AND p.status = 'CLEARED' AND p.payment_date >= @since::date;

-- name: InvoicedByCurrencyBetween :many
-- Invoiced in a window: issued in [from, to], never a template, never a DRAFT
-- or VOID invoice.
SELECT currency, COALESCE(SUM(amount), 0)::numeric AS amount
  FROM invoices
 WHERE is_template = false AND status NOT IN ('DRAFT', 'VOID')
   AND issue_date IS NOT NULL AND issue_date >= @from_date::date AND issue_date <= @to_date::date
 GROUP BY currency
 ORDER BY currency;

-- name: CollectedByCurrencyBetween :many
-- Collected in a window: CLEARED payments dated in [from, to], withholding
-- tax included (the invoice was settled by cash plus the tax certificate).
SELECT currency, COALESCE(SUM(amount + COALESCE(wht_amount, 0)), 0)::numeric AS amount
  FROM payments
 WHERE status = 'CLEARED' AND payment_date >= @from_date::date AND payment_date <= @to_date::date
 GROUP BY currency
 ORDER BY currency;

-- name: OpenARCounts :one
-- Invoices and distinct customers behind the open AR rule.
SELECT count(*)::bigint AS invoice_count, count(DISTINCT customer_id)::bigint AS customer_count
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0;

-- name: DaysToPayBetween :one
-- DaysToPay with an upper bound: payments dated from since to until, both
-- inclusive.
SELECT COALESCE(SUM(p.payment_date - i.issue_date), 0)::bigint AS total_days, COUNT(*)::bigint AS payments
  FROM payments p
  JOIN invoices i ON i.invoice_id = p.invoice_id
 WHERE i.status = 'PAID' AND i.issue_date IS NOT NULL
   AND p.status = 'CLEARED'
   AND p.payment_date >= @from_date::date AND p.payment_date <= @to_date::date;

-- name: ReconCountsBetween :one
-- Payments created in [from, to] by outcome. Auto = CLEARED with a CLEARED log
-- row written by the engine (actor_user_id NULL); manual = any other CLEARED
-- (manual entry and override write an actor); held = PENDING_MANUAL_REVIEW or
-- FLAGGED.
SELECT
  COUNT(*) FILTER (WHERE p.status = 'CLEARED' AND EXISTS (
    SELECT 1 FROM reconciliation_log l
     WHERE l.payment_id = p.payment_id AND l.action = 'CLEARED' AND l.actor_user_id IS NULL))::bigint AS auto_cleared,
  COUNT(*) FILTER (WHERE p.status = 'CLEARED' AND NOT EXISTS (
    SELECT 1 FROM reconciliation_log l
     WHERE l.payment_id = p.payment_id AND l.action = 'CLEARED' AND l.actor_user_id IS NULL))::bigint AS manual,
  COUNT(*) FILTER (WHERE p.status IN ('PENDING_MANUAL_REVIEW', 'FLAGGED'))::bigint AS held
  FROM payments p
 WHERE p.created_at::date >= @from_date::date AND p.created_at::date <= @to_date::date;

-- name: AvgConfidenceBetween :one
-- Mean match confidence of payments created in [from, to] that have a score
-- and did not come from manual entry (scored = how many; 0 means no mean).
SELECT COALESCE(AVG(p.confidence_score), 0)::float8 AS avg_confidence, COUNT(p.confidence_score)::bigint AS scored
  FROM payments p
 WHERE p.confidence_score IS NOT NULL AND p.intake_source <> 'MANUAL'
   AND p.created_at::date >= @from_date::date AND p.created_at::date <= @to_date::date;

-- name: AliasStats :one
-- Matching aliases in use across customers, and how many customers have any.
SELECT COALESCE(SUM(jsonb_array_length(matching_aliases)), 0)::bigint AS aliases,
       COUNT(*) FILTER (WHERE jsonb_array_length(matching_aliases) > 0)::bigint AS customers
  FROM customers;

-- name: InvoicedByCustomerCurrencyBetween :many
-- InvoicedByCurrencyBetween per customer.
SELECT i.customer_id, c.name, i.currency, COALESCE(SUM(i.amount), 0)::numeric AS amount
  FROM invoices i
  JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.is_template = false AND i.status NOT IN ('DRAFT', 'VOID')
   AND i.issue_date >= @from_date::date AND i.issue_date <= @to_date::date
 GROUP BY i.customer_id, c.name, i.currency
 ORDER BY i.customer_id, i.currency;

-- name: OverdueByCustomerCurrency :many
-- OverdueByCurrency per customer.
SELECT customer_id, currency, COALESCE(SUM(balance_due), 0)::numeric AS amount
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
   AND due_date IS NOT NULL AND due_date < @today::date
 GROUP BY customer_id, currency
 ORDER BY customer_id, currency;

-- name: AgingByCurrency :many
-- The open-AR rule by how late it is: today - due_date in days (no due date
-- counts as not due), per bucket and currency.
SELECT CASE
         WHEN due_date IS NULL OR due_date >= sqlc.arg(today)::date THEN 'not_due'
         WHEN due_date >= (sqlc.arg(today)::date - 30) THEN 'd1_30'
         WHEN due_date >= (sqlc.arg(today)::date - 60) THEN 'd31_60'
         WHEN due_date >= (sqlc.arg(today)::date - 90) THEN 'd61_90'
         ELSE 'd90_plus'
       END::text AS bucket,
       currency, COALESCE(SUM(balance_due), 0)::numeric AS amount, count(*)::bigint AS invoices
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
 GROUP BY 1, currency
 ORDER BY 1, currency;

-- name: OpenNotDueByCurrencyBetween :many
-- The open-AR rule with a due date from from_date to to_date, both inclusive.
SELECT currency, COALESCE(SUM(balance_due), 0)::numeric AS amount
  FROM invoices
 WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false AND balance_due > 0
   AND due_date IS NOT NULL AND due_date >= @from_date::date AND due_date <= @to_date::date
 GROUP BY currency
 ORDER BY currency;

-- name: OverdueOldest :many
-- The overdue open invoices, oldest due date first (ties by invoice id).
SELECT i.invoice_id, i.invoice_number, i.customer_id, COALESCE(c.name, '')::text AS customer_name,
       i.balance_due, i.wht_amount, i.wht_booked, i.currency, i.due_date
  FROM invoices i
  LEFT JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.status IN ('OPEN', 'SENT', 'PARTIAL') AND i.is_template = false AND i.balance_due > 0
   AND i.due_date IS NOT NULL AND i.due_date < @today::date
 ORDER BY i.due_date, i.invoice_id
 LIMIT @lim::int;
