-- +goose Up
-- Go's own (UI revamp part 2, "Manual Review" reason tags): the code of each
-- reason sentence, in the same order, so a client tags a row without parsing
-- English. payments.ReasonCode writes it for new rows. The UPDATE backfills
-- the existing rows with the same prefix rules (case-sensitive, as the code
-- writes the sentences). TestReasonCodesBackfill runs this UPDATE against
-- every known sentence and compares it with ReasonCode, so keep the two in
-- step.
ALTER TABLE reconciliation_log ADD COLUMN reason_codes text[] NOT NULL DEFAULT '{}';

-- Any reason after an 'exact amount+currency' one is CLEAR_FAILED (the
-- clearing failed after the engine cleared, often with a free-text ledger or
-- FX refusal), as payments.ReasonCodes does. TestReasonCodesBackfill extracts
-- this statement up to its first semicolon, so keep semicolons out of the
-- statement, its comments and its literals.
UPDATE reconciliation_log l
   SET reason_codes = ARRAY(
       SELECT CASE
                WHEN EXISTS (SELECT 1 FROM unnest(l.reasons) WITH ORDINALITY AS e(r, n)
                              WHERE e.n < u.n AND e.r LIKE 'exact amount+currency%') THEN 'CLEAR_FAILED'
                WHEN u.r LIKE 'no open invoice matches%' THEN 'NO_CANDIDATE'
                WHEN u.r LIKE 'currency mismatch:%' THEN 'CURRENCY_MISMATCH'
                WHEN u.r LIKE 'amount mismatch:%' THEN 'AMOUNT_MISMATCH'
                WHEN u.r LIKE 'ambiguous match:%' THEN 'AMBIGUOUS'
                WHEN u.r LIKE 'exact amount+currency%' THEN 'EXACT_MATCH'
                WHEN u.r LIKE 'manual-entry%' THEN 'MANUAL_ENTRY'
                WHEN u.r LIKE 'manual approval against invoice%' THEN 'MANUAL_APPROVAL'
                WHEN u.r LIKE 'alias match:%' THEN 'ALIAS_MATCH'
                WHEN u.r LIKE 'confidence %below threshold%' THEN 'LOW_CONFIDENCE'
                WHEN u.r LIKE 'invoice % changed before clearing' THEN 'CLEAR_FAILED'
                ELSE 'OTHER'
              END
         FROM unnest(l.reasons) WITH ORDINALITY AS u(r, n)
        ORDER BY u.n)
 WHERE cardinality(l.reasons) > 0;

-- +goose Down
ALTER TABLE reconciliation_log DROP COLUMN reason_codes;
