-- name: ListQuotations :many
SELECT * FROM quotations WHERE business_id=sqlc.arg(business_id)::uuid AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id=sqlc.narg(customer_id)) AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id)) AND (sqlc.arg(all_status)::boolean OR status=ANY(sqlc.arg(statuses)::text[])) ORDER BY created_at DESC LIMIT sqlc.arg(row_limit);
-- name: GetQuotation :one
SELECT * FROM quotations WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;
-- name: QuotationLines :many
SELECT * FROM quotation_line_items WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=ANY(sqlc.arg(quotation_ids)::uuid[]) ORDER BY position;
-- name: LockQuotationNumberAllocation :exec
SELECT pg_advisory_xact_lock(hashtextextended('quotation-number:' || sqlc.arg(business_id)::uuid::text, 0));
-- name: QuotationNumbersForPrefix :many
SELECT quotation_number FROM quotations WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_number LIKE $1;
-- name: InsertQuotation :exec
INSERT INTO quotations (business_id, quotation_id, customer_id, project_id, quotation_number, po_so_number, currency, subtotal, discount_type, discount_value, amount, status, issue_date, valid_until, payment_terms, notes, footer)
VALUES (sqlc.arg(business_id)::uuid,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'DRAFT',$11,$12,$13,$14,$15);
-- name: InsertQuotationLine :exec
INSERT INTO quotation_line_items (business_id, line_item_id, quotation_id, item_id, position, description, quantity, unit_price, amount)
VALUES (sqlc.arg(business_id)::uuid,$1,$2,$3,$4,$5,$6,$7,$8);
-- name: LockQuotation :one
SELECT * FROM quotations WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1 FOR UPDATE;
-- name: DeleteQuotationLines :exec
DELETE FROM quotation_line_items WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;
-- name: UpdateQuotation :exec
UPDATE quotations SET customer_id=$2,project_id=$3,currency=$4,quotation_number=$5,po_so_number=$6,issue_date=$7,valid_until=$8,payment_terms=$9,notes=$10,footer=$11,discount_type=$12,discount_value=$13,subtotal=$14,amount=$15 WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: DeleteQuotation :exec
DELETE FROM quotations WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: MarkQuotationSent :exec
UPDATE quotations SET status='SENT', last_sent_at=timezone('UTC', clock_timestamp())
WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: AcceptQuotation :exec
UPDATE quotations SET status='ACCEPTED', accepted_at=timezone('UTC', clock_timestamp()), accepted_by=$2
WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: DeclineQuotation :exec
UPDATE quotations SET status='DECLINED'
WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: VoidQuotation :exec
UPDATE quotations SET status='VOID'
WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: RecordQuotationSend :execrows
UPDATE quotations SET status=CASE WHEN status='DRAFT' THEN 'SENT' ELSE status END,
last_sent_at=timezone('UTC', clock_timestamp())
WHERE business_id=sqlc.arg(business_id)::uuid AND quotation_id=$1;

-- name: QuotationSendDetails :many
SELECT sqlc.embed(c), p.name AS profile_name, p.address AS profile_address,
       p.contact_email AS profile_email, p.contact_phone AS profile_phone, p.logo_url, p.default_notes,
       (a.id IS NOT NULL)::boolean AS has_account, COALESCE(a.currency, '')::text AS currency,
       COALESCE(a.instructions, '')::text AS instructions, COALESCE(a.is_default, false)::boolean AS is_default
FROM customers c
LEFT JOIN business_profile p ON p.business_id=c.business_id
LEFT JOIN payment_account a ON a.business_id=c.business_id AND a.business_profile_id=p.id
WHERE c.business_id=sqlc.arg(business_id)::uuid AND c.customer_id=sqlc.arg(customer_id)::uuid
ORDER BY a.currency;
