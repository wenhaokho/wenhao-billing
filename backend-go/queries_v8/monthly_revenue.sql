-- name: GetMonthlyRevenue :many
SELECT to_char(issue_date, 'YYYY-MM')::text AS month, currency,
       sum(amount)::numeric AS amount, sum(balance_due)::numeric AS unpaid
FROM invoices
WHERE business_id = sqlc.arg(business_id)::uuid
  AND issue_date >= sqlc.arg(window_start)::date
  AND issue_date < sqlc.arg(window_end)::date
  AND status IN ('OPEN', 'SENT', 'PARTIAL', 'PAID')
  AND is_template IS FALSE
GROUP BY 1, 2;
