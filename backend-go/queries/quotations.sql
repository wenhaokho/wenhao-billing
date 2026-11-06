-- name: ListQuotations :many
SELECT * FROM quotations WHERE (sqlc.narg(customer_id)::uuid IS NULL OR customer_id=sqlc.narg(customer_id)) AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id)) AND (sqlc.arg(all_status)::boolean OR status=ANY(sqlc.arg(statuses)::text[])) ORDER BY created_at DESC LIMIT sqlc.arg(row_limit);
-- name: GetQuotation :one
SELECT * FROM quotations WHERE quotation_id=$1;
-- name: QuotationLines :many
SELECT * FROM quotation_line_items WHERE quotation_id=ANY(sqlc.arg(quotation_ids)::uuid[]) ORDER BY position;
-- name: LockQuotationNumberAllocation :exec
SELECT pg_advisory_xact_lock(472691842002::bigint);
-- name: QuotationNumbersForPrefix :many
SELECT quotation_number FROM quotations WHERE quotation_number LIKE $1;
-- name: InsertQuotation :exec
INSERT INTO quotations (quotation_id, customer_id, project_id, quotation_number, po_so_number, currency, subtotal, discount_type, discount_value, amount, status, issue_date, valid_until, payment_terms, notes, footer)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'DRAFT',$11,$12,$13,$14,$15);
-- name: InsertQuotationLine :exec
INSERT INTO quotation_line_items (line_item_id, quotation_id, item_id, position, description, quantity, unit_price, amount)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);
-- name: LockQuotation :one
SELECT * FROM quotations WHERE quotation_id=$1 FOR UPDATE;
-- name: DeleteQuotationLines :exec
DELETE FROM quotation_line_items WHERE quotation_id=$1;
-- name: UpdateQuotation :exec
UPDATE quotations SET customer_id=$2,project_id=$3,currency=$4,quotation_number=$5,po_so_number=$6,issue_date=$7,valid_until=$8,payment_terms=$9,notes=$10,footer=$11,discount_type=$12,discount_value=$13,subtotal=$14,amount=$15 WHERE quotation_id=$1;
