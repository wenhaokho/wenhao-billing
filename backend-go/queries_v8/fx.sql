-- name: GetFXRate :one
SELECT rate FROM fx_rates WHERE business_id=sqlc.arg(business_id)::uuid AND from_currency=sqlc.arg(from_currency) AND to_currency=sqlc.arg(to_currency) AND as_of_date<=sqlc.arg(as_of_date) ORDER BY as_of_date DESC LIMIT 1;
-- name: ListFXRates :many
SELECT * FROM fx_rates WHERE business_id=sqlc.arg(business_id)::uuid AND (sqlc.narg(from_currency)::text IS NULL OR from_currency=sqlc.narg(from_currency)) AND (sqlc.narg(to_currency)::text IS NULL OR to_currency=sqlc.narg(to_currency)) ORDER BY as_of_date DESC, rate_id DESC;
-- name: LatestFXRate :one
SELECT * FROM fx_rates WHERE business_id=sqlc.arg(business_id)::uuid AND from_currency=sqlc.arg(from_currency) AND to_currency=sqlc.arg(to_currency) AND (sqlc.narg(on_date)::date IS NULL OR as_of_date<=sqlc.narg(on_date)) ORDER BY as_of_date DESC, rate_id DESC LIMIT 1;
-- name: CreateFXRate :one
INSERT INTO fx_rates (business_id, from_currency, to_currency, rate, as_of_date, source) VALUES (sqlc.arg(business_id)::uuid,sqlc.arg(from_currency),sqlc.arg(to_currency),sqlc.arg(rate),sqlc.arg(as_of_date),sqlc.arg(source)) RETURNING *;
-- name: DeleteFXRate :one
DELETE FROM fx_rates WHERE business_id=sqlc.arg(business_id)::uuid AND rate_id=sqlc.arg(rate_id) RETURNING rate_id;
-- name: LockFXSync :exec
SELECT pg_advisory_xact_lock(hashtextextended('fx-sync:' || sqlc.arg(business_id)::uuid::text, 0));
-- name: FindFXSyncRate :one
SELECT * FROM fx_rates WHERE business_id=sqlc.arg(business_id)::uuid AND from_currency=sqlc.arg(from_currency) AND to_currency=sqlc.arg(to_currency) AND as_of_date=sqlc.arg(as_of_date) FOR UPDATE;
-- name: UpdateFXSyncRate :exec
UPDATE fx_rates SET rate=sqlc.arg(rate), source='FRANKFURTER' WHERE business_id=sqlc.arg(business_id)::uuid AND rate_id=sqlc.arg(rate_id);
