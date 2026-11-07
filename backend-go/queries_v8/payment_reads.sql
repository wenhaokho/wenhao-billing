-- name: ListPaymentsAwaitingReview :many
SELECT * FROM payments WHERE business_id = sqlc.arg(business_id)::uuid AND status = 'PENDING_MANUAL_REVIEW' ORDER BY created_at DESC;

-- name: GetPayment :one
SELECT * FROM payments WHERE business_id = sqlc.arg(business_id)::uuid AND payment_id = sqlc.arg(payment_id);

-- name: PaymentLogs :many
SELECT l.log_id, l.action, l.reasons, u.user_id AS actor_user_id, l.created_at,
 ((l.actor_user_id IS NOT NULL AND u.user_id IS NULL)
 OR (p.invoice_id IS NOT NULL AND i.invoice_id IS NULL)
 OR (p.customer_id IS NOT NULL AND c.customer_id IS NULL)) AS damaged
FROM reconciliation_log l
JOIN payments p ON p.payment_id = l.payment_id AND p.business_id = l.business_id
LEFT JOIN users u ON u.user_id = l.actor_user_id AND u.business_id = l.business_id
LEFT JOIN invoices i ON i.invoice_id = p.invoice_id AND i.business_id = p.business_id
LEFT JOIN customers c ON c.customer_id = p.customer_id AND c.business_id = p.business_id
WHERE l.business_id = sqlc.arg(business_id)::uuid
 AND l.payment_id = sqlc.arg(payment_id)
ORDER BY l.created_at ASC;

-- name: PaymentAudit :many
SELECT l.log_id, p.payment_id, l.action, l.reasons, u.user_id AS actor_user_id, l.created_at,
 p.payer_name, p.amount, p.currency, i.invoice_id, p.intake_source, u.email AS actor_email,
 (p.payment_id IS NULL OR (l.actor_user_id IS NOT NULL AND u.user_id IS NULL)
 OR (p.invoice_id IS NOT NULL AND i.invoice_id IS NULL)
 OR (p.customer_id IS NOT NULL AND c.customer_id IS NULL)) AS damaged
FROM reconciliation_log l
LEFT JOIN payments p ON p.payment_id = l.payment_id AND p.business_id = l.business_id
LEFT JOIN users u ON u.user_id = l.actor_user_id AND u.business_id = l.business_id
LEFT JOIN invoices i ON i.invoice_id = p.invoice_id AND i.business_id = p.business_id
LEFT JOIN customers c ON c.customer_id = p.customer_id AND c.business_id = p.business_id
WHERE l.business_id = sqlc.arg(business_id)::uuid
 AND (sqlc.narg(action)::text IS NULL OR l.action=sqlc.narg(action))
 AND (sqlc.narg(payer)::text IS NULL OR p.payment_id IS NULL OR p.payer_name ILIKE '%' || sqlc.narg(payer)::text || '%')
 AND (sqlc.narg(from_date)::timestamp IS NULL OR l.created_at >= sqlc.narg(from_date))
 AND (sqlc.narg(to_date)::timestamp IS NULL OR l.created_at < sqlc.narg(to_date))
ORDER BY l.created_at DESC LIMIT sqlc.arg(row_limit);

-- name: ListInvoicePayments :many
SELECT * FROM payments
WHERE business_id = sqlc.arg(business_id)::uuid
  AND invoice_id = sqlc.arg(invoice_id)
  AND status = 'CLEARED'
  AND (customer_id IS NULL OR EXISTS (
    SELECT 1 FROM customers c
    WHERE c.business_id = payments.business_id AND c.customer_id = payments.customer_id
  ))
ORDER BY payment_date DESC, created_at DESC;

-- name: LockInvoiceReceiptPayment :one
SELECT * FROM payments
WHERE business_id = sqlc.arg(business_id)::uuid
  AND invoice_id = sqlc.arg(invoice_id)
  AND payment_id = sqlc.arg(payment_id)
  AND (customer_id IS NULL OR EXISTS (
    SELECT 1 FROM customers c
    WHERE c.business_id = payments.business_id AND c.customer_id = payments.customer_id
  ))
FOR UPDATE;
