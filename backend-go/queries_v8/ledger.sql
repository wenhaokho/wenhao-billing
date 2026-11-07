-- name: LedgerAccount :one
SELECT account_id FROM chart_of_accounts WHERE business_id=sqlc.arg(business_id)::uuid AND code = $1;

-- name: LedgerCreateEntry :exec
INSERT INTO journal_entries (business_id, entry_id, source_type, source_id, memo) VALUES (sqlc.arg(business_id)::uuid,$1,$2,$3,$4);

-- name: LedgerCreateLine :exec
INSERT INTO journal_lines (business_id,line_id,entry_id,account_id,debit,credit,currency,base_amount_debit,base_amount_credit,fx_rate) VALUES (sqlc.arg(business_id)::uuid,$1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: LedgerLockEntry :one
SELECT * FROM journal_entries WHERE business_id=sqlc.arg(business_id)::uuid AND entry_id = $1 FOR UPDATE;

-- name: LedgerLines :many
SELECT * FROM journal_lines WHERE business_id=sqlc.arg(business_id)::uuid AND entry_id = $1 ORDER BY line_id;

-- name: LedgerLinkReversal :exec
UPDATE journal_entries SET reversed_by_entry_id = $2 WHERE business_id=sqlc.arg(business_id)::uuid AND entry_id = $1;

-- name: LedgerLockPayment :one
SELECT * FROM payments
WHERE business_id = sqlc.arg(business_id)::uuid
  AND payment_id = sqlc.arg(payment_id)
  AND invoice_id = sqlc.arg(invoice_id)
FOR UPDATE;

-- name: LedgerPaymentPosted :one
SELECT EXISTS (
  SELECT 1 FROM journal_entries
  WHERE business_id = sqlc.arg(business_id)::uuid AND source_type = 'PAYMENT' AND source_id = sqlc.arg(payment_id)::uuid
);

-- name: LedgerReversalMatches :one
SELECT EXISTS (
  SELECT 1
  FROM journal_entries original
  JOIN journal_entries reversal ON reversal.business_id = original.business_id
    AND reversal.entry_id = sqlc.arg(reversal_id)::uuid
    AND reversal.entry_id <> original.entry_id
    AND reversal.reversed_by_entry_id IS DISTINCT FROM original.entry_id
    AND reversal.reversed_by_entry_id IS DISTINCT FROM reversal.entry_id
    AND reversal.source_type = original.source_type AND reversal.source_id = original.source_id
  WHERE original.business_id = sqlc.arg(business_id)::uuid
    AND original.entry_id = sqlc.arg(original_id)::uuid
    AND (SELECT jsonb_agg(jsonb_build_array(account_id,currency,debit,credit,base_amount_debit,base_amount_credit,fx_rate)
        ORDER BY account_id,currency,debit,credit,base_amount_debit,base_amount_credit,fx_rate)
      FROM journal_lines WHERE business_id = original.business_id AND entry_id = original.entry_id)
      = (SELECT jsonb_agg(jsonb_build_array(account_id,currency,credit,debit,base_amount_credit,base_amount_debit,fx_rate)
        ORDER BY account_id,currency,credit,debit,base_amount_credit,base_amount_debit,fx_rate)
      FROM journal_lines WHERE business_id = reversal.business_id AND entry_id = reversal.entry_id)
);


-- name: LedgerHasInvalidLineAccount :one
SELECT EXISTS (
  SELECT 1 FROM journal_lines l
  WHERE l.business_id = sqlc.arg(business_id)::uuid AND l.entry_id = sqlc.arg(entry_id)
    AND NOT EXISTS (
      SELECT 1 FROM chart_of_accounts a
      WHERE a.business_id = l.business_id AND a.account_id = l.account_id
    )
);
