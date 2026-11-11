package pdf_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	lpdf "github.com/ledongthuc/pdf"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgertest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
)

// This is an external test package: internal/payments imports internal/pdf,
// so an in-package test that records payments through payments would cycle.

type paidInvoice struct {
	app            *apitest.App
	invoice        uuid.UUID
	first, second  payments.PaymentOut
	invoiceBalance decimal.Decimal
}

// twoPartials is an issued 1000 IDR invoice with two manual payments, 400 on
// the 20th then 600 on the 21st.
func twoPartials(t *testing.T) paidInvoice {
	t.Helper()
	svc := payments.Service{BaseCurrency: "IDR", Hosting: payments.NoHosting{}}
	a := apitest.New(t, func(h huma.API, d apitest.Deps) {
		payments.Register(h, d.Pool, svc, d.Config, d.Clock, payments.ReceiptDeps{Sender: d.Mail, Renderer: payments.StubRenderer{}})
	})
	ctx := t.Context()
	cust, inv := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO customers (customer_id, name, contact_email) VALUES ($1, 'Acme Pte Ltd', 'ap@acme.example')`, []any{cust}},
		{`INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, invoice_number, issue_date)
VALUES ($1, $2, 'MILESTONE', 'IDR', 1000, 1000, 'SENT', 'WH20260001', '2026-04-10')`, []any{inv, cust}},
	} {
		if _, err := a.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledgertest.IssueFixture(t, tx, inv)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	record := func(amount, date string) payments.PaymentOut {
		rec := a.Do(t, http.MethodPost, "/api/v1/invoices/"+inv.String()+"/record-payment",
			map[string]any{"amount": amount, "payment_date": date, "payer_name": "Acme"})
		return apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
	}
	p := paidInvoice{app: a, invoice: inv}
	p.first = record("400", "2026-04-20")
	p.second = record("600", "2026-04-21")
	if err := a.Pool.QueryRow(ctx, `SELECT balance_due FROM invoices WHERE invoice_id = $1`, inv).Scan(&p.invoiceBalance); err != nil {
		t.Fatal(err)
	}
	return p
}

func receiptText(t *testing.T, p paidInvoice, id uuid.UUID) (pdf.ReceiptData, string) {
	t.Helper()
	data, err := pdf.LoadReceipt(t.Context(), p.app.Pool, id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pdf.RenderReceipt(t.Context(), pdf.ReceiptDocument(data))
	if err != nil {
		t.Fatal(err)
	}
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := 1; i <= r.NumPage(); i++ {
		s, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return data, strings.Join(out, "\n")
}

func TestReceiptShowsBalanceAsOfItsPayment(t *testing.T) {
	t.Parallel()
	p := twoPartials(t)

	data, text := receiptText(t, p, p.first.PaymentID)
	if !data.BalanceAfter.Equal(decimal.NewFromInt(600)) {
		t.Errorf("first receipt BalanceAfter = %s, want 600", data.BalanceAfter)
	}
	if !strings.Contains(text, "Remaining balance: 600 IDR.") || strings.Contains(text, "paid in full") {
		t.Errorf("first receipt should show a 600 balance and not paid in full:\n%s", text)
	}

	data, text = receiptText(t, p, p.second.PaymentID)
	if !data.BalanceAfter.IsZero() {
		t.Errorf("second receipt BalanceAfter = %s, want 0", data.BalanceAfter)
	}
	if !strings.Contains(text, "now paid in full") {
		t.Errorf("second receipt should say paid in full:\n%s", text)
	}
}

// BalanceAfter must not drift from payments.Service.clear / recordManual.
func TestBalanceReplayMatchesLiveBalance(t *testing.T) {
	t.Parallel()
	p := twoPartials(t)
	data, err := pdf.LoadReceipt(t.Context(), p.app.Pool, p.second.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if !data.BalanceAfter.Equal(p.invoiceBalance) {
		t.Errorf("BalanceAfter %s, stored balance_due %s", data.BalanceAfter, p.invoiceBalance)
	}
	if r := replay(t, p.app, p.invoice, p.second.PaymentID); !r.Equal(p.invoiceBalance) {
		t.Errorf("replayed %s, stored balance_due %s", r, p.invoiceBalance)
	}
}

// A reversed payment relieves nothing: the later receipt shows it.
func TestReceiptBalanceIgnoresReversedPayment(t *testing.T) {
	t.Parallel()
	p := twoPartials(t)
	svc := payments.Service{BaseCurrency: "IDR", Hosting: payments.NoHosting{}}
	tx, err := p.app.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reverse(t.Context(), tx, p.first.PaymentID, nil, "wrong payer"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := pdf.LoadReceipt(t.Context(), p.app.Pool, p.second.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if !data.BalanceAfter.Equal(decimal.NewFromInt(400)) {
		t.Errorf("BalanceAfter = %s, want 400 (the reversed 400 is back)", data.BalanceAfter)
	}
}

// replay is pdf.Replay (no stored-balance shortcut) over what the database holds.
func replay(t *testing.T, a *apitest.App, invoice, upTo uuid.UUID) decimal.Decimal {
	t.Helper()
	ctx := t.Context()
	q := paymentsdb.New(a.Pool)
	ps, err := q.ListInvoicePayments(ctx, pgtype.UUID{Bytes: invoice, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	times, err := q.ListInvoiceClearTimes(ctx, pgtype.UUID{Bytes: invoice, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	at := map[uuid.UUID]time.Time{}
	for _, c := range times {
		at[c.PaymentID] = c.ClearedAt
	}
	var amount, balance decimal.Decimal
	var cur string
	if err := a.Pool.QueryRow(ctx, `SELECT amount, balance_due, currency FROM invoices WHERE invoice_id = $1`, invoice).Scan(&amount, &balance, &cur); err != nil {
		t.Fatal(err)
	}
	return pdf.Replay(pdf.Invoice{Amount: amount, Currency: cur, BalanceDue: balance, ClearedAt: at}, ps, upTo)
}

// issued is an issued IDR invoice (amount, PPh23 withholding wht) and a
// payment service on a fresh database.
func issued(t *testing.T, amount, wht string) (*apitest.App, payments.Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	svc := payments.Service{BaseCurrency: "IDR", Hosting: payments.NoHosting{}}
	a := apitest.New(t, func(h huma.API, d apitest.Deps) {
		payments.Register(h, d.Pool, svc, d.Config, d.Clock, payments.ReceiptDeps{Sender: d.Mail, Renderer: payments.StubRenderer{}})
	})
	ctx := t.Context()
	cust, inv := uuid.New(), uuid.New()
	whtType := "NONE"
	if wht != "0" {
		whtType = "PPH23"
	}
	if _, err := a.Pool.Exec(ctx, `INSERT INTO customers (customer_id, name, contact_email) VALUES ($1, 'Acme Pte Ltd', 'ap@acme.example')`, cust); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pool.Exec(ctx, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, invoice_number, issue_date, wht_type, wht_amount, wht_booked)
VALUES ($1, $2, 'MILESTONE', 'IDR', $3, $3, 'SENT', 'WH20260001', '2026-04-10', $4, $5, 0)`, inv, cust, decimal.RequireFromString(amount), whtType, decimal.RequireFromString(wht)); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledgertest.IssueFixture(t, tx, inv)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return a, svc, cust, inv
}

// held stores a payment waiting for manual review.
func held(t *testing.T, a *apitest.App, cust uuid.UUID, amount, date string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO payments (payment_id, customer_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, $2, $3, 'IDR', 'Acme', $4, 'WEBHOOK', 'PENDING_MANUAL_REVIEW')`, id, cust, decimal.RequireFromString(amount), date); err != nil {
		t.Fatal(err)
	}
	return id
}

func approve(t *testing.T, a *apitest.App, svc payments.Service, payment, invoice uuid.UUID) {
	t.Helper()
	tx, err := a.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveManual(t.Context(), tx, payment, invoice, &a.AdminID, "NONE"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Relief is applied in the order payments clear, not the order they are
// dated: A (700, 10 Apr) is held, B (400, 12 Apr) is recorded and leaves 600,
// then A is approved and clamps the invoice to 0.
func TestReceiptBalanceFollowsClearingOrderNotPaymentDate(t *testing.T) {
	t.Parallel()
	a, svc, cust, inv := issued(t, "1000", "0")
	pa := held(t, a, cust, "700", "2026-04-10")
	rec := a.Do(t, http.MethodPost, "/api/v1/invoices/"+inv.String()+"/record-payment",
		map[string]any{"amount": "400", "payment_date": "2026-04-12", "payer_name": "Acme"})
	pb := apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
	approve(t, a, svc, pa, inv)

	p := paidInvoice{app: a, invoice: inv}
	dataA, textA := receiptText(t, p, pa)
	if !dataA.BalanceAfter.IsZero() || !strings.Contains(textA, "now paid in full") {
		t.Errorf("A (cleared last) BalanceAfter %s:\n%s", dataA.BalanceAfter, textA)
	}
	dataB, textB := receiptText(t, p, pb.PaymentID)
	if !dataB.BalanceAfter.Equal(decimal.NewFromInt(600)) || strings.Contains(textB, "paid in full") {
		t.Errorf("B (cleared first) BalanceAfter %s:\n%s", dataB.BalanceAfter, textB)
	}
}

// An approval that books withholding (SetPaymentWithheld) relieves by amount +
// withheld, and the replay agrees with the stored balance_due at each step.
func TestReplayMatchesStoredBalanceAfterApprovalWithWithholding(t *testing.T) {
	t.Parallel()
	a, svc, cust, inv := issued(t, "1000", "100")
	stored := func() decimal.Decimal {
		var b decimal.Decimal
		if err := a.Pool.QueryRow(t.Context(), `SELECT balance_due FROM invoices WHERE invoice_id = $1`, inv).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := held(t, a, cust, "300", "2026-04-11")
	approve(t, a, svc, first, inv) // not the expected net cash: books no withholding
	if got, want := replay(t, a, inv, first), stored(); !got.Equal(want) || !want.Equal(decimal.NewFromInt(700)) {
		t.Errorf("after first: replay %s, stored %s (want 700)", got, want)
	}
	second := held(t, a, cust, "600", "2026-04-12")
	approve(t, a, svc, second, inv) // exactly the net cash: books the 100 withheld
	var wht decimal.Decimal
	if err := a.Pool.QueryRow(t.Context(), `SELECT wht_amount FROM payments WHERE payment_id = $1`, second).Scan(&wht); err != nil {
		t.Fatal(err)
	}
	if !wht.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("payment wht_amount %s, want 100", wht)
	}
	if got, want := replay(t, a, inv, second), stored(); !got.Equal(want) || !want.IsZero() {
		t.Errorf("after second: replay %s, stored %s (want 0)", got, want)
	}
	// The first receipt still shows its own balance.
	data, err := pdf.LoadReceipt(t.Context(), a.Pool, first)
	if err != nil {
		t.Fatal(err)
	}
	if !data.BalanceAfter.Equal(decimal.NewFromInt(700)) {
		t.Errorf("first receipt BalanceAfter %s, want 700", data.BalanceAfter)
	}
}
