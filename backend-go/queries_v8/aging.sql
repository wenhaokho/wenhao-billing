-- name: CurrentAgingDocuments :many
SELECT COALESCE(i.customer_id::text, '-')::text AS party_id,
       COALESCE(NULLIF(c.name, ''), '(no customer)')::text AS party_name,
       i.currency, i.issue_date, i.due_date, i.balance_due::text AS balance,
       (i.customer_id IS NOT NULL AND c.customer_id IS NULL) AS damaged_reference
FROM invoices i
LEFT JOIN customers c ON c.business_id = i.business_id AND c.customer_id = i.customer_id
WHERE sqlc.arg(kind)::text = 'AR' AND i.business_id = sqlc.arg(business_id)::uuid
  AND (i.issue_date IS NULL OR i.issue_date <= sqlc.arg(as_of)::date)
  AND i.status IN ('OPEN', 'SENT', 'PARTIAL') AND NOT i.is_template AND i.balance_due > 0
UNION ALL
SELECT b.vendor_id::text, COALESCE(NULLIF(v.name, ''), '(no vendor)')::text,
       b.currency, b.issue_date, b.due_date, b.balance_due::text,
       v.vendor_id IS NULL
FROM bills b
LEFT JOIN vendors v ON v.business_id = b.business_id AND v.vendor_id = b.vendor_id
WHERE sqlc.arg(kind)::text = 'AP' AND b.business_id = sqlc.arg(business_id)::uuid
  AND (b.issue_date IS NULL OR b.issue_date <= sqlc.arg(as_of)::date)
  AND b.status IN ('OPEN', 'PARTIAL') AND b.balance_due > 0;
