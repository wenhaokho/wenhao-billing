-- name: AccountIDsByCode :many
-- The ids of the chart-of-accounts codes the ledger posts to; a code with no
-- row is absent from the result.
SELECT code, account_id FROM chart_of_accounts WHERE code = ANY(sqlc.arg(codes)::text[]);

-- name: InsertEntry :exec
-- posted_at is the column's default (Python's server_default).
INSERT INTO journal_entries (entry_id, source_type, source_id, memo)
VALUES ($1, $2, $3, $4);

-- name: InsertLine :exec
INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency,
                           base_amount_debit, base_amount_credit, fx_rate)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: LockEntry :one
-- The entry to reverse, locked until the caller's transaction ends so two
-- reversals of one entry run one after the other and the second sees the
-- first's reversed_by_entry_id.
SELECT source_type, source_id, (reversed_by_entry_id IS NOT NULL)::bool AS reversed
  FROM journal_entries
 WHERE entry_id = $1
   FOR UPDATE;

-- name: EntryLines :many
-- No ORDER BY: Python's relationship loads the lines in table order, and no
-- column records the order they were posted in.
SELECT account_id, debit, credit, currency, base_amount_debit, base_amount_credit, fx_rate
  FROM journal_lines
 WHERE entry_id = $1;

-- name: SetReversedBy :exec
UPDATE journal_entries SET reversed_by_entry_id = sqlc.arg(reversed_by)::uuid WHERE entry_id = sqlc.arg(entry_id);

-- name: ActiveEntryFor :one
-- The newest entry for a source that is neither reversed nor itself a reversal
-- (no entry's reversed_by_entry_id points at it). A reversal shares the
-- original's source and has reversed_by_entry_id NULL, so the NOT EXISTS is what
-- keeps it out.
SELECT e.entry_id
  FROM journal_entries e
 WHERE e.source_type = $1
   AND e.source_id = $2
   AND e.reversed_by_entry_id IS NULL
   AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)
 ORDER BY e.posted_at DESC, e.entry_id DESC
 LIMIT 1;
