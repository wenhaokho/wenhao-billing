-- name: ListAccounts :many
SELECT * FROM chart_of_accounts WHERE business_id = sqlc.arg(business_id)::uuid AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY code ASC;

-- name: GetAccount :one
SELECT * FROM chart_of_accounts WHERE business_id = sqlc.arg(business_id)::uuid AND account_id = sqlc.arg(account_id);

-- name: CreateAccount :one
INSERT INTO chart_of_accounts (business_id, code, name, type, active) VALUES (sqlc.arg(business_id)::uuid, sqlc.arg(code), sqlc.arg(name), sqlc.arg(type), sqlc.arg(active)) RETURNING *;

-- name: LockAccount :one
SELECT * FROM chart_of_accounts WHERE business_id = sqlc.arg(business_id)::uuid AND account_id = sqlc.arg(account_id) FOR UPDATE;

-- name: UpdateAccount :one
UPDATE chart_of_accounts SET code = sqlc.arg(code), name = sqlc.arg(name), type = sqlc.arg(type), active = sqlc.arg(active) WHERE business_id = sqlc.arg(business_id)::uuid AND account_id = sqlc.arg(account_id) RETURNING *;
