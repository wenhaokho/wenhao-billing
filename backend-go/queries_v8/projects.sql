-- name: ListProjects :many
SELECT * FROM projects WHERE business_id=sqlc.arg(business_id)::uuid AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)) AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) AND (sqlc.arg(all_status)::boolean OR status = ANY(sqlc.arg(statuses)::text[])) ORDER BY created_at DESC;

-- name: GetProject :one
SELECT * FROM projects WHERE business_id=sqlc.arg(business_id)::uuid AND project_id = $1;

-- name: ProjectInvoices :many
SELECT * FROM invoices WHERE business_id=sqlc.arg(business_id)::uuid AND project_id = $1 AND is_template = false ORDER BY created_at DESC;

-- name: ProjectInvoiceLines :many
SELECT l.* FROM invoice_line_items l JOIN invoices i ON i.invoice_id = l.invoice_id AND i.business_id=l.business_id WHERE i.business_id=sqlc.arg(business_id)::uuid AND i.project_id = $1 AND i.is_template = false ORDER BY l.position;

-- name: ProjectInvoiceStats :one
SELECT COALESCE(sum(amount), 0)::numeric AS invoiced, COALESCE(sum(balance_due), 0)::numeric AS outstanding, count(invoice_id)::bigint AS invoice_count FROM invoices WHERE business_id=sqlc.arg(business_id)::uuid AND project_id = $1 AND status IN ('OPEN','SENT','PARTIAL','PAID');

-- name: ProjectBillStats :one
SELECT COALESCE(sum(amount), 0)::numeric AS bill_total FROM bills WHERE business_id=sqlc.arg(business_id)::uuid AND project_id = $1 AND status IN ('OPEN','PARTIAL','PAID');

-- name: CreateProject :one
INSERT INTO projects (business_id,project_id,customer_id,code,name,currency,contract_value,status,start_date,end_date,notes,active)
SELECT c.business_id,gen_random_uuid(),c.customer_id,$2,$3,$4,$5,$6,$7,$8,$9,true FROM customers c WHERE c.business_id=sqlc.arg(business_id)::uuid AND c.customer_id=$1 RETURNING *;

-- name: LockProject :one
SELECT * FROM projects WHERE business_id=sqlc.arg(business_id)::uuid AND project_id = $1 FOR UPDATE;

-- name: UpdateProject :one
UPDATE projects SET customer_id=$2, code=$3, name=$4, currency=$5, contract_value=$6, status=$7, start_date=$8, end_date=$9, notes=$10, active=$11 WHERE business_id=sqlc.arg(business_id)::uuid AND project_id=$1 AND EXISTS (SELECT 1 FROM customers c WHERE c.business_id=sqlc.arg(business_id)::uuid AND c.customer_id=$2) RETURNING *;

-- name: CountProjectReferences :one
SELECT ((SELECT count(*) FROM invoices WHERE invoices.business_id=sqlc.arg(business_id)::uuid AND invoices.project_id=sqlc.arg(project_id)) + (SELECT count(*) FROM bills WHERE bills.business_id=sqlc.arg(business_id)::uuid AND bills.project_id=sqlc.arg(project_id)) + (SELECT count(*) FROM quotations WHERE quotations.business_id=sqlc.arg(business_id)::uuid AND quotations.project_id=sqlc.arg(project_id)))::bigint AS reference_count;

-- name: DeleteProject :exec
DELETE FROM projects WHERE business_id=sqlc.arg(business_id)::uuid AND project_id=$1;

-- name: ProjectReportHasDamagedReferences :one
WITH scope AS (
    SELECT sqlc.arg(business_id)::uuid AS business_id, sqlc.arg(project_id)::uuid AS project_id
)
SELECT (EXISTS (
    SELECT 1
    FROM scope s
    JOIN projects p ON p.project_id = s.project_id AND p.business_id = s.business_id
    LEFT JOIN customers c ON c.customer_id = p.customer_id AND c.business_id = p.business_id
    WHERE p.customer_id IS NOT NULL AND c.customer_id IS NULL
) OR EXISTS (
    SELECT 1
    FROM scope s
    JOIN invoices i ON i.project_id = s.project_id AND i.business_id = s.business_id
    LEFT JOIN customers c ON c.customer_id = i.customer_id AND c.business_id = i.business_id
    LEFT JOIN quotations q ON q.quotation_id = i.source_quote_id AND q.business_id = i.business_id
    LEFT JOIN invoice_line_items l ON l.invoice_id = i.invoice_id AND l.business_id = i.business_id
    LEFT JOIN items it ON it.item_id = l.item_id AND it.business_id = l.business_id
    WHERE CASE WHEN sqlc.arg(invoice_list)::boolean THEN NOT i.is_template
               ELSE i.status IN ('OPEN','SENT','PARTIAL','PAID') END
      AND ((i.customer_id IS NOT NULL AND c.customer_id IS NULL)
        OR (i.source_quote_id IS NOT NULL AND q.quotation_id IS NULL)
        OR (l.item_id IS NOT NULL AND it.item_id IS NULL))
) OR EXISTS (
    SELECT 1
    FROM scope s
    JOIN bills b ON b.project_id = s.project_id AND b.business_id = s.business_id
    LEFT JOIN vendors v ON v.vendor_id = b.vendor_id AND v.business_id = b.business_id
    LEFT JOIN bill_line_items l ON l.bill_id = b.bill_id AND l.business_id = b.business_id
    LEFT JOIN chart_of_accounts a ON a.account_id = l.expense_account_id AND a.business_id = l.business_id
    LEFT JOIN items it ON it.item_id = l.item_id AND it.business_id = l.business_id
    WHERE NOT sqlc.arg(invoice_list)::boolean AND b.status IN ('OPEN','PARTIAL','PAID')
      AND ((b.vendor_id IS NOT NULL AND v.vendor_id IS NULL)
        OR (l.expense_account_id IS NOT NULL AND a.account_id IS NULL)
        OR (l.item_id IS NOT NULL AND it.item_id IS NULL))
))::boolean AS damaged;
