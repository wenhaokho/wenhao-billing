package payments

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

// Ports of tests/integration/test_invoice_payments_list.py.

func (a *api) list(t *testing.T, id uuid.UUID) []PaymentOut {
	t.Helper()
	return apitest.Decode[[]PaymentOut](t, a.app.Do(t, http.MethodGet, paymentsPath(id), nil), http.StatusOK)
}

func ids(ps []PaymentOut) []uuid.UUID {
	out := []uuid.UUID{}
	for _, p := range ps {
		out = append(out, p.PaymentID)
	}
	return out
}

func (a *api) seedInvoice(t *testing.T) uuid.UUID {
	t.Helper()
	return a.invoice(t, inv{customer: a.customer(t, "Acme Corp"), currency: "USD", status: "SENT", balance: "1000"})
}

func TestListPaymentsNewestFirst(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.seedInvoice(t)
	old := a.mustRecord(t, id, map[string]any{"amount": "100", "payment_date": "2026-08-01"})
	newer := a.mustRecord(t, id, map[string]any{"amount": "200", "payment_date": "2026-08-05"})
	got := a.list(t, id)
	if want := []uuid.UUID{newer.PaymentID, old.PaymentID}; !slices.Equal(ids(got), want) {
		t.Fatalf("order %v, want %v", ids(got), want)
	}
	if got[0].Amount.Decimal().String() != "200" || got[0].Status != "CLEARED" {
		t.Errorf("first %+v", got[0])
	}
}

func TestListPaymentsEmpty(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	rec := a.app.Do(t, http.MethodGet, paymentsPath(a.seedInvoice(t)), nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q, want 200 []", rec.Code, rec.Body)
	}
}

func TestListPaymentsUnknownInvoiceIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	wantProblem(t, a.app.Do(t, http.MethodGet, paymentsPath(uuid.New()), nil), http.StatusNotFound, "invoice_not_found", "invoice not found")
}

// Unaccepted money never reaches the receipt dialog: only CLEARED payments
// are listed.
func TestListPaymentsExcludesNonCleared(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.seedInvoice(t)
	cleared := a.mustRecord(t, id, map[string]any{"amount": "100", "payment_date": "2026-08-01"})
	for _, status := range []string{"PENDING_MANUAL_REVIEW", "FLAGGED"} {
		a.exec(t, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type)
VALUES ($1, $2, 50, 'USD', 'Acme', '2026-08-05', 'WEBHOOK', $3, $4, 'NONE')`, uuid.New(), id, "pending-"+status, status)
	}
	if got := a.list(t, id); !slices.Equal(ids(got), []uuid.UUID{cleared.PaymentID}) {
		t.Errorf("listed %v, want [%s]", ids(got), cleared.PaymentID)
	}
}

// Payments of one day and one transaction time are listed in a fixed order:
// the primary key, descending, is the last sort key.
func TestListPaymentsTieBreakByID(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.seedInvoice(t)
	var want []uuid.UUID
	for range 4 {
		want = append(want, uuid.New())
	}
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for _, p := range want {
		a.exec(t, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type, created_at)
VALUES ($1, $2, 10, 'USD', 'Acme', '2026-08-01', 'MANUAL', $3, 'CLEARED', 'NONE', $4)`, p, id, p.String(), ts)
	}
	slices.SortFunc(want, func(x, y uuid.UUID) int { return -slices.Compare(x[:], y[:]) })
	if got := a.list(t, id); !slices.Equal(ids(got), want) {
		t.Errorf("order %v, want %v", ids(got), want)
	}
}
