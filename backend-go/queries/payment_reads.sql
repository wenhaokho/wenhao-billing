-- name: ListPaymentsAwaitingReview :many
SELECT * FROM payments WHERE status = 'PENDING_MANUAL_REVIEW' ORDER BY created_at DESC;

-- name: GetPayment :one
SELECT * FROM payments WHERE payment_id = $1;

-- name: PaymentLogs :many
SELECT * FROM reconciliation_log WHERE payment_id = $1 ORDER BY created_at ASC;

-- name: PaymentAudit :many
SELECT l.log_id, l.payment_id, l.action, l.reasons, l.actor_user_id, l.created_at,
 p.payer_name, p.amount, p.currency, p.invoice_id, p.intake_source, u.email AS actor_email
FROM reconciliation_log l LEFT JOIN payments p ON p.payment_id=l.payment_id
LEFT JOIN users u ON u.user_id=l.actor_user_id
WHERE (sqlc.narg(action)::text IS NULL OR l.action=sqlc.narg(action))
 AND (sqlc.narg(payer)::text IS NULL OR p.payer_name ILIKE '%' || sqlc.narg(payer)::text || '%')
 AND (sqlc.narg(from_date)::timestamp IS NULL OR l.created_at >= sqlc.narg(from_date))
 AND (sqlc.narg(to_date)::timestamp IS NULL OR l.created_at < sqlc.narg(to_date))
ORDER BY l.created_at DESC LIMIT sqlc.arg(row_limit);

-- name: ListInvoicePayments :many
SELECT * FROM payments WHERE invoice_id = $1 AND status = 'CLEARED' ORDER BY payment_date DESC, created_at DESC;
