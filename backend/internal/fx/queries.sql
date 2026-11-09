-- name: GetRate :one
-- The newest rate with as_of_date on or before $3 (Python's services/fx.py
-- get_rate); as_of_date is unique per pair, so there is no tie to break.
SELECT rate FROM fx_rates
 WHERE from_currency = $1 AND to_currency = $2 AND as_of_date <= $3
 ORDER BY as_of_date DESC
 LIMIT 1;
