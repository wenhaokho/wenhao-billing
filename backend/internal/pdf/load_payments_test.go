package pdf_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	lpdf "github.com/ledongthuc/pdf"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgertest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
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
		t.Errorf("replayed %s, stored balance_due %s", data.BalanceAfter, p.invoiceBalance)
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
