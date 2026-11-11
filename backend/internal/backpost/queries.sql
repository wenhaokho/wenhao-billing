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
-- The invoice's CLEARED payments, oldest first (payment date, created_at,
-- id), each with its active PAYMENT entry when it has one (Python's manual
-- payments have none, PORT-NOTES 161). post_at is when its entry is (re)posted:
-- the active entry's posted_at, else the payment's created_at (else its
-- payment date).
SELECT p.payment_id, p.amount, p.currency, p.payment_date, p.wht_amount,
       (a.entry_id IS NOT NULL)::bool AS has_entry,
       COALESCE(a.entry_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS entry_id,
       COALESCE(a.posted_at, p.created_at, p.payment_date::timestamp)::timestamp AS post_at,
       a.memo AS entry_memo
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
 ORDER BY p.payment_date, p.created_at, p.payment_id;

-- name: ListVoidInvoicesWithClearedPayments :many
-- Warnings: a VOID invoice's CLEARED payments keep their receivable credit.
SELECT i.invoice_id, i.invoice_number, i.currency, COUNT(p.payment_id)::int AS payments
  FROM invoices i
  JOIN payments p ON p.invoice_id = i.invoice_id AND p.status = 'CLEARED'
 WHERE i.status = 'VOID'
 GROUP BY i.invoice_id, i.invoice_number, i.currency
 ORDER BY i.invoice_number, i.invoice_id;

-- name: ListVoidBillsPartlyPaid :many
-- Warnings: a VOID bill whose balance is not its amount (paid in part under
-- Python) is not posted.
SELECT b.bill_id, b.bill_number, b.currency, b.amount, b.balance_due
  FROM bills b
 WHERE b.status = 'VOID'
   AND b.amount <> b.balance_due
 ORDER BY b.bill_number, b.bill_id;

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
