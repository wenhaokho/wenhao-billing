package backpost

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgertest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
)

// Go's own (PORT-NOTES 166): `api backpost-ledger` posts the issue entries
// of documents issued before accrual posting, and restates Python-era
// payments at the invoice's issue basis. The fixtures are Python-shaped:
// rows inserted by SQL, with no issue entries and Python's PAYMENT entries
// (receivable relieved at the payment date's rate).

const base = "IDR"

// Issue dates use the 2025 rate (15000); payments in 2026 use apitest's
// 16000 (2026-01-01).
var (
	issued2025 = day(2025, 6, 10)
	paid2026   = day(2026, 2, 1)
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type env struct {
	pool *pgxpool.Pool
	app  *apitest.App
}

// newEnv is a fresh database with the payment routes, apitest's rates and a
// USD rate of 15000 from 2025-01-01.
func newEnv(t *testing.T) *env {
	t.Helper()
	svc := payments.Service{BaseCurrency: base, Hosting: payments.NoHosting{}}
	a := apitest.New(t, func(h huma.API, d apitest.Deps) {
		payments.Register(h, d.Pool, svc, d.Config, d.Clock, payments.ReceiptDeps{Sender: d.Mail, Renderer: payments.StubRenderer{}})
	})
	e := &env{pool: a.Pool, app: a}
	e.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 15000, '2025-01-01', 'TEST')`)
	return e
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// doc is a document row to insert; a zero issue date is NULL.
type doc struct {
	number, currency, status string
	amount, balance          string
	issue                    time.Time
	invoiceType              string
	template                 bool
}

func (e *env) invoice(t *testing.T, d doc) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if d.balance == "" {
		d.balance = d.amount
	}
	if d.invoiceType == "" {
		d.invoiceType = "MILESTONE"
	}
	var issue any
	if !d.issue.IsZero() {
		issue = d.issue
	}
	e.exec(t, `INSERT INTO invoices (invoice_id, invoice_number, invoice_type, currency, amount, balance_due, status, issue_date, is_template, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '2026-03-02 08:00')`,
		id, d.number, d.invoiceType, d.currency, dec(d.amount), dec(d.balance), d.status, issue, d.template)
	return id
}

func (e *env) bill(t *testing.T, d doc) uuid.UUID {
	t.Helper()
	id, vendor := uuid.New(), uuid.New()
	if d.balance == "" {
		d.balance = d.amount
	}
	var issue any
	if !d.issue.IsZero() {
		issue = d.issue
	}
	e.exec(t, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'Supplier')`, vendor)
	e.exec(t, `INSERT INTO bills (bill_id, vendor_id, bill_number, currency, amount, balance_due, status, issue_date, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '2026-03-02 08:00')`,
		id, vendor, d.number, d.currency, dec(d.amount), dec(d.balance), d.status, issue)
	return id
}

// pythonPayment stores a CLEARED payment of amount against inv on the day
// on, created at on 12:00. With rate set it also stores Python's entry,
// posted at on 10:00: DR bank / CR receivable, both at rate.
func (e *env) pythonPayment(t *testing.T, inv uuid.UUID, currency, amount string, on time.Time, rate string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status, created_at)
VALUES ($1, $2, $3, $4, 'Acme', $5, 'MANUAL', 'CLEARED', $6)`, id, inv, dec(amount), currency, on, on.Add(12*time.Hour))
	if rate == "" {
		return id
	}
	entry := uuid.New()
	baseAmt := dec(amount).Mul(dec(rate))
	e.exec(t, `INSERT INTO journal_entries (entry_id, posted_at, source_type, source_id, memo) VALUES ($1, $2, 'PAYMENT', $3, $4)`,
		entry, on.Add(10*time.Hour), id, "Payment "+id.String()+" cleared against invoice "+inv.String())
	for _, l := range []struct {
		code  string
		debit bool
	}{{"1000", true}, {"1100", false}} {
		dr, cr := dec(amount), decimal.Zero
		bdr, bcr := baseAmt, decimal.Zero
		if !l.debit {
			dr, cr, bdr, bcr = cr, dr, bcr, bdr
		}
		e.exec(t, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency, base_amount_debit, base_amount_credit, fx_rate)
VALUES ($1, $2, (SELECT account_id FROM chart_of_accounts WHERE code = $3), $4, $5, $6, $7, $8, $9)`,
			uuid.New(), entry, l.code, dr, cr, currency, bdr, bcr, dec(rate))
	}
	return id
}

// run runs the back-post and returns its report, output and error.
func (e *env) run(t *testing.T, apply bool) (Report, string, error) {
	t.Helper()
	var out bytes.Buffer
	rep, err := Run(context.Background(), e.pool, base, apply, &out)
	return rep, out.String(), err
}

func (e *env) mustRun(t *testing.T, apply bool) (Report, string) {
	t.Helper()
	rep, out, err := e.run(t, apply)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	return rep, out
}

// rows counts every row the back-post can write.
func (e *env) rows(t *testing.T) [3]int {
	t.Helper()
	return [3]int{
		e.count(t, `SELECT COUNT(*) FROM journal_entries`),
		e.count(t, `SELECT COUNT(*) FROM journal_lines`),
		e.count(t, `SELECT COUNT(*) FROM bill_payments`),
	}
}

func (e *env) inTx(t *testing.T, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
}

func (e *env) invoiceBasis(t *testing.T, id uuid.UUID) ledger.Basis {
	t.Helper()
	var b ledger.Basis
	e.inTx(t, func(tx pgx.Tx) {
		var err error
		if b, err = ledger.InvoiceBasis(context.Background(), tx, id); err != nil {
			t.Fatal(err)
		}
	})
	return b
}

func (e *env) billBasis(t *testing.T, id uuid.UUID) ledger.Basis {
	t.Helper()
	var b ledger.Basis
	e.inTx(t, func(tx pgx.Tx) {
		var err error
		if b, err = ledger.BillBasis(context.Background(), tx, id); err != nil {
			t.Fatal(err)
		}
	})
	return b
}

func (e *env) activeEntry(t *testing.T, sourceType string, id uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var entry uuid.UUID
	var ok bool
	e.inTx(t, func(tx pgx.Tx) {
		var err error
		if entry, ok, err = ledger.ActiveEntryFor(context.Background(), tx, sourceType, id); err != nil {
			t.Fatal(err)
		}
	})
	return entry, ok
}

func (e *env) postedAt(t *testing.T, entry uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT posted_at FROM journal_entries WHERE entry_id = $1`, entry).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// accountBase is the entry's base debit and credit on the account code.
func (e *env) accountBase(t *testing.T, entry uuid.UUID, code string) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	var dr, cr decimal.Decimal
	if err := e.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(l.base_amount_debit), 0), COALESCE(SUM(l.base_amount_credit), 0)
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id WHERE l.entry_id = $1 AND a.code = $2`, entry, code).Scan(&dr, &cr); err != nil {
		t.Fatal(err)
	}
	return dr, cr
}

func (e *env) trialBalanced(t *testing.T) {
	t.Helper()
	var dr, cr decimal.Decimal
	if err := e.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(base_amount_debit), 0), COALESCE(SUM(base_amount_credit), 0) FROM journal_lines`).Scan(&dr, &cr); err != nil {
		t.Fatal(err)
	}
	if !dr.Equal(cr) {
		t.Fatalf("trial balance: debit %s, credit %s", dr, cr)
	}
}

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// fixtures is a mix of every kind of candidate.
func (e *env) fixtures(t *testing.T) {
	t.Helper()
	usd := e.invoice(t, doc{number: "INV-USD", currency: "USD", status: "PAID", amount: "1000", balance: "0", issue: issued2025})
	e.pythonPayment(t, usd, "USD", "1000", paid2026, "16000")
	idr := e.invoice(t, doc{number: "INV-IDR", currency: "IDR", status: "PARTIAL", amount: "1000000", balance: "600000", issue: paid2026})
	e.pythonPayment(t, idr, "IDR", "400000", paid2026, "")
	e.bill(t, doc{number: "B-USD", currency: "USD", status: "PARTIAL", amount: "1000", balance: "300", issue: issued2025})
}

func TestBackpostDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	before := e.rows(t)
	rep, out := e.mustRun(t, false)
	if rep.Invoices != 2 || rep.Bills != 1 || rep.PaymentsRestated != 1 || rep.PaymentsPosted != 1 || rep.LegacyBillPayments != 1 {
		t.Errorf("report = %+v", rep)
	}
	if len(rep.Totals) == 0 {
		t.Error("dry run printed no totals")
	}
	if !strings.Contains(out, "dry run: nothing written") {
		t.Errorf("output lacks the dry-run line:\n%s", out)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows (entries, lines, bill payments) = %v, want %v", after, before)
	}
}

func TestBackpostPostsIssuedInvoicesAndBills(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	idr := e.invoice(t, doc{number: "INV-1", currency: "IDR", status: "OPEN", amount: "1000000", issue: paid2026})
	usd := e.invoice(t, doc{number: "INV-2", currency: "USD", status: "SENT", amount: "1000", issue: issued2025, invoiceType: "RECURRING"})
	bIDR := e.bill(t, doc{number: "B-1", currency: "IDR", status: "OPEN", amount: "500000", issue: paid2026})
	bUSD := e.bill(t, doc{number: "B-2", currency: "USD", status: "OPEN", amount: "100"})

	rep, out := e.mustRun(t, true)
	if rep.Invoices != 2 || rep.Bills != 2 || rep.LegacyBillPayments != 0 || rep.PaymentsRestated != 0 || rep.PaymentsPosted != 0 {
		t.Errorf("report = %+v", rep)
	}
	for _, c := range []struct {
		source string
		id     uuid.UUID
		at     time.Time
		code   string
		dr, cr string
	}{
		{ledger.SourceInvoice, idr, paid2026, "1100", "1000000", "0"},
		{ledger.SourceInvoice, usd, issued2025, "1100", "15000000", "0"},
		{ledger.SourceInvoice, usd, issued2025, "4100", "0", "15000000"},
		{ledger.SourceBill, bIDR, paid2026, "2000", "0", "500000"},
		// No issue date: the created_at date (2026-03-02), at 16000.
		{ledger.SourceBill, bUSD, day(2026, 3, 2), "6000", "1600000", "0"},
	} {
		entry, ok := e.activeEntry(t, c.source, c.id)
		if !ok {
			t.Fatalf("%s %s has no active entry", c.source, c.id)
		}
		if at := e.postedAt(t, entry); !at.Equal(c.at) {
			t.Errorf("%s %s posted_at = %s, want %s", c.source, c.id, at, c.at)
		}
		dr, cr := e.accountBase(t, entry, c.code)
		eq(t, c.source+" "+c.code+" base debit", dr, c.dr)
		eq(t, c.source+" "+c.code+" base credit", cr, c.cr)
	}
	totals := map[string]AccountTotal{}
	for _, a := range rep.Totals {
		totals[a.Code] = a
	}
	eq(t, "total 1100 debit", totals["1100"].Debit, "16000000")
	eq(t, "total 2000 credit", totals["2000"].Credit, "2100000")
	if !strings.Contains(out, "BILL B-2: no issue date; created_at date 2026-03-02 used") {
		t.Errorf("output lacks the null issue date warning:\n%s", out)
	}
	if !strings.Contains(out, "applied") {
		t.Errorf("output lacks the applied line:\n%s", out)
	}
	e.trialBalanced(t)
}

func TestBackpostRestatesForeignPayments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	usd := e.invoice(t, doc{number: "INV-USD", currency: "USD", status: "PAID", amount: "1000", balance: "0", issue: issued2025})
	pay := e.pythonPayment(t, usd, "USD", "1000", paid2026, "16000")
	orig, _ := e.activeEntry(t, "PAYMENT", pay)

	rep, out := e.mustRun(t, true)
	if rep.PaymentsRestated != 1 || rep.PaymentsPosted != 0 {
		t.Errorf("report = %+v", rep)
	}
	b := e.invoiceBasis(t, usd)
	eq(t, "AR face", b.Face, "0")
	eq(t, "AR base", b.Base, "0")

	if n := e.count(t, `SELECT COUNT(*) FROM journal_entries WHERE entry_id = $1 AND reversed_by_entry_id IS NOT NULL`, orig); n != 1 {
		t.Fatal("Python's entry is not reversed")
	}
	var reversal uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT reversed_by_entry_id FROM journal_entries WHERE entry_id = $1`, orig).Scan(&reversal); err != nil {
		t.Fatal(err)
	}
	keep := paid2026.Add(10 * time.Hour)
	if at := e.postedAt(t, reversal); !at.Equal(keep) {
		t.Errorf("reversal posted_at = %s, want %s", at, keep)
	}
	restated, ok := e.activeEntry(t, "PAYMENT", pay)
	if !ok || restated == orig {
		t.Fatal("no restated entry")
	}
	if at := e.postedAt(t, restated); !at.Equal(keep) {
		t.Errorf("restated posted_at = %s, want %s", at, keep)
	}
	var memo string
	if err := e.pool.QueryRow(context.Background(), `SELECT memo FROM journal_entries WHERE entry_id = $1`, restated).Scan(&memo); err != nil {
		t.Fatal(err)
	}
	if want := "Payment " + pay.String() + " cleared against invoice " + usd.String() + " (restated at issue basis)"; memo != want {
		t.Errorf("memo = %q, want %q", memo, want)
	}
	_, ar := e.accountBase(t, restated, "1100")
	eq(t, "restated AR base credit", ar, "15000000")
	dr, _ := e.accountBase(t, restated, "1000")
	eq(t, "restated bank base debit", dr, "16000000")
	_, gain := e.accountBase(t, restated, "4900")
	eq(t, "4900 credit", gain, "1000000")
	if strings.Contains(out, "ledger face") {
		t.Errorf("unexpected face mismatch warning:\n%s", out)
	}
	e.trialBalanced(t)
}

func TestBackpostKeepsBaseCurrencyPaymentEntries(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	idr := e.invoice(t, doc{number: "INV-IDR", currency: "IDR", status: "PAID", amount: "1000000", balance: "0", issue: paid2026})
	pay := e.pythonPayment(t, idr, "IDR", "1000000", paid2026, "1")
	orig, _ := e.activeEntry(t, "PAYMENT", pay)

	rep, _ := e.mustRun(t, true)
	if rep.Invoices != 1 || rep.PaymentsRestated != 0 || rep.PaymentsPosted != 0 {
		t.Errorf("report = %+v", rep)
	}
	if got, ok := e.activeEntry(t, "PAYMENT", pay); !ok || got != orig {
		t.Errorf("active payment entry = %s (%v), want Python's %s", got, ok, orig)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM journal_entries WHERE source_type = 'PAYMENT'`); n != 1 {
		t.Errorf("PAYMENT entries = %d, want 1", n)
	}
	b := e.invoiceBasis(t, idr)
	eq(t, "AR face", b.Face, "0")
	eq(t, "AR base", b.Base, "0")
	e.trialBalanced(t)
}

func TestBackpostPostsPythonManualPayments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	usd := e.invoice(t, doc{number: "INV-USD", currency: "USD", status: "PARTIAL", amount: "1000", balance: "600", issue: issued2025})
	usdPay := e.pythonPayment(t, usd, "USD", "400", paid2026, "")
	idr := e.invoice(t, doc{number: "INV-IDR", currency: "IDR", status: "PARTIAL", amount: "1000000", balance: "750000", issue: paid2026})
	idrPay := e.pythonPayment(t, idr, "IDR", "250000", paid2026, "")

	rep, _ := e.mustRun(t, true)
	if rep.PaymentsPosted != 2 || rep.PaymentsRestated != 0 {
		t.Errorf("report = %+v", rep)
	}
	for _, p := range []uuid.UUID{usdPay, idrPay} {
		entry, ok := e.activeEntry(t, "PAYMENT", p)
		if !ok {
			t.Fatalf("payment %s has no entry", p)
		}
		if at, want := e.postedAt(t, entry), paid2026; !at.Equal(want) {
			t.Errorf("payment %s posted_at = %s, want its payment date %s (PORT-NOTES 167)", p, at, want)
		}
	}
	b := e.invoiceBasis(t, usd)
	eq(t, "USD AR face", b.Face, "600")
	eq(t, "USD AR base", b.Base, "9000000")
	e.trialBalanced(t)

	// The payment is now reversible like any Go payment.
	rec := e.app.Do(t, http.MethodPost, "/api/v1/payments/"+usdPay.String()+"/reverse", map[string]any{"reason": "wrong payer"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reverse: %d %s", rec.Code, rec.Body)
	}
	b = e.invoiceBasis(t, usd)
	eq(t, "USD AR face after reverse", b.Face, "1000")
	eq(t, "USD AR base after reverse", b.Base, "15000000")
	e.trialBalanced(t)
}

func TestBackpostLegacyBillPayment(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	partial := e.bill(t, doc{number: "B-USD", currency: "USD", status: "PARTIAL", amount: "1000", balance: "300", issue: issued2025})
	paid := e.bill(t, doc{number: "B-IDR", currency: "IDR", status: "PAID", amount: "500000", balance: "0", issue: paid2026})
	open := e.bill(t, doc{number: "B-OPEN", currency: "IDR", status: "OPEN", amount: "200000", issue: paid2026})

	rep, out := e.mustRun(t, true)
	if rep.Bills != 3 || rep.LegacyBillPayments != 2 {
		t.Errorf("report = %+v", rep)
	}
	for _, c := range []struct {
		id     uuid.UUID
		amount string
		on     time.Time
		face   string
	}{{partial, "700", issued2025, "300"}, {paid, "500000", paid2026, "0"}} {
		var id uuid.UUID
		var amount decimal.Decimal
		var on time.Time
		var notes, source, ccy string
		if err := e.pool.QueryRow(context.Background(), `SELECT bill_payment_id, amount, currency, payment_date, notes, source FROM bill_payments WHERE bill_id = $1`, c.id).
			Scan(&id, &amount, &ccy, &on, &notes, &source); err != nil {
			t.Fatal(err)
		}
		eq(t, "legacy amount", amount, c.amount)
		if !on.Equal(c.on) || source != "LEGACY_BACKPOST" || notes != "paid before the switch; Python kept no payment date" {
			t.Errorf("legacy payment = %s %s %q", on, source, notes)
		}
		entry, ok := e.activeEntry(t, ledger.SourceBillPayment, id)
		if !ok {
			t.Fatal("legacy payment not posted")
		}
		if at := e.postedAt(t, entry); !at.Equal(c.on) {
			t.Errorf("legacy payment posted_at = %s, want %s", at, c.on)
		}
		b := e.billBasis(t, c.id)
		eq(t, "AP face", b.Face, c.face)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM bill_payments WHERE bill_id = $1`, open); n != 0 {
		t.Errorf("open bill got %d payments", n)
	}
	if !strings.Contains(out, "BILL B-USD USD 700.0000 dated 2025-06-10") {
		t.Errorf("dry-run listing lacks the legacy payment:\n%s", out)
	}
	e.trialBalanced(t)
}

func TestBackpostRefusesOnMissingRates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	e.invoice(t, doc{number: "INV-EUR", currency: "EUR", status: "OPEN", amount: "100", issue: day(2026, 1, 5)})
	e.invoice(t, doc{number: "INV-OLD", currency: "USD", status: "OPEN", amount: "100", issue: day(2024, 3, 1)})
	sgd := e.invoice(t, doc{number: "INV-SGD", currency: "SGD", status: "PARTIAL", amount: "100", balance: "50", issue: day(2026, 1, 5)})
	sgdPay := e.pythonPayment(t, sgd, "SGD", "50", day(2025, 12, 1), "")
	e.bill(t, doc{number: "B-EUR", currency: "EUR", status: "OPEN", amount: "10", issue: day(2026, 1, 5)})
	before := e.rows(t)

	for _, apply := range []bool{false, true} {
		rep, out, err := e.run(t, apply)
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("apply=%v: err = %v, want ErrRefused\n%s", apply, err, out)
		}
		want := []string{
			"INVOICE INV-EUR EUR 2026-01-05: no EUR→IDR rate on or before",
			"INVOICE INV-OLD USD 2024-03-01: no USD→IDR rate on or before",
			"PAYMENT " + sgdPay.String() + " of INVOICE INV-SGD SGD 2025-12-01: no SGD→IDR rate on or before",
			"BILL B-EUR EUR 2026-01-05: no EUR→IDR rate on or before",
		}
		if len(rep.MissingRates) != len(want) {
			t.Errorf("apply=%v: missing rates = %q, want %q", apply, rep.MissingRates, want)
		}
		// The preflight stops the run: nothing is posted, not even under a
		// savepoint, so no posting refusals are listed.
		if rep.Invoices != 0 || rep.Bills != 0 || len(rep.Inconsistent) != 0 {
			t.Errorf("apply=%v: the run went past the preflight: %+v", apply, rep)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("apply=%v: output lacks %q:\n%s", apply, w, out)
			}
		}
		if after := e.rows(t); after != before {
			t.Errorf("apply=%v: rows = %v, want %v (nothing written)", apply, after, before)
		}
	}
}

func TestBackpostRefusesOnBadAccounts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	inv := e.invoice(t, doc{number: "INV-BAD", currency: "IDR", status: "OPEN", amount: "100", issue: paid2026})
	item := uuid.New()
	e.exec(t, `INSERT INTO items (item_id, name, item_type, revenue_account_id) VALUES ($1, 'Bank item', 'SERVICE', (SELECT account_id FROM chart_of_accounts WHERE code = '1000'))`, item)
	e.exec(t, `INSERT INTO invoice_line_items (line_item_id, invoice_id, item_id, position, description, quantity, unit_price, amount) VALUES ($1, $2, $3, 1, 'x', 1, 100, 100)`, uuid.New(), inv, item)
	before := e.rows(t)

	rep, out, err := e.run(t, true)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused\n%s", err, out)
	}
	if len(rep.BadAccounts) != 1 || !strings.Contains(rep.BadAccounts[0], "INVOICE INV-BAD: account 1000 (ASSET) cannot take revenue") {
		t.Errorf("bad accounts = %q", rep.BadAccounts)
	}
	if rep.Invoices != 0 || len(rep.Inconsistent) != 0 {
		t.Errorf("the run went past the preflight: %+v", rep)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v (nothing written)", after, before)
	}
}

func TestBackpostIdempotent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	if rep, _ := e.mustRun(t, true); rep.Invoices == 0 || rep.Bills == 0 {
		t.Fatalf("first run posted nothing: %+v", rep)
	}
	after := e.rows(t)
	rep, _ := e.mustRun(t, true)
	if rep.Invoices != 0 || rep.Bills != 0 || rep.PaymentsRestated != 0 || rep.PaymentsPosted != 0 || rep.LegacyBillPayments != 0 || len(rep.Totals) != 0 {
		t.Errorf("second run = %+v, want 0 of everything", rep)
	}
	if again := e.rows(t); again != after {
		t.Errorf("second run wrote rows: %v, want %v", again, after)
	}
	e.trialBalanced(t)
}

func TestBackpostSkipsDraftsTemplatesVoid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	draft := e.invoice(t, doc{number: "INV-DRAFT", currency: "IDR", status: "DRAFT", amount: "100", issue: paid2026})
	draftPay := e.pythonPayment(t, draft, "IDR", "60", paid2026, "1")
	tpl := e.invoice(t, doc{number: "INV-TPL", currency: "IDR", status: "OPEN", amount: "100", issue: paid2026, template: true})
	tplPay := e.pythonPayment(t, tpl, "IDR", "70", paid2026, "")
	zero := e.invoice(t, doc{number: "INV-ZERO", currency: "USD", status: "PAID", amount: "0", balance: "0", issue: paid2026})
	zeroPay := e.pythonPayment(t, zero, "USD", "5", paid2026, "")
	void := e.invoice(t, doc{number: "INV-VOID", currency: "IDR", status: "VOID", amount: "100", issue: paid2026})
	voidPay := e.pythonPayment(t, void, "IDR", "100", paid2026, "1")
	voidManual := e.pythonPayment(t, void, "IDR", "40", paid2026, "")
	e.bill(t, doc{number: "B-DRAFT", currency: "IDR", status: "DRAFT", amount: "100", issue: paid2026})
	e.bill(t, doc{number: "B-VOID", currency: "IDR", status: "VOID", amount: "100", balance: "40", issue: paid2026})
	e.bill(t, doc{number: "B-VOID-UNPAID", currency: "IDR", status: "VOID", amount: "100", issue: paid2026})
	before := e.rows(t)

	rep, out := e.mustRun(t, true)
	if rep.Invoices != 0 || rep.Bills != 0 || rep.PaymentsPosted != 0 || rep.PaymentsRestated != 0 || rep.LegacyBillPayments != 0 {
		t.Errorf("report = %+v", rep)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
	// I1: every CLEARED payment on an invoice that is never posted is the
	// owner's decision, split by whether it has an entry.
	wantDecisions := []string{
		"INVOICE INV-DRAFT (DRAFT) PAYMENT " + draftPay.String() + " IDR 60.0000 dated 2026-02-01: has an entry: its receivable credit stays on the ledger",
		"INVOICE INV-TPL (OPEN, template) PAYMENT " + tplPay.String() + " IDR 70.0000 dated 2026-02-01: no entry: nothing on the ledger",
		"INVOICE INV-VOID (VOID) PAYMENT " + voidPay.String() + " IDR 100.0000 dated 2026-02-01: has an entry: its receivable credit stays on the ledger",
		"INVOICE INV-VOID (VOID) PAYMENT " + voidManual.String() + " IDR 40.0000 dated 2026-02-01: no entry: nothing on the ledger",
		"INVOICE INV-ZERO (PAID, amount 0.0000) PAYMENT " + zeroPay.String() + " USD 5.0000 dated 2026-02-01: no entry: nothing on the ledger",
	}
	if len(rep.OwnerDecisions) != len(wantDecisions) {
		t.Errorf("owner decisions = %q, want %d", rep.OwnerDecisions, len(wantDecisions))
	}
	if !strings.Contains(out, "owner decisions (CLEARED payments on invoices never posted):") {
		t.Errorf("output lacks the owner decision list:\n%s", out)
	}
	for _, w := range append(wantDecisions,
		"BILL B-VOID IDR is VOID with amount 100.0000 ≠ balance 40.0000; not posted",
	) {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "B-VOID-UNPAID") {
		t.Errorf("an unpaid VOID bill is warned:\n%s", out)
	}
}

// Task 4's carry-over: a document that already has an active issue entry
// (finalized or opened by Go, or a legacy OPEN bill edited after accrual)
// is left alone, payments and all.
func TestBackpostSkipsAlreadyIssued(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-GO", currency: "USD", status: "PAID", amount: "1000", balance: "0", issue: issued2025})
	pay := e.pythonPayment(t, inv, "USD", "1000", paid2026, "16000")
	bill := e.bill(t, doc{number: "B-GO", currency: "IDR", status: "PARTIAL", amount: "1000", balance: "400", issue: paid2026})
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledgertest.IssueFixture(t, tx, inv)
	accounts, err := ledger.AccountsByCode(ctx, tx, []string{ledger.DefaultExpenseCode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.PostIssued(ctx, tx, base, ledger.Document{
		SourceType: ledger.SourceBill, ID: bill, Currency: "IDR", Amount: dec("1000"), RateDate: paid2026, IssueDate: paid2026,
		Shares: []ledger.Share{{AccountID: accounts[ledger.DefaultExpenseCode].ID, Amount: dec("1000")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	orig, _ := e.activeEntry(t, "PAYMENT", pay)
	before := e.rows(t)

	rep, _ := e.mustRun(t, true)
	if rep.Invoices != 0 || rep.Bills != 0 || rep.PaymentsRestated != 0 || rep.LegacyBillPayments != 0 {
		t.Errorf("report = %+v", rep)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
	if got, _ := e.activeEntry(t, "PAYMENT", pay); got != orig {
		t.Error("the payment of an already issued invoice was restated")
	}
}

func TestBackpostWarnsOnFaceMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-OFF", currency: "USD", status: "PARTIAL", amount: "1000", balance: "800", issue: issued2025})
	e.invoice(t, doc{number: "INV-OK", currency: "USD", status: "OPEN", amount: "1000", issue: issued2025})

	rep, out := e.mustRun(t, false)
	want := "invoice " + inv.String() + " INV-OFF USD: ledger face 1000.0000 ≠ balance_due 800.0000"
	if len(rep.Warnings) != 1 || rep.Warnings[0] != want {
		t.Errorf("warnings = %q, want [%q]", rep.Warnings, want)
	}
	if !strings.Contains(out, want) {
		t.Errorf("output lacks the warning:\n%s", out)
	}
}

// The run ends by checking the whole journal balances in base; a journal
// that does not is an error (exit 2), and nothing is written.
func TestBackpostRefusesUnbalancedJournal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	entry := uuid.New()
	e.exec(t, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES ($1, 'MANUAL', $1, 'broken')`, entry)
	e.exec(t, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency, base_amount_debit, base_amount_credit, fx_rate)
VALUES ($1, $2, (SELECT account_id FROM chart_of_accounts WHERE code = '1000'), 1, 0, 'IDR', 1, 0, 1)`, uuid.New(), entry)
	before := e.rows(t)
	_, out, err := e.run(t, true)
	if err == nil || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "journal does not balance in base: debit") {
		t.Fatalf("err = %v, want the unbalanced journal error\n%s", err, out)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
}
