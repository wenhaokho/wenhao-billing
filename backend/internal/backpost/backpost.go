// Package backpost is `api backpost-ledger` (PORT-NOTES 166): it posts the
// issue entries of the invoices and bills issued before accrual posting
// existed (plan 12), at each document's own issue date and rate, and
// restates the Python-era payments of foreign-currency invoices at the
// invoice's issue basis. It reuses the ledger's posting functions; it posts
// nothing of its own.
//
// It runs in one transaction: a dry run (the default) rolls back, --apply
// commits. A missing FX rate or a share account of the wrong type anywhere
// refuses the whole run before anything is written. A second run finds no
// candidates (every document it posted has an active issue entry).
package backpost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/backpost/backpostdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
)

// ErrRefused is the run's refusal: missing rates or bad share accounts.
// Nothing is written, in a dry run or with --apply.
var ErrRefused = errors.New("backpost refused")

// AccountTotal is one account's base debit and credit over the entries the
// run wrote.
type AccountTotal struct {
	Code, Name    string
	Debit, Credit decimal.Decimal
}

// Report is what a run did (or, in a dry run, would do).
type Report struct {
	// Invoices and Bills are the documents given an issue entry.
	Invoices, Bills int
	// PaymentsRestated are Python's payment entries of foreign-currency
	// invoices, reversed and re-posted at the issue basis.
	PaymentsRestated int
	// PaymentsPosted are CLEARED payments that had no entry (Python's manual
	// payments, PORT-NOTES 161), posted now.
	PaymentsPosted int
	// LegacyBillPayments are the bill payments stored for what bills had paid
	// under Python.
	LegacyBillPayments int
	Totals             []AccountTotal
	MissingRates       []string
	BadAccounts        []string
	Warnings           []string
}

const (
	restatedSuffix = " (restated at issue basis)"
	legacyNotes    = "paid before the switch; Python kept no payment date"
	memoMax        = 500 // journal_entries.memo is varchar(500)
)

// invoiceDoc is a locked candidate invoice and what it posts.
type invoiceDoc struct {
	inv    invoicesdb.Invoice
	label  string
	day    time.Time
	shares []ledger.Share
	// pays are the CLEARED payments to (re)post, oldest first: every one of
	// a foreign-currency invoice, only those with no entry otherwise.
	pays []backpostdb.ListClearedPaymentsRow
}

// billDoc is a locked candidate bill and what it posts.
type billDoc struct {
	bill   billsdb.Bill
	label  string
	day    time.Time
	shares []ledger.Share
	paid   decimal.Decimal // the legacy payment: amount − balance − stored payments
}

type runner struct {
	ctx     context.Context
	tx      pgx.Tx
	q       *backpostdb.Queries
	fq      *fxdb.Queries
	base    string
	w       io.Writer
	rep     Report
	entries []uuid.UUID
}

// Run back-posts in one transaction on pool, in base currency base, and
// prints what it does to w. It commits only when apply is true; a dry run
// rolls back and prints "dry run: nothing written". A refusal (missing
// rates, bad share accounts) is ErrRefused with nothing written; any other
// error rolls back too.
func Run(ctx context.Context, pool *pgxpool.Pool, base string, apply bool, w io.Writer) (Report, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r := &runner{ctx: ctx, tx: tx, q: backpostdb.New(tx), fq: fxdb.New(tx), base: base, w: w}
	if err := r.run(); err != nil {
		if errors.Is(err, ErrRefused) {
			r.printRefusal()
		}
		return r.rep, err
	}
	r.printReport()
	if !apply {
		r.printf("dry run: nothing written\n")
		return r.rep, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return r.rep, fmt.Errorf("backpost: commit: %w", err)
	}
	r.printf("applied: committed\n")
	return r.rep, nil
}

func (r *runner) run() error {
	invs, err := r.lockInvoices()
	if err != nil {
		return err
	}
	bls, err := r.lockBills()
	if err != nil {
		return err
	}
	if err := r.warnVoid(); err != nil {
		return err
	}

	// Preflight: every rate and share account, before anything is written.
	for i := range invs {
		if err := r.checkInvoice(&invs[i]); err != nil {
			return err
		}
	}
	for i := range bls {
		if err := r.checkBill(&bls[i]); err != nil {
			return err
		}
	}
	if len(r.rep.MissingRates) > 0 || len(r.rep.BadAccounts) > 0 {
		return fmt.Errorf("%w: %d missing rates, %d bad accounts", ErrRefused, len(r.rep.MissingRates), len(r.rep.BadAccounts))
	}

	for _, d := range invs {
		if err := r.postInvoice(d); err != nil {
			return fmt.Errorf("backpost: INVOICE %s: %w", d.label, err)
		}
	}
	for _, d := range bls {
		if err := r.postBill(d); err != nil {
			return fmt.Errorf("backpost: BILL %s: %w", d.label, err)
		}
	}
	for _, d := range invs {
		b, err := ledger.InvoiceBasis(r.ctx, r.tx, d.inv.InvoiceID)
		if err != nil {
			return err
		}
		if !b.Face.Equal(d.inv.BalanceDue) {
			r.warn("invoice %s %s %s: ledger face %s ≠ balance_due %s",
				d.inv.InvoiceID, d.label, d.inv.Currency, b.Face.StringFixed(4), d.inv.BalanceDue.StringFixed(4))
		}
	}

	tb, err := r.q.TrialBalance(r.ctx)
	if err != nil {
		return err
	}
	if !tb.Debit.Equal(tb.Credit) {
		return fmt.Errorf("backpost: journal does not balance in base: debit %s, credit %s", tb.Debit.StringFixed(4), tb.Credit.StringFixed(4))
	}
	if len(r.entries) > 0 {
		rows, err := r.q.EntryTotals(r.ctx, r.entries)
		if err != nil {
			return err
		}
		for _, t := range rows {
			r.rep.Totals = append(r.rep.Totals, AccountTotal{Code: t.Code, Name: t.Name, Debit: t.Debit, Credit: t.Credit})
		}
	}
	return nil
}

// lockInvoices locks each candidate (FOR UPDATE, oldest first) and keeps
// the ones still candidates under the lock.
func (r *runner) lockInvoices() ([]invoiceDoc, error) {
	ids, err := r.q.ListInvoiceCandidates(r.ctx)
	if err != nil {
		return nil, err
	}
	iq := invoicesdb.New(r.tx)
	var out []invoiceDoc
	for _, id := range ids {
		inv, err := iq.GetInvoiceForUpdate(r.ctx, id)
		if err != nil {
			return nil, err
		}
		switch inv.Status {
		case "OPEN", "SENT", "PARTIAL", "PAID":
		default:
			continue
		}
		if inv.IsTemplate || !inv.Amount.IsPositive() {
			continue
		}
		if _, active, err := ledger.ActiveEntryFor(r.ctx, r.tx, ledger.SourceInvoice, id); err != nil || active {
			if err != nil {
				return nil, err
			}
			continue
		}
		d := invoiceDoc{inv: inv, label: label(inv.InvoiceNumber, id)}
		switch {
		case inv.IssueDate.Valid:
			d.day = dateOf(inv.IssueDate.Time)
		case inv.CreatedAt != nil:
			d.day = dateOf(*inv.CreatedAt)
			r.warn("INVOICE %s: no issue date; created_at date %s used", d.label, d.day.Format(time.DateOnly))
		default:
			d.day = dateOf(time.Now())
			r.warn("INVOICE %s: no issue date and no created_at; today %s used", d.label, d.day.Format(time.DateOnly))
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *runner) lockBills() ([]billDoc, error) {
	ids, err := r.q.ListBillCandidates(r.ctx)
	if err != nil {
		return nil, err
	}
	bq := billsdb.New(r.tx)
	var out []billDoc
	for _, id := range ids {
		b, err := bq.GetBillForUpdate(r.ctx, id)
		if err != nil {
			return nil, err
		}
		switch b.Status {
		case "OPEN", "PARTIAL", "PAID":
		default:
			continue
		}
		if !b.Amount.IsPositive() {
			continue
		}
		if _, active, err := ledger.ActiveEntryFor(r.ctx, r.tx, ledger.SourceBill, id); err != nil || active {
			if err != nil {
				return nil, err
			}
			continue
		}
		d := billDoc{bill: b, label: label(b.BillNumber, id)}
		if b.IssueDate.Valid {
			d.day = dateOf(b.IssueDate.Time)
		} else {
			d.day = dateOf(b.CreatedAt)
			r.warn("BILL %s: no issue date; created_at date %s used", d.label, d.day.Format(time.DateOnly))
		}
		stored, err := r.q.SumClearedBillPayments(r.ctx, id)
		if err != nil {
			return nil, err
		}
		d.paid = b.Amount.Sub(b.BalanceDue).Sub(stored)
		if d.paid.IsNegative() {
			r.warn("BILL %s: balance_due %s is above amount %s less stored payments %s; no legacy payment",
				d.label, b.BalanceDue.StringFixed(4), b.Amount.StringFixed(4), stored.StringFixed(4))
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *runner) warnVoid() error {
	invs, err := r.q.ListVoidInvoicesWithClearedPayments(r.ctx)
	if err != nil {
		return err
	}
	for _, v := range invs {
		r.warn("INVOICE %s %s is VOID with %d CLEARED payment(s); their receivable credit stays", label(v.InvoiceNumber, v.InvoiceID), v.Currency, v.Payments)
	}
	bls, err := r.q.ListVoidBillsPartlyPaid(r.ctx)
	if err != nil {
		return err
	}
	for _, v := range bls {
		r.warn("BILL %s %s is VOID with amount %s ≠ balance %s; not posted", label(v.BillNumber, v.BillID), v.Currency, v.Amount.StringFixed(4), v.BalanceDue.StringFixed(4))
	}
	return nil
}

// checkInvoice collects d's missing rates and bad share accounts, and
// loads its shares and the payments it posts.
func (r *runner) checkInvoice(d *invoiceDoc) error {
	inv := d.inv
	if err := r.needRate("INVOICE "+d.label, inv.Currency, d.day); err != nil {
		return err
	}
	shares, err := invoices.RevenueShares(r.ctx, r.tx, inv)
	if errors.Is(err, ledger.ErrLedger) {
		r.rep.BadAccounts = append(r.rep.BadAccounts, "INVOICE "+d.label+": "+err.Error())
	} else if err != nil {
		return err
	}
	d.shares = shares

	pays, err := r.q.ListClearedPayments(r.ctx, inv.InvoiceID)
	if err != nil {
		return err
	}
	foreign := inv.Currency != r.base
	for _, p := range pays {
		if p.HasEntry && !foreign {
			continue // a base-currency invoice's payment entries are already right
		}
		d.pays = append(d.pays, p)
		who := "PAYMENT " + p.PaymentID.String() + " of INVOICE " + d.label
		if err := r.needRate(who, p.Currency, p.PaymentDate.Time); err != nil {
			return err
		}
		if p.WhtAmount.IsPositive() && p.Currency != inv.Currency {
			if err := r.needRate(who, inv.Currency, p.PaymentDate.Time); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runner) checkBill(d *billDoc) error {
	if err := r.needRate("BILL "+d.label, d.bill.Currency, d.day); err != nil {
		return err
	}
	shares, err := bills.ExpenseShares(r.ctx, r.tx, d.bill)
	if errors.Is(err, ledger.ErrLedger) {
		r.rep.BadAccounts = append(r.rep.BadAccounts, "BILL "+d.label+": "+err.Error())
		return nil
	}
	d.shares = shares
	return err
}

// needRate records a missing currency→base rate on the day on for who.
func (r *runner) needRate(who, currency string, on time.Time) error {
	_, err := fx.GetRate(r.ctx, r.fq, currency, on, r.base)
	if errors.Is(err, fx.ErrRateMissing) {
		r.rep.MissingRates = append(r.rep.MissingRates, fmt.Sprintf("%s %s %s: no %s→%s rate on or before",
			who, currency, on.Format(time.DateOnly), strings.ToUpper(currency), strings.ToUpper(r.base)))
		return nil
	}
	return err
}

func (r *runner) postInvoice(d invoiceDoc) error {
	inv := d.inv
	e, err := ledger.PostIssued(r.ctx, r.tx, r.base, ledger.Document{
		SourceType: ledger.SourceInvoice, ID: inv.InvoiceID, Currency: inv.Currency, Amount: inv.Amount,
		RateDate: d.day, IssueDate: d.day, Memo: "Invoice " + d.label + " issued (back-posted)", Shares: d.shares,
	})
	if err != nil {
		return err
	}
	r.entries = append(r.entries, e.ID)
	r.rep.Invoices++
	r.printf("INVOICE %s %s %s issued %s\n", d.label, inv.Currency, inv.Amount.StringFixed(4), d.day.Format(time.DateOnly))

	// Python relieved a foreign invoice's receivable at the payment date's
	// rate; reverse those entries (at their own posted_at) so each payment
	// can relieve it at the issue basis.
	for _, p := range d.pays {
		if !p.HasEntry {
			continue
		}
		memo := "Reversal of " + p.EntryID.String() + ": restated at issue basis"
		rev, err := ledger.PostReversalAt(r.ctx, r.tx, p.EntryID, &memo, p.PostAt)
		if err != nil {
			return err
		}
		r.entries = append(r.entries, rev.ID)
	}
	for _, p := range d.pays {
		basis, err := ledger.InvoiceBasis(r.ctx, r.tx, inv.InvoiceID)
		if err != nil {
			return err
		}
		e, err := ledger.PostPaymentClearedAt(r.ctx, r.tx, r.base,
			ledger.Payment{ID: p.PaymentID, Status: "CLEARED", Amount: p.Amount, Currency: p.Currency, Date: p.PaymentDate.Time},
			ledger.Invoice{ID: inv.InvoiceID, Currency: inv.Currency, BalanceDue: basis.Face},
			p.WhtAmount, p.PostAt)
		if err != nil {
			return fmt.Errorf("payment %s: %w", p.PaymentID, err)
		}
		r.entries = append(r.entries, e.ID)
		if p.HasEntry {
			memo := *e.Memo
			if p.EntryMemo != nil && *p.EntryMemo != "" {
				memo = *p.EntryMemo
			}
			memo = truncate(memo, memoMax-len([]rune(restatedSuffix))) + restatedSuffix
			if err := ledger.SetEntryMemo(r.ctx, r.tx, e.ID, memo); err != nil {
				return err
			}
			r.rep.PaymentsRestated++
			r.printf("  PAYMENT %s %s %s restated at issue basis\n", p.PaymentID, p.Currency, p.Amount.StringFixed(4))
		} else {
			r.rep.PaymentsPosted++
			r.printf("  PAYMENT %s %s %s posted (had no entry)\n", p.PaymentID, p.Currency, p.Amount.StringFixed(4))
		}
	}
	return nil
}

func (r *runner) postBill(d billDoc) error {
	b := d.bill
	e, err := ledger.PostIssued(r.ctx, r.tx, r.base, ledger.Document{
		SourceType: ledger.SourceBill, ID: b.BillID, Currency: b.Currency, Amount: b.Amount,
		RateDate: d.day, IssueDate: d.day, Memo: "Bill " + d.label + " opened (back-posted)", Shares: d.shares,
	})
	if err != nil {
		return err
	}
	r.entries = append(r.entries, e.ID)
	r.rep.Bills++
	r.printf("BILL %s %s %s opened %s\n", d.label, b.Currency, b.Amount.StringFixed(4), d.day.Format(time.DateOnly))
	if !d.paid.IsPositive() {
		return nil
	}

	// Python kept no payment date: the payment is dated the bill's rate date,
	// so no rate is guessed (it relieves the payable at its own basis).
	notes := legacyNotes
	id := uuid.New()
	if err := r.q.InsertLegacyBillPayment(r.ctx, backpostdb.InsertLegacyBillPaymentParams{
		BillPaymentID: id, BillID: b.BillID, Amount: d.paid, Currency: b.Currency,
		PaymentDate: pgDate(d.day), Notes: &notes,
	}); err != nil {
		return err
	}
	pe, err := ledger.PostBillPaymentAt(r.ctx, r.tx, r.base, ledger.BillPayment{
		ID: id, Amount: d.paid, Currency: b.Currency, Date: d.day,
		Memo: "Legacy payment " + id.String() + " of bill " + d.label + " (back-posted)",
	}, b.BillID, d.day)
	if err != nil {
		return err
	}
	r.entries = append(r.entries, pe.ID)
	r.rep.LegacyBillPayments++
	r.printf("  legacy bill payment: BILL %s %s %s dated %s\n", d.label, b.Currency, d.paid.StringFixed(4), d.day.Format(time.DateOnly))
	return nil
}

func (r *runner) warn(format string, args ...any) {
	r.rep.Warnings = append(r.rep.Warnings, fmt.Sprintf(format, args...))
}

func (r *runner) printReport() {
	rep := r.rep
	r.printf("invoices issued: %d\nbills opened: %d\npayments restated at issue basis: %d\npayments posted (had no entry): %d\nlegacy bill payments: %d\n",
		rep.Invoices, rep.Bills, rep.PaymentsRestated, rep.PaymentsPosted, rep.LegacyBillPayments)
	if len(rep.Totals) > 0 {
		r.printf("account totals of the entries written (%s):\n", r.base)
		for _, t := range rep.Totals {
			r.printf("  %s %-28s Dr %20s  Cr %20s\n", t.Code, t.Name, t.Debit.StringFixed(4), t.Credit.StringFixed(4))
		}
	}
	r.printList("warnings", rep.Warnings)
}

func (r *runner) printRefusal() {
	r.printf("backpost-ledger refused: nothing written\n")
	r.printList("missing rates", r.rep.MissingRates)
	r.printList("bad accounts", r.rep.BadAccounts)
	r.printList("warnings", r.rep.Warnings)
}

func (r *runner) printList(title string, items []string) {
	if len(items) == 0 {
		return
	}
	r.printf("%s:\n", title)
	for _, s := range items {
		r.printf("  %s\n", s)
	}
}

// label is a document's number, else its id.
func label(number *string, id uuid.UUID) string {
	if number != nil && *number != "" {
		return *number
	}
	return id.String()
}

// dateOf is t's UTC calendar date at 00:00 UTC.
func dateOf(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// printf writes to the run's output; a failed write does not stop the run.
func (r *runner) printf(format string, args ...any) { _, _ = fmt.Fprintf(r.w, format, args...) }
