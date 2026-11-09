-- name: StampPaid :exec
-- Python's ensure_hosting_restored stamp: every hosting item on a paid
-- invoice's lines (balance_due <= 0) takes hosting_last_paid_at, whether or
-- not it is restored.
UPDATE items SET hosting_last_paid_at = sqlc.arg(paid_at)::timestamp
WHERE is_hosting
  AND item_id IN (SELECT li.item_id FROM invoice_line_items li WHERE li.invoice_id = sqlc.arg(invoice_id))
  AND EXISTS (SELECT 1 FROM invoices i WHERE i.invoice_id = sqlc.arg(invoice_id) AND i.balance_due <= 0);

-- name: PaidInvoiceHostingItems :many
-- The hosting items on a paid invoice's lines, in line order; none when the
-- invoice still has a balance (Python's balance_due > 0 early return).
SELECT it.item_id
FROM invoice_line_items li
JOIN items it ON it.item_id = li.item_id
JOIN invoices i ON i.invoice_id = li.invoice_id
WHERE li.invoice_id = sqlc.arg(invoice_id) AND it.is_hosting AND i.balance_due <= 0
GROUP BY it.item_id
ORDER BY min(li.position), it.item_id;

-- name: EnforcementItems :many
-- Python's run_hosting_enforcement scope, oldest first.
SELECT item_id FROM items
WHERE is_hosting AND hosting_suspension_enabled
  AND hosting_status IN ('ACTIVE', 'SUSPEND_PENDING', 'SUSPENDED')
ORDER BY created_at, item_id;

-- name: LockItem :one
SELECT item_id, is_hosting, hosting_grace_days, hosting_suspension_enabled, hosting_status
FROM items WHERE item_id = $1 FOR UPDATE;

-- name: LockTarget :one
SELECT * FROM cloudflare_targets WHERE item_id = $1 FOR UPDATE;

-- name: OpenInvoicesForItem :many
-- Python's _open_invoices_for_item_stmt, ordered as resolve_overdue_invoice
-- reads it. An invoice with several lines for the item appears once per line.
SELECT i.invoice_id, i.due_date
FROM invoices i
JOIN invoice_line_items li ON li.invoice_id = i.invoice_id
WHERE li.item_id = sqlc.arg(item_id)::uuid
  AND NOT i.is_template
  AND i.status IN ('OPEN', 'SENT', 'PARTIAL')
  AND i.balance_due > 0
ORDER BY i.due_date ASC NULLS LAST, i.created_at ASC;

-- name: SetItemStatus :exec
-- items has no updated_at; hosting_last_action_at stays unwritten, as in
-- Python.
UPDATE items SET hosting_status = sqlc.arg(hosting_status)::text WHERE item_id = sqlc.arg(item_id);

-- name: SetTargetResult :exec
-- Every target write stamps last_action_at and updated_at itself (the ORM's
-- onupdate is Python's, not the table's).
UPDATE cloudflare_targets
SET provider_status = sqlc.arg(provider_status), last_error = sqlc.narg(last_error),
    last_action_at = sqlc.arg(now)::timestamp, updated_at = sqlc.arg(now)::timestamp
WHERE target_id = sqlc.arg(target_id);
