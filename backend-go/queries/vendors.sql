-- name: ListVendors :many
SELECT * FROM vendors WHERE (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY name ASC;

-- name: GetVendor :one
SELECT * FROM vendors WHERE vendor_id = $1;

-- name: CreateVendor :one
INSERT INTO vendors (vendor_id, name, contact_email, contact_name, default_currency, payment_terms_days, tax_id, notes, active)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: DeactivateVendor :one
UPDATE vendors SET active = false WHERE vendor_id = $1 RETURNING *;

-- name: LockVendor :one
SELECT * FROM vendors WHERE vendor_id = $1 FOR UPDATE;

-- name: UpdateVendor :one
UPDATE vendors AS v SET
 name = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).name,
 contact_email = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).contact_email,
 contact_name = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).contact_name,
 default_currency = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).default_currency,
 payment_terms_days = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).payment_terms_days,
 tax_id = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).tax_id,
 notes = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).notes,
 active = (jsonb_populate_record(v, sqlc.arg(data)::jsonb)).active
 WHERE v.vendor_id = sqlc.arg(vendor_id) RETURNING *;
