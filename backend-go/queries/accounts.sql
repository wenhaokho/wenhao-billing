-- name: ListAccounts :many
SELECT * FROM chart_of_accounts WHERE (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY code ASC;

-- name: GetAccount :one
SELECT * FROM chart_of_accounts WHERE account_id = $1;

-- name: CreateAccount :one
INSERT INTO chart_of_accounts (code, name, type, active) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: LockAccount :one
SELECT * FROM chart_of_accounts WHERE account_id = $1 FOR UPDATE;

-- name: UpdateAccount :one
UPDATE chart_of_accounts SET code = $2, name = $3, type = $4, active = $5 WHERE account_id = $1 RETURNING *;
