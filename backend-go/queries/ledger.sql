-- name: LedgerAccount :one
SELECT account_id FROM chart_of_accounts WHERE code = $1;

-- name: LedgerCreateEntry :exec
INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES ($1,$2,$3,$4);

-- name: LedgerCreateLine :exec
INSERT INTO journal_lines (line_id,entry_id,account_id,debit,credit,currency,base_amount_debit,base_amount_credit,fx_rate) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: LedgerLockEntry :one
SELECT * FROM journal_entries WHERE entry_id = $1 FOR UPDATE;

-- name: LedgerLines :many
SELECT * FROM journal_lines WHERE entry_id = $1 ORDER BY line_id;

-- name: LedgerLinkReversal :exec
UPDATE journal_entries SET reversed_by_entry_id = $2 WHERE entry_id = $1;
