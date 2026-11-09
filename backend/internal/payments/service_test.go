package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// Ports of tests/integration/test_reconciliation_pipeline.py, then Go's own.

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// env is a fresh database (apitest seeds USD and SGD rates on 2026-01-01)
// and a service posting in IDR.
type env struct {
	pool  *pgxpool.Pool
	svc   Service
	admin uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := apitest.New(t, func(huma.API, apitest.Deps) {})
	return &env{pool: a.Pool, svc: Service{BaseCurrency: "IDR", Hosting: NoHosting{}}, admin: a.AdminID}
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// inTx runs fn in a transaction it commits when fn succeeds and rolls back
// when it fails, returning fn's error.
func inTx[T any](t *testing.T, e *env, fn func(tx pgx.Tx) (T, error)) (T, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fn(tx)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Fatal(rbErr)
		}
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return v, nil
}

func (e *env) customer(t *testing.T, name string, aliases ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if aliases == nil {
		aliases = []string{}
	}
	j, err := json.Marshal(aliases)
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `INSERT INTO customers (customer_id, name, matching_aliases) VALUES ($1, $2, $3::jsonb)`, id, name, string(j))
	return id
}

// inv is an invoice to insert; zero dates are NULL.
type inv struct {
	customer         uuid.UUID
	currency, status string
	balance, wht     string
	issue, due       time.Time
}

func (e *env) invoice(t *testing.T, v inv) uuid.UUID {
	t.Helper()
	id := uuid.New()
	date := func(d time.Time) any {
		if d.IsZero() {
			return nil
		}
		return d
	}
	wht := v.wht
	if wht == "" {
		wht = "0"
	}
	whtType := "NONE"
	if dec(wht).IsPositive() {
		whtType = "PPH23"
	}
	e.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status,
issue_date, due_date, wht_type, wht_amount, wht_booked)
VALUES ($1, $2, 'MILESTONE', $3, $4, $4, $5, $6, $7, $8, $9, 0)`,
		id, v.customer, v.currency, dec(v.balance), v.status, date(v.issue), date(v.due), whtType, dec(wht))
	return id
}

// sentInvoice is the pipeline tests' fixture: Acme Corp (alias Acme), a SENT
// USD 1000 invoice issued 2026-04-10, due 2026-04-24.
func (e *env) sentInvoice(t *testing.T) uuid.UUID {
	t.Helper()
	c := e.customer(t, "Acme Corp", "Acme")
	return e.invoice(t, inv{
		customer: c, currency: "USD", status: "SENT", balance: "1000.0000",
		issue: day(2026, 4, 10), due: day(2026, 4, 24),
	})
}

// payload is the pipeline tests' default payment, changed by opts.
func payload(opts ...func(*IntakePayload)) IntakePayload {
	ref := "REF-1"
	p := IntakePayload{
		Amount: dec("1000.0000"), Currency: "USD", PayerName: "Acme Corp", PayerReference: &ref,
		PaymentDate: day(2026, 4, 15), IntakeSource: "WEBHOOK", ExternalRef: "EXT-" + uuid.NewString(),
	}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func amount(s string) func(*IntakePayload)   { return func(p *IntakePayload) { p.Amount = dec(s) } }
func currency(c string) func(*IntakePayload) { return func(p *IntakePayload) { p.Currency = c } }
func payer(n string) func(*IntakePayload)    { return func(p *IntakePayload) { p.PayerName = n } }
func paid(d time.Time) func(*IntakePayload)  { return func(p *IntakePayload) { p.PaymentDate = d } }

func (e *env) incoming(t *testing.T, in IntakePayload) paymentsdb.Payment {
	t.Helper()
	p, err := inTx(t, e, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.ProcessIncoming(context.Background(), tx, in)
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) approve(t *testing.T, paymentID, invoiceID uuid.UUID) (paymentsdb.Payment, error) {
	t.Helper()
	return inTx(t, e, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.ApproveManual(context.Background(), tx, paymentID, invoiceID, &e.admin, "NONE")
	})
}

func (e *env) mustApprove(t *testing.T, paymentID, invoiceID uuid.UUID) paymentsdb.Payment {
	t.Helper()
	p, err := e.approve(t, paymentID, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) reverse(t *testing.T, paymentID uuid.UUID, reason string) (paymentsdb.Payment, error) {
	t.Helper()
	return inTx(t, e, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.Reverse(context.Background(), tx, paymentID, &e.admin, reason)
	})
}

func (e *env) mustReverse(t *testing.T, paymentID uuid.UUID, reason string) paymentsdb.Payment {
	t.Helper()
	p, err := e.reverse(t, paymentID, reason)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) payment(t *testing.T, id uuid.UUID) paymentsdb.Payment {
	t.Helper()
	p, err := paymentsdb.New(e.pool).GetPayment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type invState struct {
	balance, whtBooked decimal.Decimal
	status             string
}

func (e *env) invoiceState(t *testing.T, id uuid.UUID) invState {
	t.Helper()
	var s invState
	if err := e.pool.QueryRow(context.Background(), `SELECT balance_due, wht_booked, status FROM invoices WHERE invoice_id = $1`, id).
		Scan(&s.balance, &s.whtBooked, &s.status); err != nil {
		t.Fatal(err)
	}
	return s
}

func checkInvoice(t *testing.T, e *env, id uuid.UUID, balance, whtBooked, status string) {
	t.Helper()
	s := e.invoiceState(t, id)
	if !s.balance.Equal(dec(balance)) || !s.whtBooked.Equal(dec(whtBooked)) || s.status != status {
		t.Errorf("invoice = balance %s wht_booked %s %s, want %s %s %s", s.balance, s.whtBooked, s.status, balance, whtBooked, status)
	}
}

type jline struct {
	code, currency                             string
	debit, credit, baseDebit, baseCredit, rate decimal.Decimal
}

type jentry struct {
	id         uuid.UUID
	reversedBy *uuid.UUID
	memo       string
	lines      []jline
}

// line is the entry's line on the account with code, or nil.
func (je jentry) line(code string) *jline {
	for i := range je.lines {
		if je.lines[i].code == code {
			return &je.lines[i]
		}
	}
	return nil
}

func (je jentry) codes() []string {
	var cs []string
	for _, l := range je.lines {
		cs = append(cs, l.code)
	}
	slices.Sort(cs)
	return cs
}

// nets are the entry's debit − credit and base debit − base credit.
func (je jentry) nets() (net, base decimal.Decimal) {
	for _, l := range je.lines {
		net = net.Add(l.debit.Sub(l.credit))
		base = base.Add(l.baseDebit.Sub(l.baseCredit))
	}
	return net, base
}

// entries are the journal entries whose source is the payment, oldest first.
func (e *env) entries(t *testing.T, paymentID uuid.UUID) []jentry {
	t.Helper()
	ctx := context.Background()
	rows, err := e.pool.Query(ctx, `SELECT entry_id, reversed_by_entry_id, COALESCE(memo, '') FROM journal_entries
WHERE source_id = $1 ORDER BY posted_at, entry_id`, paymentID)
	if err != nil {
		t.Fatal(err)
	}
	var out []jentry
	for rows.Next() {
		var je jentry
		if err := rows.Scan(&je.id, &je.reversedBy, &je.memo); err != nil {
			t.Fatal(err)
		}
		out = append(out, je)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		lrows, err := e.pool.Query(ctx, `SELECT a.code, l.currency, l.debit, l.credit, l.base_amount_debit, l.base_amount_credit, l.fx_rate
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id WHERE l.entry_id = $1 ORDER BY a.code`, out[i].id)
		if err != nil {
			t.Fatal(err)
		}
		for lrows.Next() {
			var l jline
			if err := lrows.Scan(&l.code, &l.currency, &l.debit, &l.credit, &l.baseDebit, &l.baseCredit, &l.rate); err != nil {
				t.Fatal(err)
			}
			out[i].lines = append(out[i].lines, l)
		}
		if err := lrows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type logRow struct {
	action  string
	reasons []string
	actor   *uuid.UUID
}

func (e *env) logs(t *testing.T, paymentID uuid.UUID) []logRow {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT action, reasons, actor_user_id FROM reconciliation_log
WHERE payment_id = $1 ORDER BY created_at, log_id`, paymentID)
	if err != nil {
		t.Fatal(err)
	}
	var out []logRow
	for rows.Next() {
		var l logRow
		if err := rows.Scan(&l.action, &l.reasons, &l.actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func refusedWith(t *testing.T, err error, sentence string) {
	t.Helper()
	if !errors.Is(err, ErrPayment) || err.Error() != sentence {
		t.Fatalf("err = %v, want ErrPayment %q", err, sentence)
	}
}

func TestClearedPaymentPostsExactlyOneBalancedEntry(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload())
	if p.Status != "CLEARED" {
		t.Fatalf("status %s, want CLEARED", p.Status)
	}
	es := e.entries(t, p.PaymentID)
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1", len(es))
	}
	if net, _ := es[0].nets(); !net.IsZero() {
		t.Errorf("net %s, want 0", net)
	}
	checkInvoice(t, e, invID, "0", "0", "PAID")
}

func TestAmountMismatchHoldsAndPostsNoJournal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("999.00")))
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", p.Status)
	}
	if n := len(e.entries(t, p.PaymentID)); n != 0 {
		t.Errorf("%d entries, want 0", n)
	}
	checkInvoice(t, e, invID, "1000.0000", "0", "SENT")
}

func TestCurrencyMismatchHoldsRegardlessOfAmount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.sentInvoice(t)
	p := e.incoming(t, payload(currency("GBP")))
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", p.Status)
	}
	if n := len(e.entries(t, p.PaymentID)); n != 0 {
		t.Errorf("%d entries, want 0", n)
	}
}

func TestReconciliationLogWrittenForEveryPayment(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.sentInvoice(t)
	p := e.incoming(t, payload())
	logs := e.logs(t, p.PaymentID)
	if len(logs) != 1 || logs[0].action != "CLEARED" {
		t.Fatalf("logs = %+v, want one CLEARED", logs)
	}
	if logs[0].actor != nil || !slices.Equal(logs[0].reasons, []string{"exact amount+currency, high payer+date confidence"}) {
		t.Errorf("log = %+v", logs[0])
	}
}

func TestReversalCreatesBalancingEntryAndWiresLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload())
	got := e.mustReverse(t, p.PaymentID, "bank returned funds")

	es := e.entries(t, p.PaymentID)
	if len(es) != 2 {
		t.Fatalf("%d entries, want 2", len(es))
	}
	var original, reversal *jentry
	for i := range es {
		if es[i].reversedBy != nil {
			original = &es[i]
		} else {
			reversal = &es[i]
		}
	}
	if original == nil || reversal == nil || *original.reversedBy != reversal.id {
		t.Fatalf("entries not linked: %+v", es)
	}
	if reversal.memo != "Reversal: bank returned funds" {
		t.Errorf("memo %q", reversal.memo)
	}
	if got.Status != "FLAGGED" || e.payment(t, p.PaymentID).Status != "FLAGGED" {
		t.Errorf("payment status %s, want FLAGGED", got.Status)
	}
	checkInvoice(t, e, invID, "1000.0000", "0", "OPEN")
	logs := e.logs(t, p.PaymentID)
	if len(logs) != 2 || logs[1].action != "REVERSED" || !slices.Equal(logs[1].reasons, []string{"bank returned funds"}) ||
		logs[1].actor == nil || *logs[1].actor != e.admin {
		t.Errorf("logs = %+v", logs)
	}
}

func TestInvariantAllActiveEntriesBalanceToZero(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.sentInvoice(t)
	e.incoming(t, payload())
	rows, err := e.pool.Query(context.Background(), `SELECT l.entry_id, l.currency, l.debit - l.credit
FROM journal_lines l JOIN journal_entries j ON j.entry_id = l.entry_id WHERE j.reversed_by_entry_id IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	nets := map[string]decimal.Decimal{}
	for rows.Next() {
		var id uuid.UUID
		var ccy string
		var d decimal.Decimal
		if err := rows.Scan(&id, &ccy, &d); err != nil {
			t.Fatal(err)
		}
		k := id.String() + ccy
		nets[k] = nets[k].Add(d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(nets) == 0 {
		t.Fatal("no active entries")
	}
	for k, n := range nets {
		if !n.IsZero() {
			t.Errorf("unbalanced entry %s: net=%s", k, n)
		}
	}
}

// intakeIDR is an IDR payment with a weak payer name, so it cannot clear.
func intakeIDR(amt string) IntakePayload {
	ref := "REF-XCCY"
	return payload(amount(amt), currency("IDR"), payer("Unknown Payer"), func(p *IntakePayload) { p.PayerReference = &ref })
}

func TestCrossCurrencyManualApprovalBooksFxLoss(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	// USD 1000 at 16000 is 16,000,000 IDR; 15,000,000 received: a loss of 1,000,000.
	p := e.incoming(t, intakeIDR("15000000"))
	if p.Status == "CLEARED" {
		t.Fatal("cleared, want held")
	}
	e.mustApprove(t, p.PaymentID, invID)

	es := e.entries(t, p.PaymentID)
	if len(es) != 1 {
		t.Fatalf("%d entries", len(es))
	}
	if _, base := es[0].nets(); !base.IsZero() {
		t.Errorf("base net %s", base)
	}
	bank, ar, loss := es[0].line("1000"), es[0].line("1100"), es[0].line("5900")
	if bank == nil || ar == nil || loss == nil {
		t.Fatalf("lines %v", es[0].codes())
	}
	if es[0].line("4900") != nil {
		t.Error("FX gain should not be booked on a loss")
	}
	if bank.currency != "IDR" || !bank.debit.Equal(dec("15000000")) {
		t.Errorf("bank %+v", bank)
	}
	if ar.currency != "USD" || !ar.credit.Equal(dec("1000")) {
		t.Errorf("ar %+v", ar)
	}
	if loss.currency != "IDR" || !loss.baseDebit.Equal(dec("1000000")) {
		t.Errorf("fx loss %+v", loss)
	}
	checkInvoice(t, e, invID, "0", "0", "PAID")
}

func TestCrossCurrencyManualApprovalBooksFxGain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, intakeIDR("17000000"))
	if p.Status == "CLEARED" {
		t.Fatal("cleared, want held")
	}
	e.mustApprove(t, p.PaymentID, invID)

	es := e.entries(t, p.PaymentID)
	if len(es) != 1 {
		t.Fatalf("%d entries", len(es))
	}
	if _, base := es[0].nets(); !base.IsZero() {
		t.Errorf("base net %s", base)
	}
	gain := es[0].line("4900")
	if gain == nil || !gain.baseCredit.Equal(dec("1000000")) {
		t.Errorf("fx gain %+v", gain)
	}
	if es[0].line("5900") != nil {
		t.Error("FX loss should not be booked on a gain")
	}
	checkInvoice(t, e, invID, "0", "0", "PAID")
}

// Python's test meant to approve a held payment, but 1000.0001 is within the
// epsilon and clears, so it returned early; the clearing is checked here and
// TestApproveFlaggedPayment and the cross-currency tests cover approval.
func TestManualApprovalClearsAndPosts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("1000.0001")))
	if p.Status != "CLEARED" {
		p = e.mustApprove(t, p.PaymentID, invID)
	}
	if p.Status != "CLEARED" {
		t.Fatalf("status %s", p.Status)
	}
	if n := len(e.entries(t, p.PaymentID)); n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
}

// Go's own.

func TestHeldPaymentLeavesInvoiceAndLedger(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := &recorder{}
	e.svc.Hosting = rec
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("999.00")))

	if p.Status != "PENDING_MANUAL_REVIEW" || p.AdjustmentType != "PARTIAL_PAY" || !p.WhtAmount.IsZero() {
		t.Errorf("payment %s %s wht %s", p.Status, p.AdjustmentType, p.WhtAmount)
	}
	// The best guess stays attached.
	if !p.InvoiceID.Valid || p.InvoiceID.Bytes != invID || !p.CustomerID.Valid {
		t.Errorf("invoice %v customer %v, want the best guess", p.InvoiceID, p.CustomerID)
	}
	if !p.ConfidenceScore.Valid || !p.ConfidenceScore.Decimal.Equal(dec("46.43")) {
		t.Errorf("confidence %v, want 46.43", p.ConfidenceScore)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries, want 0", n)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_lines`); n != 0 {
		t.Errorf("%d journal lines, want 0", n)
	}
	checkInvoice(t, e, invID, "1000.0000", "0", "SENT")
	logs := e.logs(t, p.PaymentID)
	want := []string{"amount mismatch: delta=-1.0000", "confidence 46.4286 below threshold 95.0"}
	if len(logs) != 1 || logs[0].action != "HELD" || logs[0].actor != nil || !slices.Equal(logs[0].reasons, want) {
		t.Errorf("logs = %+v, want one HELD %q", logs, want)
	}
	if got := rec.got(); len(got) != 0 {
		t.Errorf("hosting restored %v for a held payment", got)
	}
}

// rateless is an invoice dated before apitest's first USD rate (2026-01-01),
// with withheld tax wht ("0" for none) of its balance.
func (e *env) rateless(t *testing.T, balance, wht string) uuid.UUID {
	t.Helper()
	c := e.customer(t, "Acme Corp")
	return e.invoice(t, inv{
		customer: c, currency: "USD", status: "SENT", balance: balance, wht: wht,
		issue: day(2025, 11, 28), due: day(2025, 12, 12),
	})
}

func TestIncomingClearWithoutRateIsHeld(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := &recorder{}
	e.svc.Hosting = rec
	// The net payment clears, so the clearing writes the payment's
	// wht_amount before the posting fails: the savepoint must undo it.
	invID := e.rateless(t, "1020.41", "20.41")
	p := e.incoming(t, payload(amount("1000.00"), paid(day(2025, 12, 1))))

	if p.Status != "PENDING_MANUAL_REVIEW" || !p.WhtAmount.IsZero() || !p.InvoiceID.Valid || p.InvoiceID.Bytes != invID {
		t.Errorf("payment %s wht %s invoice %v", p.Status, p.WhtAmount, p.InvoiceID)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries, want 0", n)
	}
	checkInvoice(t, e, invID, "1020.41", "0", "SENT")
	logs := e.logs(t, p.PaymentID)
	want := []string{"exact amount+currency, high payer+date confidence", "no fx rate for USD->IDR on or before 2025-12-01"}
	if len(logs) != 1 || logs[0].action != "HELD" || !slices.Equal(logs[0].reasons, want) {
		t.Errorf("logs = %+v, want one HELD %q", logs, want)
	}
	if got := rec.got(); len(got) != 0 {
		t.Errorf("hosting restored %v for a payment held at the savepoint", got)
	}
}

func TestIncomingDuplicateRef(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.sentInvoice(t)
	in := payload(amount("5"))
	e.incoming(t, in)
	_, err := inTx(t, e, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.ProcessIncoming(context.Background(), tx, in)
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	if n := e.count(t, `SELECT count(*) FROM payments`); n != 1 {
		t.Errorf("%d payments, want 1", n)
	}
	// Another source with the same ref is not a duplicate.
	in.IntakeSource = "EMAIL_PARSER"
	e.incoming(t, in)
}

func TestApproveDraftOrVoidInvoiceIs400(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.customer(t, "Acme Corp")
	p := e.incoming(t, payload(payer("Nobody")))
	for _, status := range []string{"DRAFT", "VOID"} {
		invID := e.invoice(t, inv{customer: c, currency: "USD", status: status, balance: "1000"})
		_, err := e.approve(t, p.PaymentID, invID)
		refusedWith(t, err, fmt.Sprintf("cannot approve payment against %s invoice", status))
		checkInvoice(t, e, invID, "1000", "0", status)
	}
	if got := e.payment(t, p.PaymentID); got.Status != "PENDING_MANUAL_REVIEW" || got.InvoiceID.Valid {
		t.Errorf("payment %s invoice %v, want untouched", got.Status, got.InvoiceID)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries, want 0", n)
	}
}

func TestApproveRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	unknown := uuid.New()
	_, err := e.approve(t, unknown, invID)
	refusedWith(t, err, fmt.Sprintf("payment %s not found", unknown))

	held := e.incoming(t, payload(amount("1")))
	_, err = e.approve(t, held.PaymentID, unknown)
	refusedWith(t, err, fmt.Sprintf("invoice %s not found", unknown))

	cleared := e.incoming(t, payload())
	_, err = e.approve(t, cleared.PaymentID, invID)
	refusedWith(t, err, fmt.Sprintf("payment %s already CLEARED", cleared.PaymentID))
}

// A FLAGGED (reversed) payment can be approved again, as in Python.
func TestApproveFlaggedPayment(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload())
	e.mustReverse(t, p.PaymentID, "bounced")
	got := e.mustApprove(t, p.PaymentID, invID)
	if got.Status != "CLEARED" || got.AdjustmentType != "NONE" {
		t.Errorf("payment %s %s", got.Status, got.AdjustmentType)
	}
	checkInvoice(t, e, invID, "0", "0", "PAID")
	logs := e.logs(t, p.PaymentID)
	if len(logs) != 3 || logs[2].action != "OVERRIDDEN" ||
		!slices.Equal(logs[2].reasons, []string{"manual approval against invoice " + invID.String()}) ||
		logs[2].actor == nil || *logs[2].actor != e.admin {
		t.Errorf("logs = %+v", logs)
	}
	es := e.entries(t, p.PaymentID)
	active := 0
	for _, je := range es {
		if je.reversedBy == nil && !strings.HasPrefix(je.memo, "Reversal") {
			active++
		}
	}
	if len(es) != 3 || active != 1 {
		t.Errorf("%d entries, %d active clearings; want 3, 1", len(es), active)
	}
}

// waitBlocked waits until the backend pid waits on a lock.
func waitBlocked(t *testing.T, pool *pgxpool.Pool, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := pool.QueryRow(context.Background(),
			`SELECT COALESCE(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, int32(pid)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("second transaction never blocked")
}

// race runs first in one transaction and, while it is open, second in
// another on its own connection; second must block on a lock, and runs on
// once first commits. second's transaction is committed when it succeeds.
func race[T any](t *testing.T, e *env, first, second func(tx pgx.Tx) (T, error)) (T, error) {
	t.Helper()
	ctx := context.Background()
	tx1, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := first(tx1); err != nil {
		t.Fatal(err)
	}
	tx2, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := second(tx2)
		if err == nil {
			err = tx2.Commit(ctx)
		} else {
			_ = tx2.Rollback(ctx)
		}
		done <- result{v, err}
	}()
	waitBlocked(t, e.pool, tx2.Conn().PgConn().PID())
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	return r.v, r.err
}

func TestApproveTwiceConcurrentPostsOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("400")))
	approve := func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.ApproveManual(context.Background(), tx, p.PaymentID, invID, &e.admin, "NONE")
	}
	_, err := race(t, e, approve, approve)
	refusedWith(t, err, fmt.Sprintf("payment %s already CLEARED", p.PaymentID))
	if n := len(e.entries(t, p.PaymentID)); n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
	checkInvoice(t, e, invID, "600", "0", "PARTIAL")
}

// Two payments that each clear the same invoice arrive at once: the second,
// reading the invoice again under its lock, finds it PAID and is held.
func TestIncomingConcurrentSameInvoiceClearsOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := &recorder{}
	e.svc.Hosting = rec
	invID := e.sentInvoice(t)
	process := func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return e.svc.ProcessIncoming(context.Background(), tx, payload())
	}
	second, err := race(t, e, process, process)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("second payment %s, want held", second.Status)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 1 {
		t.Errorf("%d journal entries, want 1", n)
	}
	checkInvoice(t, e, invID, "0", "0", "PAID")
	logs := e.logs(t, second.PaymentID)
	want := []string{"exact amount+currency, high payer+date confidence", "invoice " + invID.String() + " changed before clearing"}
	if len(logs) != 1 || logs[0].action != "HELD" || !slices.Equal(logs[0].reasons, want) {
		t.Errorf("logs = %+v, want one HELD %q", logs, want)
	}
	// Only the first payment's clearing restores hosting.
	if got := rec.got(); !slices.Equal(got, []uuid.UUID{invID}) {
		t.Errorf("hosting restored %v, want [%s] once", got, invID)
	}
}

// approve → reverse → approve → reverse undoes the second clearing, never
// the first reversal, and leaves the invoice as it was.
func TestApproveReverseTwiceUndoesLatestClearing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("999")))
	e.mustApprove(t, p.PaymentID, invID)
	checkInvoice(t, e, invID, "1", "0", "PARTIAL")
	e.mustReverse(t, p.PaymentID, "first")
	e.mustApprove(t, p.PaymentID, invID)
	e.mustReverse(t, p.PaymentID, "second")

	es := e.entries(t, p.PaymentID)
	if len(es) != 4 {
		t.Fatalf("%d entries, want 4", len(es))
	}
	byID := map[uuid.UUID]jentry{}
	for _, je := range es {
		byID[je.id] = je
	}
	// Oldest first: clearing, its reversal, clearing, its reversal.
	wantMemo := []string{"Payment ", "Reversal: first", "Payment ", "Reversal: second"}
	for i, je := range es {
		if !strings.HasPrefix(je.memo, wantMemo[i]) {
			t.Errorf("entry %d memo %q, want %q…", i, je.memo, wantMemo[i])
		}
	}
	for _, i := range []int{0, 2} {
		if es[i].reversedBy == nil || *es[i].reversedBy != es[i+1].id {
			t.Errorf("clearing %d reversed by %v, want %s", i, es[i].reversedBy, es[i+1].id)
		}
		if es[i+1].reversedBy != nil {
			t.Errorf("reversal %d is itself reversed", i+1)
		}
	}
	total := map[string]decimal.Decimal{}
	for _, je := range es {
		for _, l := range je.lines {
			total[l.code] = total[l.code].Add(l.debit.Sub(l.credit))
		}
	}
	for code, n := range total {
		if !n.IsZero() {
			t.Errorf("account %s nets %s over the four entries, want 0", code, n)
		}
	}
	checkInvoice(t, e, invID, "1000", "0", "OPEN")
	if got := e.payment(t, p.PaymentID); got.Status != "FLAGGED" {
		t.Errorf("payment %s", got.Status)
	}
}

func TestReverseManualPaymentHasNoEntry(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	id := uuid.New()
	e.exec(t, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source,
external_ref, status) VALUES ($1, $2, 400, 'USD', '(manual entry)', '2026-04-15', 'MANUAL', 'manual-0123456789ab', 'CLEARED')`, id, invID)
	_, err := e.reverse(t, id, "oops")
	refusedWith(t, err, fmt.Sprintf("no active journal entry to reverse for payment %s", id))
	if got := e.payment(t, id); got.Status != "CLEARED" {
		t.Errorf("payment %s", got.Status)
	}
}

func TestReverseNonClearedIs400(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.sentInvoice(t)
	unknown := uuid.New()
	_, err := e.reverse(t, unknown, "x")
	refusedWith(t, err, fmt.Sprintf("payment %s not found", unknown))

	held := e.incoming(t, payload(amount("1")))
	_, err = e.reverse(t, held.PaymentID, "x")
	refusedWith(t, err, "only CLEARED payments can be reversed (got PENDING_MANUAL_REVIEW)")

	cleared := e.incoming(t, payload())
	e.mustReverse(t, cleared.PaymentID, "x")
	_, err = e.reverse(t, cleared.PaymentID, "x")
	refusedWith(t, err, "only CLEARED payments can be reversed (got FLAGGED)")
}

func TestApproveMissingRateIs400Sentence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.rateless(t, "1000", "0")
	p := e.incoming(t, payload(amount("999"), paid(day(2025, 12, 1))))
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", p.Status)
	}
	_, err := e.approve(t, p.PaymentID, invID)
	if !errors.Is(err, fx.ErrRateMissing) || err.Error() != "no fx rate for USD->IDR on or before 2025-12-01" {
		t.Fatalf("err = %v, want the missing-rate sentence", err)
	}
	if errors.Is(err, ErrPayment) || errors.Is(err, ledger.ErrLedger) {
		t.Errorf("err %v also matches a refusal", err)
	}
	checkInvoice(t, e, invID, "1000", "0", "SENT")
	if got := e.payment(t, p.PaymentID); got.Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("payment %s", got.Status)
	}
}

// recorder is a HostingRestorer that records the invoices it is told of.
type recorder struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (r *recorder) RestoreIfPaid(_ context.Context, _ pgx.Tx, invoiceID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, invoiceID)
	return nil
}

func (r *recorder) got() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ids)
}

func TestHostingRestorerCalledOnPaid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := &recorder{}
	e.svc.Hosting = rec
	auto := e.sentInvoice(t)
	e.incoming(t, payload())
	if got := rec.got(); !slices.Equal(got, []uuid.UUID{auto}) {
		t.Fatalf("restored %v after auto-clear, want [%s]", got, auto)
	}

	manual := e.sentInvoice(t)
	part := e.incoming(t, payload(amount("400"), payer("Nobody")))
	e.mustApprove(t, part.PaymentID, manual) // PARTIAL: not restored
	if got := rec.got(); len(got) != 1 {
		t.Fatalf("restored %v after a partial approval", got)
	}
	rest := e.incoming(t, payload(amount("600"), payer("Nobody")))
	e.mustApprove(t, rest.PaymentID, manual)
	if got := rec.got(); !slices.Equal(got, []uuid.UUID{auto, manual}) {
		t.Errorf("restored %v, want [%s %s]", got, auto, manual)
	}
}

func TestPaymentOutFrom(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload())
	b, err := json.Marshal(PaymentOutFrom(p))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	jd := json.NewDecoder(strings.NewReader(string(b)))
	if _, err := jd.Token(); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for jd.More() {
		k, err := jd.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, _ := k.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := jd.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	wantKeys := []string{
		"payment_id", "invoice_id", "customer_id", "amount", "wht_amount", "currency", "payer_name",
		"payer_reference", "payment_date", "intake_source", "external_ref", "status", "adjustment_type",
		"confidence_score", "created_at",
	}
	if !slices.Equal(keys, wantKeys) {
		t.Errorf("keys %v, want %v", keys, wantKeys)
	}
	want := map[string]any{
		"invoice_id": invID.String(), "amount": "1000.0000", "wht_amount": "0.0000", "currency": "USD",
		"payment_date": "2026-04-15", "status": "CLEARED", "adjustment_type": "NONE", "confidence_score": "96.43",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("%s = %v, want %v", k, fields[k], v)
		}
	}
	if s, _ := fields["created_at"].(string); !strings.HasSuffix(s, "Z") {
		t.Errorf("created_at %v", fields["created_at"])
	}
	// A held payment with no candidate prints confidence 0.00.
	none := e.incoming(t, payload(currency("GBP")))
	nb, err := json.Marshal(PaymentOutFrom(none))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nb), `"confidence_score":"0.00"`) {
		t.Errorf("held payment JSON %s, want confidence_score 0.00", nb)
	}
}

// Two identical open invoices of one customer both clear the payment: it is
// held with the ambiguity reason and nothing changes.
func TestIncomingAmbiguousIsHeld(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := &recorder{}
	e.svc.Hosting = rec
	c := e.customer(t, "Acme Corp", "Acme")
	var ids []uuid.UUID
	for range 2 {
		ids = append(ids, e.invoice(t, inv{
			customer: c, currency: "USD", status: "SENT", balance: "1000.0000",
			issue: day(2026, 4, 10), due: day(2026, 4, 24),
		}))
	}
	p := e.incoming(t, payload())
	if p.Status != "PENDING_MANUAL_REVIEW" || !p.InvoiceID.Valid || !slices.Contains(ids, uuid.UUID(p.InvoiceID.Bytes)) {
		t.Errorf("payment %s invoice %v, want held with one of %v", p.Status, p.InvoiceID, ids)
	}
	logs := e.logs(t, p.PaymentID)
	want := []string{"ambiguous match: 2 open invoices match exactly"}
	if len(logs) != 1 || logs[0].action != "HELD" || !slices.Equal(logs[0].reasons, want) {
		t.Errorf("logs = %+v, want one HELD %q", logs, want)
	}
	for _, id := range ids {
		checkInvoice(t, e, id, "1000.0000", "0", "SENT")
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries, want 0", n)
	}
	if got := rec.got(); len(got) != 0 {
		t.Errorf("hosting restored %v", got)
	}
}

// Inserting a payment takes FOR KEY SHARE on its invoice (the foreign key).
// Intake's invoice lock must not wait on that, or two intakes for one invoice
// deadlock (insert, insert, lock, lock) and one is lost.
func TestIncomingLockCoexistsWithKeyShare(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.sentInvoice(t)
	ctx := context.Background()
	holder, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM invoices WHERE invoice_id = $1 FOR KEY SHARE`, invID); err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := e.svc.ProcessIncoming(tctx, tx, payload())
	if err != nil {
		t.Fatalf("intake blocked behind a key-share lock: %v", err)
	}
	if p.Status != "CLEARED" {
		t.Errorf("status %s, want CLEARED", p.Status)
	}
}
