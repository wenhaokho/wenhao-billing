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
