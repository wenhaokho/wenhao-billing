-- name: TrialBalanceLines :many
SELECT l.account_id, l.currency, l.debit::text AS debit, l.credit::text AS credit,
       l.base_amount_debit::text AS base_debit, l.base_amount_credit::text AS base_credit,
       coalesce(a.code, '')::text AS account_code,
       coalesce(a.name, '')::text AS account_name,
       coalesce(a.type::text, '')::text AS account_type,
       e.posted_at,
       (a.account_id IS NULL OR e.entry_id IS NULL) AS damaged_reference
FROM journal_lines l
LEFT JOIN journal_entries e ON e.business_id = l.business_id AND e.entry_id = l.entry_id
LEFT JOIN chart_of_accounts a ON a.business_id = l.business_id AND a.account_id = l.account_id
WHERE l.business_id = sqlc.arg(business_id)::uuid
  AND (e.entry_id IS NULL OR e.posted_at <= sqlc.arg(cutoff)::timestamp);
