-- name: GetInvoiceSummaryBuckets :many
SELECT i.currency, (i.due_date < sqlc.arg(today)::date)::boolean AS overdue,
       SUM(i.balance_due)::numeric AS amount
FROM invoices i
WHERE i.business_id = sqlc.arg(business_id)::uuid
  AND i.status IN ('OPEN', 'SENT', 'PARTIAL')
  AND i.is_template IS FALSE
  AND i.balance_due > 0
  AND i.due_date <= sqlc.arg(today)::date + 30
GROUP BY i.currency, overdue
ORDER BY i.currency;

-- name: GetInvoiceSummaryPaymentDays :one
SELECT COUNT(*) AS payment_count,
       COALESCE(SUM((p.payment_date - i.issue_date)::bigint), 0)::numeric AS total_days
FROM payments p
JOIN invoices i ON i.business_id = p.business_id AND i.invoice_id = p.invoice_id
WHERE p.business_id = sqlc.arg(business_id)::uuid
  AND i.status = 'PAID'
  AND i.issue_date IS NOT NULL
  AND p.status = 'CLEARED'
  AND p.payment_date >= sqlc.arg(today)::date - 90;
