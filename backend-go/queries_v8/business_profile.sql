-- name: GetBusinessProfile :one
SELECT * FROM business_profile WHERE business_id = sqlc.arg(business_id)::uuid;

-- name: GetBusinessProfileDefaultTime :one
SELECT CURRENT_TIMESTAMP::timestamp AS updated_at;

-- name: ListBusinessPaymentAccounts :many
SELECT * FROM payment_account WHERE business_id = sqlc.arg(business_id)::uuid ORDER BY currency;

-- name: EnsureBusinessProfile :exec
INSERT INTO business_profile (business_id) VALUES (sqlc.arg(business_id)::uuid) ON CONFLICT (business_id) DO NOTHING;

-- name: ApplyBusinessProfileUpdate :one
UPDATE business_profile AS p SET
 name = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).name,
 address = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).address,
 contact_email = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).contact_email,
 contact_phone = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).contact_phone,
 invoice_title = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).invoice_title,
 invoice_summary = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).invoice_summary,
 logo_url = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).logo_url,
 default_notes = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).default_notes,
 npwp = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).npwp,
 npwp_name = (jsonb_populate_record(p, sqlc.arg(data)::jsonb)).npwp_name,
 updated_at = CASE WHEN (to_jsonb(p) - 'updated_at') IS DISTINCT FROM (to_jsonb(jsonb_populate_record(p, sqlc.arg(data)::jsonb - 'business_id')) - 'updated_at') THEN CURRENT_TIMESTAMP ELSE p.updated_at END
WHERE p.business_id = sqlc.arg(business_id)::uuid RETURNING *;

-- name: LockBusinessProfile :one
SELECT id FROM business_profile WHERE business_id = sqlc.arg(business_id)::uuid FOR UPDATE;

-- name: DeleteBusinessPaymentAccounts :exec
DELETE FROM payment_account WHERE business_id = sqlc.arg(business_id)::uuid;

-- name: InsertBusinessPaymentAccount :exec
INSERT INTO payment_account (business_id, business_profile_id, currency, instructions, is_default)
SELECT p.business_id, p.id, sqlc.arg(currency), sqlc.arg(instructions), sqlc.arg(is_default)
FROM business_profile p WHERE p.business_id = sqlc.arg(business_id)::uuid;
