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
INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, actor_user_id)
VALUES ($1, $2, $3, sqlc.arg(reasons)::text[], $4);

-- name: LockInvoice :one
-- The invoice a payment settles, locked until the transaction ends so its
-- balance, booked withholding and status are not changed under the clearing.
-- FOR NO KEY UPDATE, not FOR UPDATE: inserting a payment takes FOR KEY SHARE
-- on its invoice through payments_invoice_id_fkey, which FOR UPDATE would
-- wait on, so two intakes for one invoice (insert, insert, lock, lock) would
-- deadlock. NO KEY UPDATE still conflicts with itself and with FOR UPDATE.
SELECT invoice_id, customer_id, currency, status, balance_due, wht_amount, wht_booked, issue_date, due_date
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
