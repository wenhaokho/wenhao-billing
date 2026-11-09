-- name: TrialBalanceRows :many
-- Inner joins: an account with no line before the cutoff is absent, and the
-- inactive ones are included. cutoff is the day after as_of at midnight, so
-- every posting of as_of's own day is in (Python's <= 23:59:59.999999).
SELECT a.account_id, a.code, a.name, a.type, l.currency,
       COALESCE(SUM(l.debit), 0)::numeric AS debit,
       COALESCE(SUM(l.credit), 0)::numeric AS credit,
       COALESCE(SUM(l.base_amount_debit), 0)::numeric AS base_debit,
       COALESCE(SUM(l.base_amount_credit), 0)::numeric AS base_credit
  FROM chart_of_accounts a
  JOIN journal_lines l ON l.account_id = a.account_id
  JOIN journal_entries e ON e.entry_id = l.entry_id
 WHERE e.posted_at < @cutoff::timestamp
 GROUP BY a.account_id, a.code, a.name, a.type, l.currency
 ORDER BY a.code, l.currency;

-- name: BalanceSheetRows :many
-- The cutoff filters the lines summed (Python's sat in an outer join's ON and
-- filtered nothing). An account with no line before it sums to zero and is
-- skipped by the report either way, so the joins are inner.
SELECT a.account_id, a.code, a.name, a.type,
       COALESCE(SUM(l.base_amount_debit), 0)::numeric AS base_debit,
       COALESCE(SUM(l.base_amount_credit), 0)::numeric AS base_credit
  FROM chart_of_accounts a
  JOIN journal_lines l ON l.account_id = a.account_id
  JOIN journal_entries e ON e.entry_id = l.entry_id
 WHERE e.posted_at < @cutoff::timestamp
 GROUP BY a.account_id, a.code, a.name, a.type
 ORDER BY a.code;

-- name: RevaluationRows :many
-- Monetary buckets only: ASSET and LIABILITY accounts, lines whose currency
-- is not the base (compared as given, no case folding). The cutoff is in the
-- WHERE, so it filters here.
SELECT a.account_id, a.code, a.name, a.type, l.currency,
       COALESCE(SUM(l.debit), 0)::numeric AS debit,
       COALESCE(SUM(l.credit), 0)::numeric AS credit,
       COALESCE(SUM(l.base_amount_debit), 0)::numeric AS base_debit,
       COALESCE(SUM(l.base_amount_credit), 0)::numeric AS base_credit
  FROM chart_of_accounts a
  JOIN journal_lines l ON l.account_id = a.account_id
  JOIN journal_entries e ON e.entry_id = l.entry_id
 WHERE e.posted_at < @cutoff::timestamp
   AND a.type IN ('ASSET', 'LIABILITY')
   AND l.currency <> @base_currency::text
 GROUP BY a.account_id, a.code, a.name, a.type, l.currency
 ORDER BY a.code, l.currency;
