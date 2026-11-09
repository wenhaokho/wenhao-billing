-- name: ListAccounts :many
-- A null active lists every account.
SELECT * FROM chart_of_accounts
WHERE sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)::boolean
ORDER BY code, account_id;

-- name: GetAccount :one
SELECT * FROM chart_of_accounts WHERE account_id = $1;

-- name: GetAccountForUpdate :one
-- A PATCH reads the row locked, merges the fields sent, and writes every
-- column back with UpdateAccount.
SELECT * FROM chart_of_accounts WHERE account_id = $1 FOR UPDATE;

-- name: CreateAccount :one
INSERT INTO chart_of_accounts (code, name, type, active)
VALUES (sqlc.arg(code), sqlc.arg(name), sqlc.arg(type), sqlc.arg(active))
RETURNING *;

-- name: UpdateAccount :one
UPDATE chart_of_accounts
SET code = sqlc.arg(code), name = sqlc.arg(name), type = sqlc.arg(type), active = sqlc.arg(active)
WHERE account_id = sqlc.arg(account_id)
RETURNING *;

-- name: DeactivateAccount :one
-- Accounts are never deleted: journal lines and items reference them.
UPDATE chart_of_accounts SET active = false WHERE account_id = $1 RETURNING *;
