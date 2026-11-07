-- name: ListVendors :many
SELECT * FROM vendors WHERE business_id = sqlc.arg(business_id)::uuid AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY name ASC;

-- name: GetVendor :one
SELECT * FROM vendors WHERE business_id = sqlc.arg(business_id)::uuid AND vendor_id = sqlc.arg(vendor_id);

-- name: CreateVendor :one
INSERT INTO vendors (business_id, vendor_id, name, contact_email, contact_name, default_currency, payment_terms_days, tax_id, notes, active)
VALUES (sqlc.arg(business_id)::uuid, gen_random_uuid(), sqlc.arg(name), sqlc.arg(contact_email), sqlc.arg(contact_name), sqlc.arg(default_currency), sqlc.arg(payment_terms_days), sqlc.arg(tax_id), sqlc.arg(notes), sqlc.arg(active)) RETURNING *;

-- name: DeactivateVendor :one
UPDATE vendors SET active = false WHERE business_id = sqlc.arg(business_id)::uuid AND vendor_id = sqlc.arg(vendor_id) RETURNING *;

-- name: LockVendor :one
SELECT * FROM vendors WHERE business_id = sqlc.arg(business_id)::uuid AND vendor_id = sqlc.arg(vendor_id) FOR UPDATE;

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
 WHERE v.business_id = sqlc.arg(business_id)::uuid AND v.vendor_id = sqlc.arg(vendor_id) RETURNING *;
