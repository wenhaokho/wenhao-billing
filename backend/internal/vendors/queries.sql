-- name: ListVendors :many
-- A null active lists every vendor.
SELECT * FROM vendors
WHERE sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)::boolean
ORDER BY name, vendor_id;

-- name: GetVendor :one
SELECT * FROM vendors WHERE vendor_id = $1;

-- name: GetVendorForUpdate :one
-- A PATCH reads the row locked, merges the fields sent, and writes every
-- column back with UpdateVendor.
SELECT * FROM vendors WHERE vendor_id = $1 FOR UPDATE;

-- name: CreateVendor :one
INSERT INTO vendors (vendor_id, name, contact_email, contact_name, default_currency, payment_terms_days, tax_id, notes, active)
VALUES (sqlc.arg(vendor_id), sqlc.arg(name), sqlc.narg(contact_email), sqlc.narg(contact_name), sqlc.arg(default_currency),
        sqlc.arg(payment_terms_days), sqlc.narg(tax_id), sqlc.narg(notes), sqlc.arg(active))
RETURNING *;

-- name: UpdateVendor :one
UPDATE vendors
SET name = sqlc.arg(name), contact_email = sqlc.narg(contact_email), contact_name = sqlc.narg(contact_name),
    default_currency = sqlc.arg(default_currency), payment_terms_days = sqlc.arg(payment_terms_days),
    tax_id = sqlc.narg(tax_id), notes = sqlc.narg(notes), active = sqlc.arg(active)
WHERE vendor_id = sqlc.arg(vendor_id)
RETURNING *;

-- name: DeactivateVendor :one
-- Vendors are never deleted: bills reference them.
UPDATE vendors SET active = false WHERE vendor_id = $1 RETURNING *;
