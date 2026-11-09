-- name: ListItems :many
-- A null active or is_hosting, and an empty item_type, do not filter.
SELECT * FROM items
WHERE (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)::boolean)
  AND (sqlc.arg(item_type)::text = '' OR item_type = sqlc.arg(item_type)::text)
  AND (sqlc.narg(is_hosting)::boolean IS NULL OR is_hosting = sqlc.narg(is_hosting)::boolean)
ORDER BY name, item_id;

-- name: GetItem :one
SELECT * FROM items WHERE item_id = $1;

-- name: GetItemForUpdate :one
-- A PATCH reads the row locked, merges the fields sent, and writes every
-- column it may change back with UpdateItem.
SELECT * FROM items WHERE item_id = $1 FOR UPDATE;

-- name: CreateItem :one
INSERT INTO items (item_id, sku, name, item_type, description, default_currency, default_unit_price,
                   default_purchase_price, revenue_account_id, expense_account_id, is_sold, is_purchased, active,
                   is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status)
VALUES (sqlc.arg(item_id), sqlc.narg(sku), sqlc.arg(name), sqlc.arg(item_type), sqlc.narg(description),
        sqlc.arg(default_currency), sqlc.narg(default_unit_price), sqlc.narg(default_purchase_price),
        sqlc.narg(revenue_account_id), sqlc.narg(expense_account_id), sqlc.arg(is_sold), sqlc.arg(is_purchased),
        sqlc.arg(active), sqlc.arg(is_hosting), sqlc.narg(hosting_domain), sqlc.narg(hosting_grace_days),
        sqlc.narg(hosting_suspension_enabled), sqlc.narg(hosting_status))
RETURNING *;

-- name: UpdateItem :one
-- The hosting_last_* columns belong to hosting enforcement, not to a PATCH.
UPDATE items
SET sku = sqlc.narg(sku), name = sqlc.arg(name), item_type = sqlc.arg(item_type),
    description = sqlc.narg(description), default_currency = sqlc.arg(default_currency),
    default_unit_price = sqlc.narg(default_unit_price), default_purchase_price = sqlc.narg(default_purchase_price),
    revenue_account_id = sqlc.narg(revenue_account_id), expense_account_id = sqlc.narg(expense_account_id),
    is_sold = sqlc.arg(is_sold), is_purchased = sqlc.arg(is_purchased), active = sqlc.arg(active),
    is_hosting = sqlc.arg(is_hosting), hosting_domain = sqlc.narg(hosting_domain),
    hosting_grace_days = sqlc.narg(hosting_grace_days),
    hosting_suspension_enabled = sqlc.narg(hosting_suspension_enabled), hosting_status = sqlc.narg(hosting_status)
WHERE item_id = sqlc.arg(item_id)
RETURNING *;

-- name: DeactivateItem :one
-- Items are never deleted: invoice and bill lines reference them.
UPDATE items SET active = false WHERE item_id = $1 RETURNING *;

-- name: GetTarget :one
SELECT * FROM cloudflare_targets WHERE item_id = $1;

-- name: ListTargets :many
SELECT * FROM cloudflare_targets WHERE item_id = ANY(sqlc.arg(item_ids)::uuid[]);

-- name: UpsertTarget :one
-- An item has at most one target (cloudflare_targets_item_id_key): a second
-- write replaces the fields sent and keeps target_id, provider_status and
-- the last action. A new target takes provider_status's default (ACTIVE).
-- A null proxied (a PATCH that did not send it) keeps the stored value, or
-- is true for a new target.
INSERT INTO cloudflare_targets (target_id, item_id, zone_id, record_id, record_name, record_type, live_content,
                                maintenance_content, proxied)
VALUES (sqlc.arg(target_id), sqlc.arg(item_id), sqlc.arg(zone_id), sqlc.arg(record_id), sqlc.arg(record_name),
        sqlc.arg(record_type), sqlc.arg(live_content), sqlc.arg(maintenance_content),
        COALESCE(sqlc.narg(proxied)::boolean, true))
ON CONFLICT (item_id) DO UPDATE
SET zone_id = EXCLUDED.zone_id, record_id = EXCLUDED.record_id, record_name = EXCLUDED.record_name,
    record_type = EXCLUDED.record_type, live_content = EXCLUDED.live_content,
    maintenance_content = EXCLUDED.maintenance_content,
    proxied = COALESCE(sqlc.narg(proxied)::boolean, cloudflare_targets.proxied),
    updated_at = CURRENT_TIMESTAMP
RETURNING *;
