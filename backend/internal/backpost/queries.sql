-- name: ListInvoiceCandidates :many
-- Issued invoices with no active INVOICE entry (posted before accrual, or
-- under Python): not templates, OPEN/SENT/PARTIAL/PAID, amount above 0. An
-- active entry is neither reversed nor itself a reversal. Oldest first: rate
-- date (issue date, else the created_at date), created_at, id.
SELECT i.invoice_id
  FROM invoices i
 WHERE NOT i.is_template
   AND i.status IN ('OPEN', 'SENT', 'PARTIAL', 'PAID')
   AND i.amount > 0
   AND NOT EXISTS (
       SELECT 1 FROM journal_entries e
        WHERE e.source_type = 'INVOICE' AND e.source_id = i.invoice_id
          AND e.reversed_by_entry_id IS NULL
          AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id))
 ORDER BY COALESCE(i.issue_date, i.created_at::date), i.created_at, i.invoice_id;

-- name: ListBillCandidates :many
-- Opened bills with no active BILL entry: OPEN/PARTIAL/PAID, amount above
-- 0, in the invoices' order.
SELECT b.bill_id
  FROM bills b
 WHERE b.status IN ('OPEN', 'PARTIAL', 'PAID')
   AND b.amount > 0
   AND NOT EXISTS (
       SELECT 1 FROM journal_entries e
        WHERE e.source_type = 'BILL' AND e.source_id = b.bill_id
          AND e.reversed_by_entry_id IS NULL
          AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id))
 ORDER BY COALESCE(b.issue_date, b.created_at::date), b.created_at, b.bill_id;

-- name: ListClearedPayments :many
-- The invoice's CLEARED payments in the order Python cleared them (the
-- active entry's posted_at, else the payment's created_at; then payment
-- date, id), each with its active PAYMENT entry when it has one (Python's
-- manual payments have none, PORT-NOTES 161). entry_face is what that entry
-- relieved: Σ(credit − debit) over its 1100 lines, in the invoice's currency.
SELECT p.payment_id, p.amount, p.currency, p.payment_date, p.wht_amount,
       (a.entry_id IS NOT NULL)::bool AS has_entry,
       COALESCE(a.entry_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS entry_id,
       COALESCE(a.posted_at, p.payment_date::timestamp)::timestamp AS entry_posted_at,
       a.memo AS entry_memo,
       COALESCE((SELECT SUM(l.credit - l.debit)
                   FROM journal_lines l
                   JOIN chart_of_accounts c ON c.account_id = l.account_id AND c.code = '1100'
                  WHERE l.entry_id = a.entry_id), 0)::numeric AS entry_face
  FROM payments p
  LEFT JOIN LATERAL (
       SELECT e.entry_id, e.posted_at, e.memo
         FROM journal_entries e
        WHERE e.source_type = 'PAYMENT' AND e.source_id = p.payment_id
          AND e.reversed_by_entry_id IS NULL
          AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)
        ORDER BY e.posted_at DESC, e.entry_id DESC
        LIMIT 1) a ON true
 WHERE p.invoice_id = sqlc.arg(invoice_id)::uuid
   AND p.status = 'CLEARED'
 ORDER BY COALESCE(a.posted_at, p.created_at, p.payment_date::timestamp), p.payment_date, p.payment_id;

-- name: ListPaymentNets :many
-- Every payment of the invoice (any status) whose PAYMENT entries' 1100
-- lines do not net to what they should: a CLEARED payment's to its active
-- entry's, any other payment's to 0 (a reversal reversed again under Python
-- leaves them apart).
SELECT p.payment_id, p.status, p.currency,
       COALESCE(SUM(l.credit - l.debit), 0)::numeric AS total_face,
       COALESCE(SUM(l.credit - l.debit) FILTER (
           WHERE e.reversed_by_entry_id IS NULL
             AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)), 0)::numeric AS active_face
  FROM payments p
  JOIN journal_entries e ON e.source_type = 'PAYMENT' AND e.source_id = p.payment_id
  JOIN journal_lines l ON l.entry_id = e.entry_id
  JOIN chart_of_accounts c ON c.account_id = l.account_id AND c.code = '1100'
 WHERE p.invoice_id = sqlc.arg(invoice_id)::uuid
 GROUP BY p.payment_id, p.status, p.currency
HAVING COALESCE(SUM(l.credit - l.debit), 0) <> CASE WHEN p.status = 'CLEARED' THEN COALESCE(SUM(l.credit - l.debit) FILTER (
           WHERE e.reversed_by_entry_id IS NULL
             AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)), 0) ELSE 0 END
 ORDER BY p.payment_id;

-- name: ListMultiEntryPayments :many
-- Every payment of the invoice (any status) with more than one active
-- PAYMENT entry (Python reversed a reversal): ListClearedPayments restates
-- only the newest, so the run refuses them by name.
SELECT p.payment_id, COUNT(*)::int AS active_entries
  FROM payments p
  JOIN journal_entries e ON e.source_type = 'PAYMENT' AND e.source_id = p.payment_id
 WHERE p.invoice_id = sqlc.arg(invoice_id)::uuid
   AND e.reversed_by_entry_id IS NULL
   AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)
 GROUP BY p.payment_id
HAVING COUNT(*) > 1
 ORDER BY p.payment_id;

-- name: ListUnpostedInvoicePayments :many
-- The owner's decisions: CLEARED payments on invoices the back-post never
-- posts (VOID, DRAFT or any other status, templates, amount 0 or less;
-- Python's approve_manual_match does not check the status), each with
-- whether it has an active PAYMENT entry.
SELECT i.invoice_id, i.invoice_number, i.status AS invoice_status, i.is_template, i.amount AS invoice_amount,
       p.payment_id, p.amount, p.currency, p.payment_date,
       EXISTS (SELECT 1 FROM journal_entries e
                WHERE e.source_type = 'PAYMENT' AND e.source_id = p.payment_id
                  AND e.reversed_by_entry_id IS NULL
                  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id))::bool AS has_entry
  FROM invoices i
  JOIN payments p ON p.invoice_id = i.invoice_id AND p.status = 'CLEARED'
 WHERE i.is_template
    OR i.status NOT IN ('OPEN', 'SENT', 'PARTIAL', 'PAID')
    OR i.amount <= 0
 ORDER BY i.invoice_number, i.invoice_id, p.payment_date, p.payment_id;

-- name: ListVoidBillsPartlyPaid :many
-- Warnings: a VOID bill whose balance is not its amount (paid in part under
-- Python) is not posted.
SELECT b.bill_id, b.bill_number, b.currency, b.amount, b.balance_due
  FROM bills b
 WHERE b.status = 'VOID'
   AND b.amount <> b.balance_due
 ORDER BY b.bill_number, b.bill_id;

-- name: ListCustomerAdvances :many
-- Customer advances (owner ruling 2026-11-11): PAID invoices with balance_due
-- 0 and an active issue entry whose ledger receivable is below 0, i.e. more
-- was relieved than issued (Python's approve_manual_match does not check the
-- status). face and base are as ledger.InvoiceBasis (1100 lines of the
-- invoice's entries and of its payments' entries, reversals included).
SELECT i.invoice_id, i.invoice_number, COALESCE(c.name, '(no customer)')::text AS customer_name, i.currency,
       ar.face::numeric AS face, ar.base::numeric AS base
  FROM invoices i
  LEFT JOIN customers c ON c.customer_id = i.customer_id
 CROSS JOIN LATERAL (
       SELECT COALESCE(SUM(l.debit - l.credit), 0) AS face,
              COALESCE(SUM(l.base_amount_debit - l.base_amount_credit), 0) AS base
         FROM journal_lines l
         JOIN journal_entries e ON e.entry_id = l.entry_id
         JOIN chart_of_accounts a ON a.account_id = l.account_id
        WHERE a.code = '1100'
          AND ((e.source_type = 'INVOICE' AND e.source_id = i.invoice_id)
            OR (e.source_type = 'PAYMENT' AND e.source_id IN (SELECT p.payment_id FROM payments p WHERE p.invoice_id = i.invoice_id)))
       ) ar
 WHERE i.status = 'PAID'
   AND i.balance_due = 0
   AND NOT i.is_template
   AND ar.face < 0
   AND EXISTS (SELECT 1 FROM journal_entries ie
                WHERE ie.source_type = 'INVOICE' AND ie.source_id = i.invoice_id
                  AND ie.reversed_by_entry_id IS NULL
                  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = ie.entry_id))
 ORDER BY i.invoice_number, i.invoice_id;

-- name: SumClearedBillPayments :one
-- What the bill's stored (CLEARED) payments already paid.
SELECT COALESCE(SUM(amount), 0)::numeric AS paid
  FROM bill_payments
 WHERE bill_id = $1 AND status = 'CLEARED';

-- name: InsertLegacyBillPayment :exec
INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date, notes, source)
VALUES ($1, $2, $3, $4, $5, $6, 'LEGACY_BACKPOST');

-- name: EntryTotals :many
-- Per account, the base debit and credit of the entries.
SELECT a.code, a.name,
       SUM(l.base_amount_debit)::numeric AS debit,
       SUM(l.base_amount_credit)::numeric AS credit
  FROM journal_lines l
  JOIN chart_of_accounts a ON a.account_id = l.account_id
 WHERE l.entry_id = ANY(sqlc.arg(entry_ids)::uuid[])
 GROUP BY a.code, a.name
 ORDER BY a.code;

-- name: TrialBalance :one
-- Σ base debit and Σ base credit over every journal line.
SELECT COALESCE(SUM(base_amount_debit), 0)::numeric AS debit,
       COALESCE(SUM(base_amount_credit), 0)::numeric AS credit
  FROM journal_lines;
