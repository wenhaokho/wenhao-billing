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

-- name: ListInvoicesBy :many
-- ListBy's loader (the project routes): optionally one project's, optionally
-- without templates; ListInvoices' order.
SELECT * FROM invoices
WHERE (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (NOT @exclude_templates::boolean OR is_template = false)
ORDER BY created_at DESC, invoice_id DESC;

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

-- name: ListTemplates :many
-- GET /invoices/recurring-templates (invoices.py:104-115): newest first,
-- the id breaking ties.
SELECT * FROM invoices
WHERE is_template = true
ORDER BY created_at DESC, invoice_id DESC;

-- name: ListTemplateRows :many
-- The templates of the rows view (invoices.py:215-284) with their
-- customer's name, in ListTemplates' order.
SELECT i.invoice_id, i.customer_id, c.name AS customer_name, i.currency, i.amount, i.payment_terms,
       i.billing_cycle_ref
FROM invoices i
LEFT JOIN customers c ON c.customer_id = i.customer_id
WHERE i.is_template = true
ORDER BY i.created_at DESC, i.invoice_id DESC;

-- name: ListLatestTemplateChildren :many
-- The live generated children of templates (_generated_children,
-- invoices.py:135-153: not templates, RECURRING, not VOID), one row per
-- template: how many there are, and the cycle_key ("" when absent) and
-- issue_date of the one with the latest issue_date (NULLs last; the newest
-- breaks ties). A null template_id answers for every template.
SELECT DISTINCT ON (c.template_invoice_id)
    c.template_invoice_id::text AS template_invoice_id,
    c.generated::bigint AS generated,
    COALESCE(c.cycle_key, '')::text AS cycle_key,
    c.issue_date
FROM (
    SELECT billing_cycle_ref->>'template_invoice_id' AS template_invoice_id,
           billing_cycle_ref->>'cycle_key' AS cycle_key,
           issue_date, created_at, invoice_id,
           count(*) OVER (PARTITION BY billing_cycle_ref->>'template_invoice_id') AS generated
    FROM invoices
    WHERE is_template = false AND invoice_type = 'RECURRING' AND status <> 'VOID'
      AND billing_cycle_ref->>'template_invoice_id' IS NOT NULL
      AND (sqlc.narg(template_id)::text IS NULL
           OR billing_cycle_ref->>'template_invoice_id' = sqlc.narg(template_id)::text)
) c
ORDER BY c.template_invoice_id, c.issue_date DESC NULLS LAST, c.created_at DESC, c.invoice_id DESC;

-- name: UpdateTemplate :exec
-- The columns a template PUT writes (invoicing.py:442-456).
UPDATE invoices SET
    customer_id = @customer_id, project_id = @project_id, currency = @currency,
    po_so_number = @po_so_number, payment_terms = @payment_terms, notes = @notes, footer = @footer,
    internal_note = @internal_note, discount_type = @discount_type, discount_value = @discount_value,
    subtotal = @subtotal, wht_type = @wht_type, wht_rate = @wht_rate, wht_gross_up = @wht_gross_up,
    wht_base = @wht_base, wht_amount = @wht_amount, amount = @amount, balance_due = @balance_due,
    billing_cycle_ref = @billing_cycle_ref
WHERE invoice_id = @invoice_id;

-- name: SetBillingCycleRef :exec
UPDATE invoices SET billing_cycle_ref = @billing_cycle_ref WHERE invoice_id = @invoice_id;

-- name: LockRecurringTemplate :exec
-- Held until the caller's transaction ends: one trigger at a time per
-- template, so a manual trigger and the scan cannot both create a cycle.
SELECT pg_advisory_xact_lock(hashtextextended('recurring-template:' || @template_id::text, 0));

-- name: FindCycleInvoice :one
-- find_cycle_invoice (invoicing.py:477-498): the generated invoice of a
-- template's cycle, its cycle_key compared as text; the oldest when there
-- are several.
SELECT * FROM invoices
WHERE invoice_type = 'RECURRING' AND is_template = false
  AND billing_cycle_ref->>'template_invoice_id' = @template_id::text
  AND billing_cycle_ref->>'cycle_key' = @cycle_key::text
ORDER BY created_at, invoice_id
LIMIT 1;

-- name: InvoiceHasHostingItem :one
-- hosting_items_in_invoice (hosting.py:54-58): does any line's item host?
SELECT EXISTS (
    SELECT 1 FROM invoice_line_items l JOIN items i ON i.item_id = l.item_id
    WHERE l.invoice_id = $1 AND i.is_hosting
);

-- name: SetInvoiceCoverage :exec
UPDATE invoices SET coverage_start = @coverage_start, coverage_end = @coverage_end WHERE invoice_id = @invoice_id;

-- name: GetCustomerName :one
SELECT name FROM customers WHERE customer_id = $1;

-- name: ListScanTemplates :many
-- The templates the recurring scan visits (workers/tasks/recurring.py:52-56:
-- no status filter), oldest first, the id breaking ties.
SELECT invoice_id, billing_cycle_ref FROM invoices
WHERE invoice_type = 'RECURRING' AND is_template = true
ORDER BY created_at, invoice_id;

-- name: LockUsageInvoice :exec
-- Usage Lock (invoicing.py:604-640): the recomputed withholding and totals,
-- and the cycle config with its cut-off and lock marks. The status stays.
UPDATE invoices SET
    wht_base = @wht_base, wht_amount = @wht_amount, amount = @total, balance_due = @total,
    billing_cycle_ref = @billing_cycle_ref
WHERE invoice_id = @invoice_id;

-- name: ListUsageDrafts :many
-- The cut-off scan's candidates (workers/tasks/usage_lock.py:34-38), oldest
-- first, the id breaking ties.
SELECT invoice_id, billing_cycle_ref FROM invoices
WHERE invoice_type = 'USAGE' AND status = 'DRAFT'
ORDER BY created_at, invoice_id;

-- name: ListCustomerBalances :many
-- Open AR per (customer, currency) with the overdue part split out
-- (stats.py:401-434): OPEN, SENT and PARTIAL, never templates, nothing at
-- zero. Python has no ORDER BY; this one is customer, currency.
SELECT customer_id, currency,
       SUM(balance_due)::numeric AS balance,
       SUM(CASE WHEN due_date < @today::date THEN balance_due ELSE 0 END)::numeric AS overdue
FROM invoices
WHERE status IN ('OPEN', 'SENT', 'PARTIAL')
  AND is_template = false
  AND balance_due > 0
  AND customer_id IS NOT NULL
GROUP BY customer_id, currency
ORDER BY customer_id, currency;

-- name: ListOpenInvoicesOfCustomer :many
-- The billing summary's open invoices (customers.py:157-164): DRAFT, OPEN,
-- SENT and PARTIAL, templates included, due date ascending (NULLs last), then
-- creation.
SELECT * FROM invoices
WHERE customer_id = @customer_id AND status IN ('DRAFT', 'OPEN', 'SENT', 'PARTIAL')
ORDER BY due_date ASC, created_at ASC, invoice_id ASC;

-- name: GetLatestInvoiceOfCustomer :one
-- customers.py:165-170: the latest issue date (a NULL one first, as Postgres
-- orders Python's DESC), then the newest.
SELECT invoice_id, issue_date FROM invoices
WHERE customer_id = $1
ORDER BY issue_date DESC NULLS FIRST, created_at DESC, invoice_id DESC
LIMIT 1;

-- name: SumClearedPaymentsSince :many
-- customers.py:172-198: the cash (amount, never the withholding) of the
-- customer's CLEARED payments dated on or after since, per currency.
SELECT currency, SUM(amount)::numeric AS amount
FROM payments
WHERE customer_id = $1 AND status = 'CLEARED' AND payment_date >= @since::date
GROUP BY currency
ORDER BY currency;

-- name: ListInvoicesOfCustomer :many
-- customers.py:204-219: every invoice of the customer, templates included,
-- latest issue date first (NULLs first), no limit.
SELECT * FROM invoices
WHERE customer_id = $1
ORDER BY issue_date DESC NULLS FIRST, created_at DESC, invoice_id DESC;
