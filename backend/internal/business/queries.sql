-- name: GetProfile :one
SELECT * FROM business_profile WHERE id = 1;

-- name: EnsureProfile :exec
-- The singleton row 1 (ck_business_profile_singleton); the migrations seed it,
-- but a write creates it if it was deleted, as Python's _get_or_create does.
INSERT INTO business_profile (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- name: GetProfileForUpdate :one
-- Every write holds row 1 locked: a PUT merges the fields sent, and a payment
-- account replace runs one at a time.
SELECT * FROM business_profile WHERE id = 1 FOR UPDATE;

-- name: UpdateProfile :one
UPDATE business_profile
SET name = sqlc.narg(name), address = sqlc.narg(address), contact_email = sqlc.narg(contact_email),
    contact_phone = sqlc.narg(contact_phone), invoice_title = sqlc.narg(invoice_title),
    invoice_summary = sqlc.narg(invoice_summary), logo_url = sqlc.narg(logo_url),
    default_notes = sqlc.narg(default_notes), npwp = sqlc.narg(npwp), npwp_name = sqlc.narg(npwp_name),
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1
RETURNING *;

-- name: ListPaymentAccounts :many
SELECT * FROM payment_account WHERE business_profile_id = 1 ORDER BY currency, id;

-- name: DeletePaymentAccounts :exec
DELETE FROM payment_account WHERE business_profile_id = 1;

-- name: CreatePaymentAccount :exec
INSERT INTO payment_account (business_profile_id, currency, instructions, is_default)
VALUES (1, sqlc.arg(currency), sqlc.arg(instructions), sqlc.arg(is_default));
