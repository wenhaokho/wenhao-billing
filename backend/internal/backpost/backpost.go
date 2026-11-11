// Package backpost is `api backpost-ledger` (PORT-NOTES 166): it posts the
// issue entries of the invoices and bills issued before accrual posting
// existed (plan 12), at each document's own issue date and rate, and
// restates the Python-era payments of foreign-currency invoices at the
// invoice's issue basis. It reuses the ledger's posting functions; it posts
// nothing of its own.
//
// It runs in one transaction: a dry run (the default) rolls back, --apply
// commits. A missing FX rate, a share account of the wrong type, a payment
// whose entries do not net, a posting the ledger refuses, or a restated
// invoice whose ledger receivable would not be its balance due anywhere
// refuses the whole run: everything is listed and nothing is written. A
// second run finds no candidates (every document it posted has an active
// issue entry).
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

// ErrRefused is the run's refusal: missing rates, bad share accounts or
// inconsistencies (Report.Inconsistent). Nothing is written, in a dry run
// or with --apply.
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
	// payments, PORT-NOTES 161), posted on their payment date.
	PaymentsPosted int
	// LegacyBillPayments are the bill payments stored for what bills had paid
	// under Python.
	LegacyBillPayments int
	Totals             []AccountTotal
	MissingRates       []string
	BadAccounts        []string
	// Inconsistent are the refusals found in the data or while posting: a
	// payment whose entries do not net, a posting the ledger refuses, a
	// restated invoice whose ledger receivable is not its balance due.
	Inconsistent []string
	// OwnerDecisions are CLEARED payments on invoices the back-post never
	// posts (VOID, DRAFT, templates, amount 0): listed, never posted.
	OwnerDecisions []string
	Warnings       []string
}

func (rep Report) refused() bool {
	return len(rep.MissingRates) > 0 || len(rep.BadAccounts) > 0 || len(rep.Inconsistent) > 0
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
	// pays are the CLEARED payments to (re)post, in clearing order: every
	// one of a foreign-currency invoice, only those with no entry otherwise.
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
	ctx  context.Context
	tx   pgx.Tx
	q    *backpostdb.Queries
	fq   *fxdb.Queries
	base string
	w    io.Writer
	rep  Report
	// entries are the entries written, for the totals.
	entries []uuid.UUID
	// lines are the documents posted, printed with the report (not on a
	// refusal, which writes nothing).
	lines []string
}

// Run back-posts in one transaction on pool, in base currency base, and
// prints what it does to w. It commits only when apply is true; a dry run
// rolls back and prints "dry run: nothing written". A refusal is ErrRefused
// with nothing written and every reason listed; any other error rolls back
// too.
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

func (r *runner) refusal() error {
	return fmt.Errorf("%w: %d missing rates, %d bad accounts, %d inconsistent", ErrRefused,
		len(r.rep.MissingRates), len(r.rep.BadAccounts), len(r.rep.Inconsistent))
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
	if err := r.ownerDecisions(); err != nil {
		return err
	}
	if err := r.warnVoidBills(); err != nil {
		return err
	}

	// Preflight: every rate, share account and payment, before anything is
	// written.
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
	if r.rep.refused() {
		return r.refusal()
	}

	// Each document posts under its own savepoint, so a refusal is listed and
	// the run goes on to list every other one; any refusal refuses the run.
	for _, d := range invs {
		if err := r.document("INVOICE "+d.label, func(tx pgx.Tx, out *docOut) error { return r.postInvoice(tx, d, out) }); err != nil {
			return err
		}
	}
	for _, d := range bls {
		if err := r.document("BILL "+d.label, func(tx pgx.Tx, out *docOut) error { return r.postBill(tx, d, out) }); err != nil {
			return err
		}
	}
	if r.rep.refused() {
		return r.refusal()
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

// docOut is what one document's posting adds to the report; it is kept
// only when the document's savepoint is.
type docOut struct {
	invoices, bills, restated, posted, legacy int
	entries                                   []uuid.UUID
	lines                                     []string
	// inconsistent: posted, but the result is refused (the face check).
	inconsistent []string
	warnings     []string
}

func (o *docOut) printf(format string, args ...any) {
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}

// document runs post under a savepoint. A ledger refusal or a missing rate
// rolls the savepoint back and is listed as inconsistent; any other error
// stops the run.
func (r *runner) document(who string, post func(pgx.Tx, *docOut) error) error {
	sp, err := r.tx.Begin(r.ctx)
	if err != nil {
		return err
	}
	var out docOut
	err = post(sp, &out)
	if errors.Is(err, ledger.ErrLedger) || errors.Is(err, fx.ErrRateMissing) {
		if rbErr := sp.Rollback(r.ctx); rbErr != nil {
			return rbErr
		}
		r.rep.Inconsistent = append(r.rep.Inconsistent, who+": "+err.Error())
		return nil
	}
	if err != nil {
		_ = sp.Rollback(r.ctx)
		return fmt.Errorf("backpost: %s: %w", who, err)
	}
	if err := sp.Commit(r.ctx); err != nil {
		return err
	}
	r.rep.Invoices += out.invoices
	r.rep.Bills += out.bills
	r.rep.PaymentsRestated += out.restated
	r.rep.PaymentsPosted += out.posted
	r.rep.LegacyBillPayments += out.legacy
	r.rep.Inconsistent = append(r.rep.Inconsistent, out.inconsistent...)
	r.rep.Warnings = append(r.rep.Warnings, out.warnings...)
	r.entries = append(r.entries, out.entries...)
	r.lines = append(r.lines, out.lines...)
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
			d.day = ledger.Midnight(inv.IssueDate.Time)
		case inv.CreatedAt != nil:
			d.day = ledger.Midnight(inv.CreatedAt.UTC())
			r.warn("INVOICE %s: no issue date; created_at date %s used", d.label, d.day.Format(time.DateOnly))
		default:
			d.day = ledger.Midnight(time.Now().UTC())
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
			d.day = ledger.Midnight(b.IssueDate.Time)
		} else {
			d.day = ledger.Midnight(b.CreatedAt.UTC())
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

// ownerDecisions lists the CLEARED payments on invoices the back-post never
// posts: whether to void, re-issue or move them is the owner's call.
func (r *runner) ownerDecisions() error {
	rows, err := r.q.ListUnpostedInvoicePayments(r.ctx)
	if err != nil {
		return err
	}
	for _, v := range rows {
		what := v.InvoiceStatus
		if v.IsTemplate {
			what += ", template"
		}
		if !v.InvoiceAmount.IsPositive() {
			what += ", amount " + v.InvoiceAmount.StringFixed(4)
		}
		state := "no entry: nothing on the ledger"
		if v.HasEntry {
			state = "has an entry: its receivable credit stays on the ledger"
		}
		r.rep.OwnerDecisions = append(r.rep.OwnerDecisions, fmt.Sprintf("INVOICE %s (%s) PAYMENT %s %s %s dated %s: %s",
			label(v.InvoiceNumber, v.InvoiceID), what, v.PaymentID, v.Currency, v.Amount.StringFixed(4),
			v.PaymentDate.Time.Format(time.DateOnly), state))
	}
	return nil
}

func (r *runner) warnVoidBills() error {
	bls, err := r.q.ListVoidBillsPartlyPaid(r.ctx)
	if err != nil {
		return err
	}
	for _, v := range bls {
		r.warn("BILL %s %s is VOID with amount %s ≠ balance %s; not posted", label(v.BillNumber, v.BillID), v.Currency, v.Amount.StringFixed(4), v.BalanceDue.StringFixed(4))
	}
	return nil
}

// checkInvoice collects d's missing rates, bad share accounts and payments
// whose entries do not net, warns of payments dated before the issue, and
// loads its shares and the payments it posts.
func (r *runner) checkInvoice(d *invoiceDoc) error {
	inv := d.inv
	who := "INVOICE " + d.label
	if err := r.needRate(who, inv.Currency, d.day); err != nil {
		return err
	}
	shares, err := invoices.RevenueShares(r.ctx, r.tx, inv)
	if errors.Is(err, ledger.ErrLedger) {
		r.rep.BadAccounts = append(r.rep.BadAccounts, who+": "+err.Error())
	} else if err != nil {
		return err
	}
	d.shares = shares

	nets, err := r.q.ListPaymentNets(r.ctx, inv.InvoiceID)
	if err != nil {
		return err
	}
	for _, n := range nets {
		r.rep.Inconsistent = append(r.rep.Inconsistent, fmt.Sprintf(
			"PAYMENT %s of %s (%s): its entries relieve %s %s net but should relieve %s (its reversals do not pair)",
			n.PaymentID, who, n.Status, n.TotalFace.StringFixed(4), inv.Currency, expectedNet(n).StringFixed(4)))
	}

	pays, err := r.q.ListClearedPayments(r.ctx, inv.InvoiceID)
	if err != nil {
		return err
	}
	foreign := inv.Currency != r.base
	for _, p := range pays {
		pwho := "PAYMENT " + p.PaymentID.String() + " of " + who
		at := ledger.Midnight(p.PaymentDate.Time)
		if p.HasEntry {
			at = p.EntryPostedAt
		}
		if ledger.Midnight(at).Before(d.day) {
			r.warn("%s is on the ledger on %s, before the invoice's issue date %s: the receivable is below 0 in between",
				pwho, at.Format(time.DateOnly), d.day.Format(time.DateOnly))
		}
		if p.HasEntry && !foreign {
			continue // a base-currency invoice's payment entries are already right
		}
		if p.HasEntry && p.Currency == inv.Currency && !p.Amount.Add(p.WhtAmount).Equal(p.EntryFace) {
			r.rep.Inconsistent = append(r.rep.Inconsistent, fmt.Sprintf(
				"%s: its entry relieved %s %s, not its amount + withheld %s", pwho,
				p.EntryFace.StringFixed(4), inv.Currency, p.Amount.Add(p.WhtAmount).StringFixed(4)))
		}
		d.pays = append(d.pays, p)
		if err := r.needRate(pwho, p.Currency, p.PaymentDate.Time); err != nil {
			return err
		}
		if p.WhtAmount.IsPositive() && p.Currency != inv.Currency {
			if err := r.needRate(pwho, inv.Currency, p.PaymentDate.Time); err != nil {
				return err
			}
		}
	}
	return nil
}

// expectedNet is what a payment's entries should net to: its active
// entry's relief when CLEARED, else 0.
func expectedNet(n backpostdb.ListPaymentNetsRow) decimal.Decimal {
	if n.Status == "CLEARED" {
		return n.ActiveFace
	}
	return decimal.Zero
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

func (r *runner) postInvoice(tx pgx.Tx, d invoiceDoc, out *docOut) error {
	inv := d.inv
	e, err := ledger.PostIssued(r.ctx, tx, r.base, ledger.Document{
		SourceType: ledger.SourceInvoice, ID: inv.InvoiceID, Currency: inv.Currency, Amount: inv.Amount,
		RateDate: d.day, IssueDate: d.day, Memo: "Invoice " + d.label + " issued (back-posted)", Shares: d.shares,
	})
	if err != nil {
		return err
	}
	out.entries = append(out.entries, e.ID)
	out.invoices++
	out.printf("INVOICE %s %s %s issued %s", d.label, inv.Currency, inv.Amount.StringFixed(4), d.day.Format(time.DateOnly))

	// Python relieved a foreign invoice's receivable at the payment date's
	// rate; reverse those entries (at their own posted_at, as every payment
	// reversal) so each payment can relieve it at the issue basis.
	for _, p := range d.pays {
		if !p.HasEntry {
			continue
		}
		memo := "Reversal of " + p.EntryID.String() + ": restated at issue basis"
		rev, err := ledger.ReversePayment(r.ctx, tx, p.EntryID, &memo)
		if err != nil {
			return err
		}
		out.entries = append(out.entries, rev.ID)
	}
	for _, p := range d.pays {
		pay := ledger.Payment{ID: p.PaymentID, Status: "CLEARED", Amount: p.Amount, Currency: p.Currency, Date: p.PaymentDate.Time}
		var balance decimal.Decimal
		var at time.Time
		if p.HasEntry {
			// What Python relieved, at Python's posted_at (owner decision).
			balance, at = p.EntryFace, p.EntryPostedAt
			memo := fmt.Sprintf("Payment %s cleared against invoice %s", p.PaymentID, inv.InvoiceID)
			if p.EntryMemo != nil && *p.EntryMemo != "" {
				memo = *p.EntryMemo
			}
			pay.Memo = truncate(memo, memoMax-len([]rune(restatedSuffix))) + restatedSuffix
		} else {
			// Python's manual payment: the live rule, on its payment date
			// (PORT-NOTES 167), relieving the receivable that is left.
			basis, err := ledger.InvoiceBasis(r.ctx, tx, inv.InvoiceID)
			if err != nil {
				return err
			}
			balance, at = basis.Face, ledger.Midnight(p.PaymentDate.Time)
		}
		e, err := ledger.PostPaymentClearedAt(r.ctx, tx, r.base, pay,
			ledger.Invoice{ID: inv.InvoiceID, Currency: inv.Currency, BalanceDue: balance}, p.WhtAmount, at)
		if err != nil {
			return fmt.Errorf("payment %s: %w", p.PaymentID, err)
		}
		out.entries = append(out.entries, e.ID)
		if p.HasEntry {
			out.restated++
			out.printf("  PAYMENT %s %s %s restated at issue basis", p.PaymentID, p.Currency, p.Amount.StringFixed(4))
		} else {
			out.posted++
			out.printf("  PAYMENT %s %s %s posted (had no entry)", p.PaymentID, p.Currency, p.Amount.StringFixed(4))
		}
	}

	b, err := ledger.InvoiceBasis(r.ctx, tx, inv.InvoiceID)
	if err != nil {
		return err
	}
	if !b.Face.Equal(inv.BalanceDue) {
		msg := fmt.Sprintf("invoice %s %s %s: ledger face %s ≠ balance_due %s",
			inv.InvoiceID, d.label, inv.Currency, b.Face.StringFixed(4), inv.BalanceDue.StringFixed(4))
		if len(d.pays) > 0 {
			// The run (re)posted its payments: a receivable left apart from
			// the balance is the run's own result, so it is refused.
			out.inconsistent = append(out.inconsistent, msg)
		} else {
			out.warnings = append(out.warnings, msg)
		}
	}
	return nil
}

func (r *runner) postBill(tx pgx.Tx, d billDoc, out *docOut) error {
	b := d.bill
	e, err := ledger.PostIssued(r.ctx, tx, r.base, ledger.Document{
		SourceType: ledger.SourceBill, ID: b.BillID, Currency: b.Currency, Amount: b.Amount,
		RateDate: d.day, IssueDate: d.day, Memo: "Bill " + d.label + " opened (back-posted)", Shares: d.shares,
	})
	if err != nil {
		return err
	}
	out.entries = append(out.entries, e.ID)
	out.bills++
	out.printf("BILL %s %s %s opened %s", d.label, b.Currency, b.Amount.StringFixed(4), d.day.Format(time.DateOnly))
	if !d.paid.IsPositive() {
		return nil
	}

	// Python kept no payment date: the payment is dated the bill's rate date,
	// so no rate is guessed (it relieves the payable at its own basis).
	notes := legacyNotes
	id := uuid.New()
	if err := backpostdb.New(tx).InsertLegacyBillPayment(r.ctx, backpostdb.InsertLegacyBillPaymentParams{
		BillPaymentID: id, BillID: b.BillID, Amount: d.paid, Currency: b.Currency,
		PaymentDate: pgDate(d.day), Notes: &notes,
	}); err != nil {
		return err
	}
	pe, err := ledger.PostBillPaymentAt(r.ctx, tx, r.base, ledger.BillPayment{
		ID: id, Amount: d.paid, Currency: b.Currency, Date: d.day,
		Memo: "Legacy payment " + id.String() + " of bill " + d.label + " (back-posted)",
	}, b.BillID, d.day)
	if err != nil {
		return err
	}
	out.entries = append(out.entries, pe.ID)
	out.legacy++
	out.printf("  legacy bill payment: BILL %s %s %s dated %s", d.label, b.Currency, d.paid.StringFixed(4), d.day.Format(time.DateOnly))
	return nil
}

func (r *runner) warn(format string, args ...any) {
	r.rep.Warnings = append(r.rep.Warnings, fmt.Sprintf(format, args...))
}

func (r *runner) printReport() {
	rep := r.rep
	for _, l := range r.lines {
		r.printf("%s\n", l)
	}
	r.printf("invoices issued: %d\nbills opened: %d\npayments restated at issue basis: %d\npayments posted (had no entry): %d\nlegacy bill payments: %d\n",
		rep.Invoices, rep.Bills, rep.PaymentsRestated, rep.PaymentsPosted, rep.LegacyBillPayments)
	if len(rep.Totals) > 0 {
		r.printf("account totals of the entries written (%s):\n", r.base)
		for _, t := range rep.Totals {
			r.printf("  %s %-28s Dr %20s  Cr %20s\n", t.Code, t.Name, t.Debit.StringFixed(4), t.Credit.StringFixed(4))
		}
	}
	r.printList("owner decisions (CLEARED payments on invoices never posted)", rep.OwnerDecisions)
	r.printList("warnings", rep.Warnings)
}

func (r *runner) printRefusal() {
	r.printf("backpost-ledger refused: nothing written\n")
	r.printList("missing rates", r.rep.MissingRates)
	r.printList("bad accounts", r.rep.BadAccounts)
	r.printList("inconsistent", r.rep.Inconsistent)
	r.printList("owner decisions (CLEARED payments on invoices never posted)", r.rep.OwnerDecisions)
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

// printf writes to the run's output; a failed write does not stop the run.
func (r *runner) printf(format string, args ...any) { _, _ = fmt.Fprintf(r.w, format, args...) }

// label is a document's number, else its id.
func label(number *string, id uuid.UUID) string {
	if number != nil && *number != "" {
		return *number
	}
	return id.String()
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }
