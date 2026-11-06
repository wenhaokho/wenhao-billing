-- name: ListItems :many
SELECT * FROM items WHERE (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) AND (sqlc.narg(item_type)::text IS NULL OR item_type = sqlc.narg(item_type)) AND (sqlc.narg(is_hosting)::boolean IS NULL OR is_hosting = sqlc.narg(is_hosting)) ORDER BY name ASC;

-- name: GetItem :one
SELECT * FROM items WHERE item_id = $1;

-- name: LockItem :one
SELECT * FROM items WHERE item_id = $1 FOR UPDATE;

-- name: InsertItem :one
INSERT INTO items (item_id, sku, name, item_type, description, default_currency, default_unit_price, default_purchase_price, revenue_account_id, expense_account_id, is_sold, is_purchased, active, is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING *;

-- name: UpdateItem :one
UPDATE items SET sku=$2, name=$3, item_type=$4, description=$5, default_currency=$6, default_unit_price=$7, default_purchase_price=$8, revenue_account_id=$9, expense_account_id=$10, is_sold=$11, is_purchased=$12, active=$13, is_hosting=$14, hosting_domain=$15, hosting_grace_days=$16, hosting_suspension_enabled=$17, hosting_status=$18 WHERE item_id=$1 RETURNING *;

-- name: GetItemTarget :one
SELECT * FROM cloudflare_targets WHERE item_id=$1;

-- name: UpsertItemTarget :one
INSERT INTO cloudflare_targets (target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (item_id) DO UPDATE SET zone_id=EXCLUDED.zone_id,record_id=EXCLUDED.record_id,record_name=EXCLUDED.record_name,record_type=EXCLUDED.record_type,live_content=EXCLUDED.live_content,maintenance_content=EXCLUDED.maintenance_content,proxied=EXCLUDED.proxied,updated_at=CURRENT_TIMESTAMP RETURNING *;

-- name: DeactivateItem :one
UPDATE items SET active=false WHERE item_id=$1 RETURNING *;
