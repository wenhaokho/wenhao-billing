-- name: GetBill :one
SELECT * FROM bills WHERE bill_id = $1;

-- name: GetBillForUpdate :one
-- Every write reads the bill locked, so a PATCH, a payment or a void sees
-- the status and balance it acts on (Python lost an update when two
-- payments raced).
SELECT * FROM bills WHERE bill_id = $1 FOR UPDATE;

-- name: ListBillLines :many
-- Python's relationship orders by position only; line_item_id makes ties
-- stable.
SELECT * FROM bill_line_items WHERE bill_id = $1 ORDER BY position, line_item_id;

-- name: ListLinesOfBills :many
-- The lines of every listed bill in one query, each bill's in ListBillLines'
-- order.
SELECT * FROM bill_line_items
WHERE bill_id = ANY (@bill_ids::uuid[])
ORDER BY bill_id, position, line_item_id;

-- name: ListBills :many
-- GET /bills (bills.py:34-49): each filter applies when given (a null or
-- empty statuses list is no filter); newest first, the id breaking ties. No
-- limit, as Python.
SELECT * FROM bills
WHERE (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(vendor_id)::uuid IS NULL OR vendor_id = sqlc.narg(vendor_id)::uuid)
ORDER BY created_at DESC, bill_id DESC;

-- name: InsertBill :exec
INSERT INTO bills (
    bill_id, vendor_id, project_id, bill_number, po_number, currency, subtotal,
    discount_type, discount_value, amount, balance_due, status, issue_date, due_date, payment_terms, notes
) VALUES (
    @bill_id, @vendor_id, @project_id, @bill_number, @po_number, @currency, @subtotal,
    @discount_type, @discount_value, @amount, @balance_due, @status, @issue_date, @due_date, @payment_terms, @notes
);

-- name: UpdateBill :exec
-- The columns a PATCH writes, written back from the row read with
-- GetBillForUpdate.
UPDATE bills SET
    vendor_id = @vendor_id, project_id = @project_id, bill_number = @bill_number, po_number = @po_number,
    currency = @currency, subtotal = @subtotal, discount_type = @discount_type, discount_value = @discount_value,
    amount = @amount, balance_due = @balance_due, status = @status, issue_date = @issue_date,
    due_date = @due_date, payment_terms = @payment_terms, notes = @notes
WHERE bill_id = @bill_id;

-- name: InsertBillLine :exec
INSERT INTO bill_line_items (line_item_id, bill_id, item_id, expense_account_id, position, description, quantity, unit_price, amount)
VALUES (@line_item_id, @bill_id, @item_id, @expense_account_id, @position, @description, @quantity, @unit_price, @amount);

-- name: DeleteBillLines :exec
DELETE FROM bill_line_items WHERE bill_id = $1;
