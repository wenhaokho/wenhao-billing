-- name: UserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: UserByID :one
SELECT * FROM users WHERE user_id = $1;

-- name: SetPassword :one
-- SetPassword replaces the hash, signs every session out and voids any reset
-- token.
UPDATE users
SET password_hash = sqlc.arg(password_hash), session_epoch = session_epoch + 1,
    password_reset_token = NULL, password_reset_expires = NULL
WHERE user_id = sqlc.arg(user_id)
RETURNING *;

-- name: SetResetToken :exec
-- The token column holds the SHA-256 hex of the emailed token; expires is
-- naive UTC.
UPDATE users SET password_reset_token = sqlc.arg(token_hash)::varchar, password_reset_expires = sqlc.arg(expires)::timestamp
WHERE user_id = sqlc.arg(user_id);

-- name: UserByResetTokenHash :one
SELECT user_id, password_reset_expires FROM users WHERE password_reset_token = sqlc.arg(token_hash)::varchar FOR UPDATE;

-- name: ClearResetToken :exec
UPDATE users SET password_reset_token = NULL, password_reset_expires = NULL WHERE user_id = $1;

-- name: CountPairFailures :one
SELECT count(*) FROM login_attempts
WHERE email = sqlc.arg(email) AND client_addr = sqlc.arg(client_addr) AND failed_at > sqlc.arg(since);

-- name: CountAddressFailures :one
SELECT count(*) FROM login_attempts
WHERE client_addr = sqlc.arg(client_addr) AND failed_at > sqlc.arg(since);

-- name: OldestPairFailure :one
-- Called only when the count is positive, so min is never null.
SELECT min(failed_at)::timestamptz FROM login_attempts
WHERE email = sqlc.arg(email) AND client_addr = sqlc.arg(client_addr) AND failed_at > sqlc.arg(since);

-- name: OldestAddressFailure :one
SELECT min(failed_at)::timestamptz FROM login_attempts
WHERE client_addr = sqlc.arg(client_addr) AND failed_at > sqlc.arg(since);

-- name: InsertFailure :exec
INSERT INTO login_attempts (email, client_addr, failed_at)
VALUES (sqlc.arg(email), sqlc.arg(client_addr), sqlc.arg(failed_at));

-- name: PruneFailures :exec
DELETE FROM login_attempts WHERE failed_at < sqlc.arg(before);

-- name: ClearPairFailures :exec
DELETE FROM login_attempts WHERE email = sqlc.arg(email) AND client_addr = sqlc.arg(client_addr);
