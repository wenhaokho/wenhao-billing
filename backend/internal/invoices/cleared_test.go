package invoices

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

// clearedInvoice stores an invoice with the given status and one payment of
// it, and returns the invoice and payment ids.
func clearedInvoice(t *testing.T, a *apitest.App, status string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	inv := insertInvoice(t, a.Pool, strPtr("WH-CLR-"+uuid.NewString()[:8]))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET status = $2 WHERE invoice_id = $1`, inv, status); err != nil {
		t.Fatal(err)
	}
	pay := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status)
VALUES ($1, $2, 10, 'IDR', 'Acme Corp', '2026-01-10', 'TEST', $3, 'CLEARED')`, pay, inv, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return inv, pay
}

func logRow(t *testing.T, a *apitest.App, pay uuid.UUID, action string, actor *uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO reconciliation_log (log_id, payment_id, action, actor_user_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), pay, action, actor, at); err != nil {
		t.Fatal(err)
	}
}

func getDetail(t *testing.T, a *apitest.App, id uuid.UUID) InvoiceDetail {
	t.Helper()
	return apitest.Decode[InvoiceDetail](t, a.Do(t, http.MethodGet, "/api/v1/invoices/"+id.String(), nil), http.StatusOK)
}

func TestDetailClearedByEngineHasNullActor(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv, pay := clearedInvoice(t, a, "PAID")
	at := time.Date(2026, 1, 12, 8, 30, 0, 0, time.UTC)
	logRow(t, a, pay, "CLEARED", nil, at)
	got := getDetail(t, a, inv)
	if got.ClearedAt == nil || !got.ClearedAt.Equal(at) {
		t.Fatalf("cleared_at = %v, want %s", got.ClearedAt, at)
	}
	if got.ClearedBy != nil {
		t.Fatalf("cleared_by = %q, want null (the matching engine)", *got.ClearedBy)
	}
}

func TestDetailClearedManuallyNamesTheLatestActor(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv, pay := clearedInvoice(t, a, "PAID")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE users SET display_name = 'Wen Hao' WHERE user_id = $1`, a.AdminID); err != nil {
		t.Fatal(err)
	}
	// An older engine row, then the admin's override: the newest row wins,
	// and a non-clearing row after it is ignored.
	logRow(t, a, pay, "CLEARED", nil, time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC))
	at := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	logRow(t, a, pay, "OVERRIDDEN", &a.AdminID, at)
	logRow(t, a, pay, "PENDING_MANUAL_REVIEW", nil, time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC))
	got := getDetail(t, a, inv)
	if got.ClearedBy == nil || *got.ClearedBy != "Wen Hao" || got.ClearedAt == nil || !got.ClearedAt.Equal(at) {
		t.Fatalf("cleared = %v by %v, want %s by Wen Hao", got.ClearedAt, got.ClearedBy, at)
	}
}

func TestDetailClearedByFallsBackToEmail(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv, pay := clearedInvoice(t, a, "PAID")
	logRow(t, a, pay, "CLEARED", &a.AdminID, time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC))
	if got := getDetail(t, a, inv); got.ClearedBy == nil || *got.ClearedBy != apitest.AdminEmail {
		t.Fatalf("cleared_by = %v, want %s", got.ClearedBy, apitest.AdminEmail)
	}
}

func TestDetailClearedFieldsNullUnlessPaid(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv, pay := clearedInvoice(t, a, "PARTIAL")
	logRow(t, a, pay, "CLEARED", &a.AdminID, time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC))
	rec := a.Do(t, http.MethodGet, "/api/v1/invoices/"+inv.String(), nil)
	m := apitest.Decode[map[string]any](t, rec, http.StatusOK)
	for _, k := range []string{"cleared_at", "cleared_by"} {
		v, ok := m[k]
		if !ok || v != nil {
			t.Fatalf("%s = %v (present %v), want null", k, v, ok)
		}
	}
}
