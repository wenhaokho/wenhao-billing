-- name: GetInvoice :one
SELECT * FROM invoices WHERE invoice_id = $1;

-- name: ListInvoiceLines :many
-- Python's relationship orders by position only; line_item_id makes ties
-- stable.
SELECT * FROM invoice_line_items WHERE invoice_id = $1 ORDER BY position, line_item_id;

-- name: GetCustomerWHTDefaults :one
SELECT default_wht_type, default_wht_rate, default_wht_gross_up FROM customers WHERE customer_id = $1;

-- name: LockInvoiceNumbers :exec
-- Held until the caller's transaction ends: one number at a time per year.
SELECT pg_advisory_xact_lock(hashtextextended('invoice-number:' || @year::text, 0));

-- name: ListInvoiceNumbersWithPrefix :many
SELECT invoice_number::text FROM invoices
WHERE invoice_number LIKE @prefix::text || '%';

-- name: GetInvoiceForUpdate :one
SELECT * FROM invoices WHERE invoice_id = $1 FOR UPDATE;

-- name: InsertInvoice :exec
INSERT INTO invoices (
    invoice_id, customer_id, project_id, invoice_type, invoice_number, po_so_number, currency,
    subtotal, discount_type, discount_value, wht_type, wht_rate, wht_gross_up, wht_base, wht_amount,
    amount, balance_due, status, billing_cycle_ref, issue_date, due_date, payment_terms, notes, footer,
    internal_note, is_template
) VALUES (
    @invoice_id, @customer_id, @project_id, @invoice_type, @invoice_number, @po_so_number, @currency,
    @subtotal, @discount_type, @discount_value, @wht_type, @wht_rate, @wht_gross_up, @wht_base, @wht_amount,
    @amount, @balance_due, @status, @billing_cycle_ref, @issue_date, @due_date, @payment_terms, @notes, @footer,
    @internal_note, @is_template
);

-- name: UpdateInvoice :exec
-- The columns a PATCH writes (the editable fields, withholding and totals).
UPDATE invoices SET
    customer_id = @customer_id, project_id = @project_id, invoice_type = @invoice_type,
    invoice_number = @invoice_number, po_so_number = @po_so_number, currency = @currency,
    subtotal = @subtotal, discount_type = @discount_type, discount_value = @discount_value,
    wht_type = @wht_type, wht_rate = @wht_rate, wht_gross_up = @wht_gross_up, wht_base = @wht_base,
    wht_amount = @wht_amount, amount = @amount, balance_due = @balance_due,
    issue_date = @issue_date, due_date = @due_date, payment_terms = @payment_terms,
    notes = @notes, footer = @footer, internal_note = @internal_note
WHERE invoice_id = @invoice_id;

-- name: InsertInvoiceLine :exec
INSERT INTO invoice_line_items (line_item_id, invoice_id, item_id, position, description, quantity, unit_price, amount)
VALUES (@line_item_id, @invoice_id, @item_id, @position, @description, @quantity, @unit_price, @amount);

-- name: DeleteInvoiceLines :exec
DELETE FROM invoice_line_items WHERE invoice_id = $1;

-- name: ListInvoices :many
-- GET /invoices (invoices.py:426-450): never templates; each filter applies
-- when given; newest first, the id breaking ties.
SELECT * FROM invoices
WHERE is_template = false
  AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(invoice_type)::text IS NULL OR invoice_type = sqlc.narg(invoice_type)::text)
ORDER BY created_at DESC, invoice_id DESC
LIMIT @row_limit;

-- name: ListAwaitingFinalization :many
-- invoices.py:411-423: every DRAFT that is not a template, no limit.
SELECT * FROM invoices
WHERE status = 'DRAFT' AND is_template = false
ORDER BY created_at DESC, invoice_id DESC;

-- name: ListLinesOfInvoices :many
-- The lines of a page of invoices in one query, each invoice's in
-- ListInvoiceLines' order.
SELECT * FROM invoice_line_items
WHERE invoice_id = ANY (@invoice_ids::uuid[])
ORDER BY invoice_id, position, line_item_id;

-- name: SetInvoiceStatus :exec
-- The lifecycle transitions: the new status with the balance it leaves.
UPDATE invoices SET status = @status, balance_due = @balance_due WHERE invoice_id = @invoice_id;

-- name: DeleteInvoice :exec
-- The lines go with it (ON DELETE CASCADE).
DELETE FROM invoices WHERE invoice_id = $1;
