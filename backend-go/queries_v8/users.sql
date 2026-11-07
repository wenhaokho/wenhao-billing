-- name: FindUserByEmail :one
SELECT user_id, email, password_hash, role, created_at, display_name, password_reset_token, password_reset_expires FROM users WHERE business_id=sqlc.arg(business_id)::uuid AND email = $1 FOR UPDATE;

-- name: SetUserPassword :one
UPDATE users SET password_hash = $2, password_reset_token = NULL, password_reset_expires = NULL WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1 RETURNING user_id;

-- name: GetSessionUserByEmail :one
SELECT user_id, email, password_hash, role, created_at, display_name FROM users WHERE business_id=sqlc.arg(business_id)::uuid AND email = $1;

-- name: GetSessionUserByID :one
SELECT user_id, email, password_hash, role, created_at, display_name FROM users WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1;

-- name: LockSessionUserByID :one
SELECT user_id, email, password_hash, role, created_at, display_name FROM users WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1 FOR UPDATE;

-- name: ListUsers :many
SELECT user_id, email, role, display_name, created_at FROM users WHERE business_id=sqlc.arg(business_id)::uuid ORDER BY email ASC;

-- name: UpdateUserProfile :one
UPDATE users SET email = $2, display_name = $3 WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1 RETURNING user_id, email, role, display_name, created_at;

-- name: CreateUser :one
INSERT INTO users (business_id, user_id, email, password_hash, role, display_name) VALUES (sqlc.arg(business_id)::uuid, $1, $2, $3, $4, $5) RETURNING user_id, email, role, display_name, created_at;

-- name: LockUserByResetToken :one
SELECT user_id, email, role, created_at, display_name, password_reset_expires FROM users WHERE business_id=sqlc.arg(business_id)::uuid AND password_reset_token = $1 FOR UPDATE;

-- name: SetResetToken :exec
UPDATE users SET password_reset_token = $2, password_reset_expires = $3 WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1;

-- name: ClearResetToken :exec
UPDATE users SET password_reset_token = NULL, password_reset_expires = NULL WHERE business_id=sqlc.arg(business_id)::uuid AND user_id = $1;
