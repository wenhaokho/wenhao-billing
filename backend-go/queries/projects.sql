-- name: ListProjects :many
SELECT * FROM projects WHERE (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)) AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) AND (sqlc.arg(all_status)::boolean OR status = ANY(sqlc.arg(statuses)::text[])) ORDER BY created_at DESC;

-- name: GetProject :one
SELECT * FROM projects WHERE project_id = $1;

-- name: ProjectInvoices :many
SELECT * FROM invoices WHERE project_id = $1 AND is_template = false ORDER BY created_at DESC;

-- name: ProjectInvoiceLines :many
SELECT l.* FROM invoice_line_items l JOIN invoices i ON i.invoice_id = l.invoice_id WHERE i.project_id = $1 AND i.is_template = false ORDER BY l.position;

-- name: ProjectInvoiceStats :one
SELECT COALESCE(sum(amount), 0)::numeric AS invoiced, COALESCE(sum(balance_due), 0)::numeric AS outstanding, count(invoice_id)::bigint AS invoice_count FROM invoices WHERE project_id = $1 AND status IN ('OPEN','SENT','PARTIAL','PAID');

-- name: ProjectBillStats :one
SELECT COALESCE(sum(amount), 0)::numeric AS bill_total FROM bills WHERE project_id = $1 AND status IN ('OPEN','PARTIAL','PAID');

-- name: CreateProject :one
INSERT INTO projects (project_id,customer_id,code,name,currency,contract_value,status,start_date,end_date,notes,active)
VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,$9,true) RETURNING *;

-- name: LockProject :one
SELECT * FROM projects WHERE project_id = $1 FOR UPDATE;

-- name: UpdateProject :one
UPDATE projects SET customer_id=$2, code=$3, name=$4, currency=$5, contract_value=$6, status=$7, start_date=$8, end_date=$9, notes=$10, active=$11 WHERE project_id=$1 RETURNING *;

-- name: CountProjectReferences :one
SELECT ((SELECT count(*) FROM invoices WHERE invoices.project_id=sqlc.arg(project_id)) + (SELECT count(*) FROM bills WHERE bills.project_id=sqlc.arg(project_id)))::bigint AS reference_count;

-- name: DeleteProject :exec
DELETE FROM projects WHERE project_id=$1;
