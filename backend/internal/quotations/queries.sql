-- name: GetQuotation :one
SELECT * FROM quotations WHERE quotation_id = $1;

-- name: GetQuotationForUpdate :one
-- Every write reads the quotation locked, so a PATCH, a transition or a
-- delete sees the status the write acts on.
SELECT * FROM quotations WHERE quotation_id = $1 FOR UPDATE;

-- name: ListQuotationLines :many
-- Python's relationship orders by position only; line_item_id makes ties
-- stable.
SELECT * FROM quotation_line_items WHERE quotation_id = $1 ORDER BY position, line_item_id;

-- name: ListLinesOfQuotations :many
-- The lines of a page of quotations in one query, each quotation's in
-- ListQuotationLines' order.
SELECT * FROM quotation_line_items
WHERE quotation_id = ANY (@quotation_ids::uuid[])
ORDER BY quotation_id, position, line_item_id;

-- name: LockQuotationNumbers :exec
-- Held until the caller's transaction ends: one number at a time per year.
SELECT pg_advisory_xact_lock(hashtextextended('quotation-number:' || @year::text, 0));

-- name: ListQuotationNumbersWithPrefix :many
SELECT quotation_number::text FROM quotations
WHERE quotation_number LIKE @prefix::text || '%';

-- name: InsertQuotation :exec
INSERT INTO quotations (
    quotation_id, customer_id, project_id, quotation_number, po_so_number, currency, subtotal,
    discount_type, discount_value, amount, status, issue_date, valid_until, payment_terms, notes, footer
) VALUES (
    @quotation_id, @customer_id, @project_id, @quotation_number, @po_so_number, @currency, @subtotal,
    @discount_type, @discount_value, @amount, @status, @issue_date, @valid_until, @payment_terms, @notes, @footer
);

-- name: UpdateQuotation :exec
-- The columns a PATCH writes (the editable fields and the totals), written
-- back from the row read with GetQuotationForUpdate.
UPDATE quotations SET
    customer_id = @customer_id, project_id = @project_id, quotation_number = @quotation_number,
    po_so_number = @po_so_number, currency = @currency, subtotal = @subtotal,
    discount_type = @discount_type, discount_value = @discount_value, amount = @amount,
    issue_date = @issue_date, valid_until = @valid_until, payment_terms = @payment_terms,
    notes = @notes, footer = @footer
WHERE quotation_id = @quotation_id;

-- name: InsertQuotationLine :exec
INSERT INTO quotation_line_items (line_item_id, quotation_id, item_id, position, description, quantity, unit_price, amount)
VALUES (@line_item_id, @quotation_id, @item_id, @position, @description, @quantity, @unit_price, @amount);

-- name: DeleteQuotationLines :exec
DELETE FROM quotation_line_items WHERE quotation_id = $1;

-- name: ListQuotations :many
-- GET /quotations (quotations.py:46-65): each filter applies when given (a
-- null or empty statuses list is no filter); newest first, the id breaking
-- ties.
SELECT * FROM quotations
WHERE (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
ORDER BY created_at DESC, quotation_id DESC
LIMIT @row_limit;

-- name: DeleteQuotation :exec
-- The lines go with it (ON DELETE CASCADE).
DELETE FROM quotations WHERE quotation_id = $1;

-- name: MarkQuotationSent :exec
UPDATE quotations SET status = 'SENT', last_sent_at = @last_sent_at WHERE quotation_id = @quotation_id;

-- name: AcceptQuotation :exec
UPDATE quotations SET status = 'ACCEPTED', accepted_at = @accepted_at, accepted_by = @accepted_by
WHERE quotation_id = @quotation_id;

-- name: MarkQuotationInvoiced :exec
-- A conversion: the quotation points at the invoice it became.
UPDATE quotations SET status = 'INVOICED', converted_invoice_id = @converted_invoice_id
WHERE quotation_id = @quotation_id;

-- name: SetQuotationStatus :exec
-- Decline and void: the status alone.
UPDATE quotations SET status = @status WHERE quotation_id = @quotation_id;
