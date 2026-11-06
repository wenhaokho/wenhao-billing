-- name: GetFXRate :one
SELECT rate FROM fx_rates
WHERE from_currency = $1 AND to_currency = $2 AND as_of_date <= $3
ORDER BY as_of_date DESC LIMIT 1;

-- name: ListFXRates :many
SELECT * FROM fx_rates
WHERE (sqlc.narg(from_currency)::text IS NULL OR from_currency = sqlc.narg(from_currency))
  AND (sqlc.narg(to_currency)::text IS NULL OR to_currency = sqlc.narg(to_currency))
ORDER BY as_of_date DESC, rate_id DESC;

-- name: LatestFXRate :one
SELECT * FROM fx_rates
WHERE from_currency = sqlc.arg(from_currency) AND to_currency = sqlc.arg(to_currency)
  AND (sqlc.narg(on_date)::date IS NULL OR as_of_date <= sqlc.narg(on_date))
ORDER BY as_of_date DESC, rate_id DESC LIMIT 1;

-- name: CreateFXRate :one
INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: DeleteFXRate :one
DELETE FROM fx_rates WHERE rate_id = $1 RETURNING rate_id;

-- name: LockFXSync :exec
SELECT pg_advisory_xact_lock(76920174933512);

-- name: FindFXSyncRate :one
SELECT * FROM fx_rates WHERE from_currency = $1 AND to_currency = $2 AND as_of_date = $3 FOR UPDATE;

-- name: UpdateFXSyncRate :exec
UPDATE fx_rates SET rate = $2, source = 'FRANKFURTER' WHERE rate_id = $1;
