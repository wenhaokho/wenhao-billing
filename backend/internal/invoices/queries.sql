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
