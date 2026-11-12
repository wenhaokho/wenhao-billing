-- name: InsertPayment :one
-- wht_amount and created_at are the columns' defaults (0 and
-- CURRENT_TIMESTAMP), as Python's ORM defaults left them.
INSERT INTO payments (payment_id, invoice_id, customer_id, amount, currency, payer_name, payer_reference,
                      payment_date, intake_source, external_ref, status, adjustment_type, confidence_score)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments WHERE payment_id = $1;

-- name: LockPayment :one
-- manual-review and reverse read the payment locked first (lock order:
-- payment, invoice, journal entry), so two approvals of one payment run one
-- after the other and the second sees the first's CLEARED.
SELECT * FROM payments WHERE payment_id = $1 FOR UPDATE;

-- name: SetPaymentMatch :exec
UPDATE payments
   SET invoice_id = $2, customer_id = $3, status = $4, adjustment_type = $5
 WHERE payment_id = $1;

-- name: SetPaymentStatus :exec
UPDATE payments SET status = $2 WHERE payment_id = $1;

-- name: SetPaymentWithheld :exec
UPDATE payments SET wht_amount = $2 WHERE payment_id = $1;

-- name: InsertReconLog :exec
-- created_at is the column's default (CURRENT_TIMESTAMP).
INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes, actor_user_id)
VALUES ($1, $2, $3, sqlc.arg(reasons)::text[], sqlc.arg(reason_codes)::text[], $4);

-- name: LockInvoice :one
-- The invoice a payment settles, locked until the transaction ends so its
-- balance, booked withholding and status are not changed under the clearing.
-- FOR NO KEY UPDATE, not FOR UPDATE: inserting a payment takes FOR KEY SHARE
-- on its invoice through payments_invoice_id_fkey, which FOR UPDATE would
-- wait on, so two intakes for one invoice (insert, insert, lock, lock) would
-- deadlock. NO KEY UPDATE still conflicts with itself and with FOR UPDATE.
SELECT invoice_id, customer_id, currency, status, amount, balance_due, wht_amount, wht_booked, issue_date, due_date
  FROM invoices
 WHERE invoice_id = $1
   FOR NO KEY UPDATE;

-- name: SetInvoiceSettlement :exec
UPDATE invoices
   SET balance_due = $2, wht_booked = $3, status = $4
 WHERE invoice_id = $1;

-- name: OpenCandidates :many
-- The open invoices in the payment's currency with their customers (Python
-- SR:47-68: an inner join, so an invoice without a customer is never a
-- candidate). Not locked: the one that clears is locked and checked again.
-- Python had no ORDER BY; the engine orders the candidates itself.
SELECT i.invoice_id, c.customer_id, c.name AS customer_name, c.matching_aliases,
       i.currency, i.status, i.balance_due, i.wht_amount, i.wht_booked, i.issue_date, i.due_date
  FROM invoices i
  JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.status IN ('OPEN', 'SENT', 'PARTIAL')
   AND i.currency = $1
 ORDER BY i.invoice_id;

-- name: InsertManualPayment :one
-- record-payment's row: a CLEARED MANUAL payment against a known invoice with
-- its withheld amount set at once and no confidence. external_ref is random
-- (manual-<12 hex>), so it never meets the unique key.
INSERT INTO payments (payment_id, invoice_id, customer_id, amount, wht_amount, currency, payer_name, payer_reference,
                      payment_date, intake_source, external_ref, status, adjustment_type)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'MANUAL', $10, 'CLEARED', 'NONE')
RETURNING *;

-- name: InvoiceExists :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE invoice_id = $1);

-- name: ReceiptInvoice :one
-- What a payment receipt needs of its invoice and customer (Python
-- RI:650-662); the customer is NULL for an invoice without one.
SELECT i.invoice_id, i.invoice_number, i.currency, i.balance_due,
       c.name AS customer_name, c.contact_email AS customer_email
  FROM invoices i
  LEFT JOIN customers c ON c.customer_id = i.customer_id
 WHERE i.invoice_id = $1;

-- name: BusinessName :one
SELECT name FROM business_profile WHERE id = 1;

-- name: ListInvoicePayments :many
-- The CLEARED payments of an invoice, newest first (Python ordered by date
-- and created_at only; the key breaks the tie of one transaction's rows).
SELECT * FROM payments
 WHERE invoice_id = $1 AND status = 'CLEARED'
 ORDER BY payment_date DESC, created_at DESC, payment_id DESC;

-- name: LastCustomerPayer :many
-- The payer name of the newest CLEARED payment from the invoice's customer,
-- on any of its invoices: what the record-payment form suggests. A manual
-- payment that named no payer holds the placeholder and is skipped. At most
-- one row; none when the customer has no named payment yet.
SELECT p.payer_name
  FROM invoices i
  JOIN payments p ON p.customer_id = i.customer_id
 WHERE i.invoice_id = $1 AND p.status = 'CLEARED'
   AND btrim(p.payer_name) <> '' AND p.payer_name <> @manual_payer::text
 ORDER BY p.payment_date DESC, p.created_at DESC, p.payment_id DESC
 LIMIT 1;

-- name: ListInvoiceClearTimes :many
-- When each payment of an invoice was last cleared: its newest CLEARED
-- (auto-clear, manual entry) or OVERRIDDEN (manual approval) log row. The
-- log's created_at is the transaction's start, and relief is applied in the
-- order payments clear, which payment_date does not give (pdf.BalanceAfter).
SELECT l.payment_id, MAX(l.created_at)::timestamp AS cleared_at
  FROM reconciliation_log l
  JOIN payments p ON p.payment_id = l.payment_id
 WHERE p.invoice_id = $1 AND l.action IN ('CLEARED', 'OVERRIDDEN')
   -- created_at is nullable: a payment whose rows all lack it has no
   -- clear time (it sorts first) instead of a NULL MAX the scan rejects.
   AND l.created_at IS NOT NULL
 GROUP BY l.payment_id;

-- name: ListAwaitingReview :many
-- Python ordered by created_at DESC only; the key breaks a tie. A null
-- row_limit lists them all (the route); the MCP queue tool caps it.
SELECT * FROM payments
 WHERE status = 'PENDING_MANUAL_REVIEW'
 ORDER BY created_at DESC, payment_id DESC
 LIMIT sqlc.narg(row_limit);

-- name: PaymentLog :many
SELECT * FROM reconciliation_log
 WHERE payment_id = $1
 ORDER BY created_at ASC, log_id ASC;

-- name: AuditLog :many
-- The log joined to its payment and actor. payer is a substring already
-- escaped for LIKE (\, % and _ with a backslash). from_date starts at its
-- midnight and to_date runs to the end of its day. Python had no tie-break.
SELECT l.log_id, l.payment_id, l.action, l.reasons, l.reason_codes, l.actor_user_id, l.created_at,
       p.payer_name, p.amount, p.currency, p.invoice_id, p.intake_source,
       u.email AS actor_email,
       p.payment_date, p.confidence_score, i.invoice_number,
       c.name AS customer_name, u.display_name AS actor_display_name
  FROM reconciliation_log l
  LEFT JOIN payments p ON p.payment_id = l.payment_id
  LEFT JOIN users u ON u.user_id = l.actor_user_id
  LEFT JOIN invoices i ON i.invoice_id = p.invoice_id
  LEFT JOIN customers c ON c.customer_id = COALESCE(p.customer_id, i.customer_id)
 WHERE (sqlc.narg(action)::text IS NULL OR l.action = sqlc.narg(action)::text)
   AND (sqlc.narg(payer)::text IS NULL OR p.payer_name ILIKE '%' || sqlc.narg(payer)::text || '%' ESCAPE '\')
   AND (sqlc.narg(from_date)::date IS NULL OR l.created_at >= sqlc.narg(from_date)::date)
   AND (sqlc.narg(to_date)::date IS NULL OR l.created_at < sqlc.narg(to_date)::date + 1)
 ORDER BY l.created_at DESC, l.log_id DESC
 LIMIT sqlc.arg(row_limit);
