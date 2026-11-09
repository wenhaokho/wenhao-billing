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
