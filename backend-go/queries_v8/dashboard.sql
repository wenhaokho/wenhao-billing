-- name: GetDashboardCounts :one
SELECT
    (SELECT COUNT(*) FROM payments p WHERE p.business_id = sqlc.arg(business_id)::uuid AND p.status = 'PENDING_MANUAL_REVIEW') AS awaiting_review_count,
    (SELECT COUNT(*) FROM invoices i WHERE i.business_id = sqlc.arg(business_id)::uuid AND i.status = 'DRAFT' AND i.is_template IS FALSE) AS draft_count,
    (SELECT COUNT(*) FROM invoices i WHERE i.business_id = sqlc.arg(business_id)::uuid AND i.status IN ('OPEN', 'SENT')) AS sent_count,
    (SELECT COUNT(*) FROM customers c WHERE c.business_id = sqlc.arg(business_id)::uuid) AS customer_count,
    (SELECT COUNT(*) FROM payments p WHERE p.business_id = sqlc.arg(business_id)::uuid AND p.status = 'CLEARED'
        AND p.created_at >= sqlc.arg(cleared_since)::timestamp) AS auto_cleared_last_30d,
    (SELECT COUNT(*) FROM quotations q WHERE q.business_id = sqlc.arg(business_id)::uuid AND q.status IN ('SENT', 'ACCEPTED')) AS open_quotation_count;

-- name: GetDashboardOpenAR :many
SELECT i.currency, SUM(i.balance_due)::numeric AS amount
FROM invoices i
WHERE i.business_id = sqlc.arg(business_id)::uuid AND i.status IN ('OPEN', 'SENT', 'PARTIAL')
GROUP BY i.currency
ORDER BY i.currency;

-- name: GetDashboardOpenQuotations :many
SELECT q.currency, SUM(q.amount)::numeric AS amount
FROM quotations q
WHERE q.business_id = sqlc.arg(business_id)::uuid AND q.status IN ('SENT', 'ACCEPTED')
GROUP BY q.currency
ORDER BY amount DESC, q.currency;

-- name: GetDashboardActivity :many
SELECT l.log_id, l.payment_id, l.action, l.reasons, u.user_id AS actor_user_id, l.created_at,
       COALESCE(p.payer_name, '')::text AS payer_name, p.amount, COALESCE(p.currency, '')::text AS currency, p.payment_date, p.payer_reference,
       p.wht_amount, p.confidence_score, COALESCE(p.intake_source, '')::text AS intake_source, i.invoice_id, i.invoice_number,
       c.name AS customer_name, u.display_name, u.email AS actor_email,
       (p.payment_id IS NULL OR (l.actor_user_id IS NOT NULL AND u.user_id IS NULL)
          OR (p.invoice_id IS NOT NULL AND i.invoice_id IS NULL)
          OR (COALESCE(p.customer_id, i.customer_id) IS NOT NULL AND c.customer_id IS NULL))::boolean AS invalid_reference
FROM reconciliation_log l
LEFT JOIN payments p ON p.business_id = l.business_id AND p.payment_id = l.payment_id
LEFT JOIN invoices i ON i.business_id = p.business_id AND i.invoice_id = p.invoice_id
LEFT JOIN customers c ON c.business_id = p.business_id AND c.customer_id = COALESCE(p.customer_id, i.customer_id)
LEFT JOIN users u ON u.business_id = l.business_id AND u.user_id = l.actor_user_id
WHERE l.business_id = sqlc.arg(business_id)::uuid
ORDER BY l.created_at DESC, l.log_id
LIMIT 10;

-- name: GetDashboardUpcomingSources :many
SELECT i.invoice_id, c.customer_id, c.name AS customer_name, i.invoice_type,
       i.billing_cycle_ref, i.amount, i.currency,
       (i.customer_id IS NOT NULL AND c.customer_id IS NULL)::boolean AS invalid_reference
FROM invoices i
LEFT JOIN customers c ON c.business_id = i.business_id AND c.customer_id = i.customer_id
WHERE i.business_id = sqlc.arg(business_id)::uuid
  AND ((i.invoice_type = 'RECURRING' AND i.is_template IS TRUE)
    OR (i.invoice_type = 'USAGE' AND i.status = 'DRAFT'
        AND COALESCE(i.billing_cycle_ref->'locked' IN ('null','false','0','""','[]','{}'), TRUE)))
ORDER BY i.invoice_id;
