package payments

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

func lastPayerPath(id uuid.UUID) string { return "/api/v1/invoices/" + id.String() + "/last-payer" }

func (a *api) lastPayer(t *testing.T, id uuid.UUID) *string {
	t.Helper()
	return apitest.Decode[LastPayerOut](t, a.app.Do(t, http.MethodGet, lastPayerPath(id), nil), http.StatusOK).PayerName
}

func TestLastPayerIsNewestNamedPaymentOfCustomer(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	other := a.customer(t, "Other Co")
	first := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	second := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	fresh := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	elsewhere := a.invoice(t, inv{customer: other, currency: "IDR", status: "OPEN", balance: "1000"})

	if got := a.lastPayer(t, fresh); got != nil {
		t.Fatalf("no payments yet: payer %q, want null", *got)
	}

	a.mustRecord(t, first, map[string]any{"amount": "1", "payment_date": "2026-04-20", "payer_name": "Acme"})
	a.mustRecord(t, second, map[string]any{"amount": "1", "payment_date": "2026-04-21", "payer_name": "PT Acme Indonesia"})
	// A later payment that named no payer holds "(manual entry)" and is skipped.
	a.mustRecord(t, first, map[string]any{"amount": "1", "payment_date": "2026-04-22", "payer_name": ""})

	// Any invoice of the customer, paid or not, gets the customer's newest name.
	for _, id := range []uuid.UUID{first, second, fresh} {
		if got := a.lastPayer(t, id); got == nil || *got != "PT Acme Indonesia" {
			t.Errorf("invoice %s: payer %v, want PT Acme Indonesia", id, got)
		}
	}
	if got := a.lastPayer(t, elsewhere); got != nil {
		t.Errorf("other customer: payer %q, want null", *got)
	}
}

func TestLastPayerUnknownInvoiceIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	wantProblem(t, a.app.Do(t, http.MethodGet, lastPayerPath(uuid.New()), nil), http.StatusNotFound, "invoice_not_found", "invoice not found")
}
