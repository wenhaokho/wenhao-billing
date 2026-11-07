-- name: CreateHeldIntakePayment :one
INSERT INTO payments(business_id, payment_id, amount, currency, payer_name, payer_reference, payment_date, intake_source, external_ref, status)
VALUES (sqlc.arg(business_id)::uuid, gen_random_uuid(), sqlc.arg(amount), sqlc.arg(currency),
        sqlc.arg(payer_name), sqlc.narg(payer_reference), sqlc.arg(payment_date),
        sqlc.arg(intake_source), sqlc.arg(external_ref), 'PENDING_MANUAL_REVIEW')
ON CONFLICT (business_id, intake_source, external_ref) DO NOTHING
RETURNING *;

-- name: LogHeldIntakePayment :exec
INSERT INTO reconciliation_log(business_id, log_id, payment_id, action, reasons)
VALUES (sqlc.arg(business_id)::uuid, gen_random_uuid(), sqlc.arg(payment_id), 'HELD', sqlc.arg(reasons));
