-- name: AccountsByCode :many
-- The chart-of-accounts rows with the codes; a code with no row is absent.
SELECT code, account_id, type, active FROM chart_of_accounts WHERE code = ANY(sqlc.arg(codes)::text[]);

-- name: AccountsByID :many
-- The chart-of-accounts rows with the ids; an id with no row is absent.
SELECT code, account_id, type, active FROM chart_of_accounts WHERE account_id = ANY(sqlc.arg(ids)::int[]);

-- name: InsertEntry :exec
-- posted_at is the given time, or now when it is null, and never later than
-- now: an issue entry is posted at its issue date, a future one at now.
INSERT INTO journal_entries (entry_id, source_type, source_id, memo, posted_at)
VALUES (sqlc.arg(entry_id), sqlc.arg(source_type), sqlc.arg(source_id), sqlc.narg(memo),
        LEAST(COALESCE(sqlc.narg(posted_at)::timestamp, CURRENT_TIMESTAMP), CURRENT_TIMESTAMP));

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

-- name: EntryPostedAt :one
-- An entry's posted_at: a payment reversal is dated at its original's.
SELECT posted_at FROM journal_entries WHERE entry_id = $1;

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

-- name: InvoiceARBasis :one
-- The invoice's receivable on the ledger: the 1100 lines of its INVOICE
-- entries and of the PAYMENT entries of every payment pointing at it,
-- reversals included. face is in the invoice's currency, base in the base.
SELECT COALESCE(SUM(l.debit - l.credit), 0)::numeric AS face,
       COALESCE(SUM(l.base_amount_debit - l.base_amount_credit), 0)::numeric AS base
  FROM journal_lines l
  JOIN journal_entries e ON e.entry_id = l.entry_id
  JOIN chart_of_accounts a ON a.account_id = l.account_id
 WHERE a.code = '1100'
   AND ((e.source_type = 'INVOICE' AND e.source_id = sqlc.arg(invoice_id)::uuid)
     OR (e.source_type = 'PAYMENT' AND e.source_id IN (SELECT p.payment_id FROM payments p WHERE p.invoice_id = sqlc.arg(invoice_id)::uuid)));

-- name: BillAPBasis :one
-- The bill's payable on the ledger: the 2000 lines of its BILL entries and of
-- the BILL_PAYMENT entries of every bill payment pointing at it, reversals
-- included. face is in the bill's currency (credit − debit), base in the
-- base currency.
SELECT COALESCE(SUM(l.credit - l.debit), 0)::numeric AS face,
       COALESCE(SUM(l.base_amount_credit - l.base_amount_debit), 0)::numeric AS base
  FROM journal_lines l
  JOIN journal_entries e ON e.entry_id = l.entry_id
  JOIN chart_of_accounts a ON a.account_id = l.account_id
 WHERE a.code = '2000'
   AND ((e.source_type = 'BILL' AND e.source_id = sqlc.arg(bill_id)::uuid)
     OR (e.source_type = 'BILL_PAYMENT' AND e.source_id IN (SELECT p.bill_payment_id FROM bill_payments p WHERE p.bill_id = sqlc.arg(bill_id)::uuid)));

-- name: SetEntryMemo :exec
-- The back-post's restated payment entries carry the original's memo with a
-- suffix (plan 12 Task 7); nothing else changes an entry's memo.
UPDATE journal_entries SET memo = sqlc.arg(memo)::text WHERE entry_id = sqlc.arg(entry_id);
