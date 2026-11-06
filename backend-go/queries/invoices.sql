-- name: GetInvoice :one
SELECT * FROM invoices WHERE invoice_id = $1;

-- name: ListInvoices :many
SELECT * FROM invoices WHERE is_template = false AND (sqlc.arg(all_status)::boolean OR status = ANY(sqlc.arg(statuses)::text[])) AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)) AND (sqlc.narg(invoice_type)::text IS NULL OR invoice_type = sqlc.narg(invoice_type)) ORDER BY created_at DESC LIMIT sqlc.arg(row_limit)::bigint;

-- name: AwaitingFinalization :many
SELECT * FROM invoices WHERE status = 'DRAFT' AND is_template = false ORDER BY created_at DESC;

-- name: InvoiceLinesByIDs :many
SELECT * FROM invoice_line_items WHERE invoice_id = ANY(sqlc.arg(invoice_ids)::uuid[]) ORDER BY position;

-- name: ListRecurringTemplates :many
SELECT * FROM invoices WHERE is_template = true ORDER BY created_at DESC;

-- name: InsertDraftInvoice :exec
INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,invoice_number,po_so_number,currency,subtotal,discount_type,discount_value,wht_type,wht_rate,wht_gross_up,wht_base,wht_amount,amount,balance_due,status,issue_date,due_date,payment_terms,notes,footer,internal_note,is_template)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16,'DRAFT',$17,$18,$19,$20,$21,$22,false);

-- name: MarkInvoiceTemplate :exec
UPDATE invoices SET is_template=true, billing_cycle_ref=$2 WHERE invoice_id=$1;

-- name: InsertDraftInvoiceLine :exec
INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: LockInvoiceNumberAllocation :exec
SELECT pg_advisory_xact_lock(749221001);

-- name: InvoiceNumbersForPrefix :many
SELECT invoice_number FROM invoices WHERE invoice_number LIKE $1;

-- name: CustomerWithholdingDefaults :one
SELECT default_wht_type,default_wht_rate,default_wht_gross_up FROM customers WHERE customer_id=$1;

-- name: LockInvoice :one
SELECT * FROM invoices WHERE invoice_id = $1 FOR UPDATE;

-- name: TransitionInvoice :exec
UPDATE invoices SET status = $2, balance_due = $3 WHERE invoice_id = $1;

-- name: UpdateDraftInvoice :exec
UPDATE invoices SET customer_id=$2, project_id=$3, invoice_type=$4, currency=$5, invoice_number=$6, po_so_number=$7, issue_date=$8, due_date=$9, payment_terms=$10, notes=$11, footer=$12, internal_note=$13, discount_type=$14, discount_value=$15, wht_type=$16, wht_rate=$17, wht_gross_up=$18, subtotal=$19, wht_base=$20, wht_amount=$21, amount=$22, balance_due=$22 WHERE invoice_id=$1;

-- name: DeleteInvoiceLines :exec
DELETE FROM invoice_line_items WHERE invoice_id=$1;

-- name: LockInvoiceLines :many
SELECT * FROM invoice_line_items WHERE invoice_id=$1 ORDER BY position FOR UPDATE;

-- name: InsertMilestoneInvoice :exec
INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,currency,wht_type,wht_rate,wht_gross_up,wht_base,wht_amount,amount,balance_due,status,billing_cycle_ref,issue_date,due_date,is_template)
VALUES ($1,$2,$3,'MILESTONE',$4,$5,$6,$7,$8,$9,$10,$10,'DRAFT',$11,$12,$13,false);

-- name: LockMilestoneCustomer :one
SELECT default_wht_type,default_wht_rate,default_wht_gross_up FROM customers WHERE customer_id=$1 FOR SHARE;

-- name: LockMilestoneProject :one
SELECT project_id FROM projects WHERE project_id=$1 FOR SHARE;

-- name: DeleteInvoice :exec
DELETE FROM invoices WHERE invoice_id=$1;

-- name: RecurringTemplateRows :many
WITH children AS MATERIALIZED (
  SELECT billing_cycle_ref->>'template_invoice_id' AS template_id, issue_date, billing_cycle_ref,
         count(*) OVER (PARTITION BY billing_cycle_ref->>'template_invoice_id') AS generated_count,
         row_number() OVER (PARTITION BY billing_cycle_ref->>'template_invoice_id' ORDER BY issue_date DESC NULLS LAST) AS position
  FROM invoices
  WHERE NOT is_template AND invoice_type = 'RECURRING' AND status <> 'VOID'
)
SELECT t.invoice_id, t.customer_id, c.name AS customer_name, t.currency, t.amount,
       t.payment_terms, t.billing_cycle_ref, t.created_at,
       COALESCE(ch.generated_count, 0)::bigint AS generated_count,
       ch.issue_date AS previous_issue_date, ch.billing_cycle_ref AS previous_ref
FROM invoices t
LEFT JOIN customers c ON c.customer_id = t.customer_id
LEFT JOIN children ch ON ch.template_id = t.invoice_id::text AND ch.position = 1
WHERE t.is_template ORDER BY t.created_at DESC;

-- name: LatestRecurringChild :one
SELECT issue_date, billing_cycle_ref FROM invoices
WHERE NOT is_template AND invoice_type = 'RECURRING' AND status <> 'VOID'
AND billing_cycle_ref->>'template_invoice_id' = sqlc.arg(template_id)::uuid::text
ORDER BY issue_date DESC NULLS LAST LIMIT 1;

-- name: UpdateTemplateSchedule :exec
UPDATE invoices SET billing_cycle_ref=$2 WHERE invoice_id=$1;

-- name: FindRecurringCycle :one
SELECT * FROM invoices WHERE invoice_type='RECURRING' AND NOT is_template AND billing_cycle_ref->>'template_invoice_id'=sqlc.arg(template_id)::uuid::text AND jsonb_typeof(billing_cycle_ref->'cycle_key')='string' AND billing_cycle_ref->>'cycle_key'=sqlc.arg(cycle_key)::text LIMIT 1;

-- name: SetRecurringChildFields :exec
UPDATE invoices SET billing_cycle_ref=$2, coverage_start=$3, coverage_end=$4 WHERE invoice_id=$1;

-- name: ItemIsHosting :one
SELECT is_hosting FROM items WHERE item_id=$1 FOR SHARE;

-- name: ListRecurringCycleCandidates :many
SELECT * FROM invoices WHERE invoice_type='RECURRING' AND NOT is_template AND billing_cycle_ref->>'template_invoice_id'=sqlc.arg(template_id)::uuid::text ORDER BY created_at, invoice_id;

-- name: LockUsageInvoice :exec
UPDATE invoices SET wht_base=$2, wht_amount=$3, amount=$4, balance_due=$4,
 billing_cycle_ref=sqlc.arg(billing_cycle_ref)::jsonb
WHERE invoice_id=$1;

-- name: ApplyInvoicePayment :exec
UPDATE invoices SET balance_due=$2, wht_booked=$3, status=$4 WHERE invoice_id=$1;

-- name: InsertInvoicePayment :one
INSERT INTO payments(payment_id,invoice_id,customer_id,amount,wht_amount,currency,payer_name,payer_reference,payment_date,intake_source,external_ref,status,adjustment_type)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'MANUAL',$10,'CLEARED','NONE') RETURNING *;

-- name: InsertInvoicePaymentLog :exec
INSERT INTO reconciliation_log(log_id,payment_id,action,reasons,actor_user_id) VALUES ($1,$2,'CLEARED',$3,$4);
