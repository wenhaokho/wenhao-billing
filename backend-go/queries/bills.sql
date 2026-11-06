-- name: InsertBill :one
INSERT INTO bills(bill_id,vendor_id,project_id,bill_number,po_number,currency,subtotal,discount_type,discount_value,amount,balance_due,status,issue_date,due_date,payment_terms,notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,'DRAFT',$11,$12,$13,$14) RETURNING *;

-- name: InsertBillLine :exec
INSERT INTO bill_line_items(line_item_id,bill_id,item_id,expense_account_id,position,description,quantity,unit_price,amount)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: BillLines :many
SELECT * FROM bill_line_items WHERE bill_id = ANY(sqlc.arg(bill_ids)::uuid[]) ORDER BY position;

-- name: GetBill :one
SELECT * FROM bills WHERE bill_id=$1;

-- name: ListBills :many
SELECT * FROM bills WHERE (sqlc.narg(vendor_id)::uuid IS NULL OR vendor_id=sqlc.narg(vendor_id)) AND (sqlc.arg(all_status)::boolean OR status=ANY(sqlc.arg(statuses)::text[])) ORDER BY created_at DESC;

-- name: LockBill :one
SELECT * FROM bills WHERE bill_id=$1 FOR UPDATE;

-- name: UpdateBill :one
UPDATE bills SET vendor_id=$2, project_id=$3, bill_number=$4, po_number=$5, currency=$6, subtotal=$7, discount_type=$8, discount_value=$9, amount=$10, balance_due=$11, status=$12, issue_date=$13, due_date=$14, payment_terms=$15, notes=$16 WHERE bill_id=$1 RETURNING *;

-- name: DeleteBillLines :exec
DELETE FROM bill_line_items WHERE bill_id=$1;
