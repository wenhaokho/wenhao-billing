-- name: ListProjects :many
-- GET /projects (projects.py:48-66): each filter applies when given (a null
-- or empty statuses list is no filter); newest first, the id breaking ties.
SELECT * FROM projects
WHERE (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)::boolean)
ORDER BY created_at DESC, project_id DESC;

-- name: GetProject :one
SELECT * FROM projects WHERE project_id = $1;

-- name: GetProjectForUpdate :one
-- A PATCH or a delete reads the row locked: a concurrent invoice, bill or
-- quote insert takes a share lock on it through its foreign key, so the
-- delete's count cannot miss one.
SELECT * FROM projects WHERE project_id = $1 FOR UPDATE;

-- name: CreateProject :one
INSERT INTO projects (project_id, customer_id, code, name, currency, contract_value, status, start_date, end_date, notes)
VALUES (sqlc.arg(project_id), sqlc.arg(customer_id), sqlc.arg(code), sqlc.arg(name), sqlc.arg(currency),
        sqlc.narg(contract_value), sqlc.arg(status), sqlc.narg(start_date), sqlc.narg(end_date), sqlc.narg(notes))
RETURNING *;

-- name: UpdateProject :one
-- Every column is written back from the row read with GetProjectForUpdate.
UPDATE projects
SET customer_id = sqlc.arg(customer_id), code = sqlc.arg(code), name = sqlc.arg(name), currency = sqlc.arg(currency),
    contract_value = sqlc.narg(contract_value), status = sqlc.arg(status), start_date = sqlc.narg(start_date),
    end_date = sqlc.narg(end_date), notes = sqlc.narg(notes), active = sqlc.arg(active)
WHERE project_id = sqlc.arg(project_id)
RETURNING *;

-- name: CountProjectReferences :one
-- The rows that point at the project, of any status (templates and void
-- documents included): invoices, bills and quotations (Python missed the
-- quotations and failed on the foreign key).
SELECT ((SELECT count(*) FROM invoices i WHERE i.project_id = sqlc.arg(project_id)::uuid)
      + (SELECT count(*) FROM bills b WHERE b.project_id = sqlc.arg(project_id)::uuid)
      + (SELECT count(*) FROM quotations q WHERE q.project_id = sqlc.arg(project_id)::uuid))::bigint AS refs;

-- name: DeleteProject :exec
DELETE FROM projects WHERE project_id = $1;

-- name: ProjectInvoiceTotals :one
-- GET /projects/{id}/summary (projects.py:127-136): the issued invoices only.
-- No currency filter and no template filter (templates are DRAFT).
SELECT COALESCE(SUM(amount), 0)::numeric(19,4) AS invoiced,
       COALESCE(SUM(balance_due), 0)::numeric(19,4) AS outstanding,
       count(invoice_id)::bigint AS invoice_count
FROM invoices
WHERE project_id = sqlc.arg(project_id)::uuid AND status IN ('OPEN', 'SENT', 'PARTIAL', 'PAID');

-- name: ProjectBillTotal :one
-- projects.py:142-147.
SELECT COALESCE(SUM(amount), 0)::numeric(19,4) AS bill_total
FROM bills
WHERE project_id = sqlc.arg(project_id)::uuid AND status IN ('OPEN', 'PARTIAL', 'PAID');
