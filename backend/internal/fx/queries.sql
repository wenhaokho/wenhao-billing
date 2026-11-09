-- name: GetRate :one
-- The newest rate with as_of_date on or before $3 (Python's services/fx.py
-- get_rate); as_of_date is unique per pair, so there is no tie to break.
SELECT rate FROM fx_rates
 WHERE from_currency = $1 AND to_currency = $2 AND as_of_date <= $3
 ORDER BY as_of_date DESC
 LIMIT 1;

-- name: ListRates :many
-- A null filter matches every currency (Python's list_fx_rates).
SELECT * FROM fx_rates
 WHERE (sqlc.narg(from_currency)::text IS NULL OR from_currency = sqlc.narg(from_currency)::text)
   AND (sqlc.narg(to_currency)::text IS NULL OR to_currency = sqlc.narg(to_currency)::text)
 ORDER BY as_of_date DESC, rate_id DESC;

-- name: CreateRate :one
INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES (sqlc.arg(from_currency), sqlc.arg(to_currency), sqlc.arg(rate), sqlc.arg(as_of_date), sqlc.arg(source))
RETURNING *;

-- name: LatestRate :one
-- The newest from->to row, on or before on_date when it is not null
-- (Python's latest_fx_rate).
SELECT * FROM fx_rates
 WHERE from_currency = sqlc.arg(from_currency) AND to_currency = sqlc.arg(to_currency)
   AND (sqlc.narg(on_date)::date IS NULL OR as_of_date <= sqlc.narg(on_date)::date)
 ORDER BY as_of_date DESC, rate_id DESC
 LIMIT 1;

-- name: DeleteRate :execrows
DELETE FROM fx_rates WHERE rate_id = $1;

-- name: UpsertSyncedRate :many
-- A provider rate: inserted, or written over the row of the same pair and
-- date when its rate or source differs. inserted is true for a new row and
-- false for an updated one; an unchanged row returns nothing. :many, not
-- :one, so "nothing" is an empty result rather than an error.
INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES (sqlc.arg(from_currency), sqlc.arg(to_currency), sqlc.arg(rate), sqlc.arg(as_of_date), 'FRANKFURTER')
ON CONFLICT ON CONSTRAINT uq_fx_rates_triple DO UPDATE
   SET rate = EXCLUDED.rate, source = 'FRANKFURTER'
 WHERE fx_rates.rate <> EXCLUDED.rate OR fx_rates.source <> 'FRANKFURTER'
RETURNING (xmax = 0) AS inserted;
