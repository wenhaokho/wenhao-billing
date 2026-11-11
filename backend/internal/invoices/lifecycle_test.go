package invoices

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

func actionPath(id uuid.UUID, action string) string { return invoicePath(id) + "/" + action }

func finalize(t *testing.T, a *apitest.App, id uuid.UUID) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, actionPath(id, "finalize"), nil), http.StatusOK)
}

// insertWithStatus stores an invoice with the given status and amount (the
// balance is the amount) and returns its id.
func insertWithStatus(t *testing.T, pool *pgxpool.Pool, status, amount string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status)
VALUES ($1, 'MILESTONE', 'USD', $2::numeric, $2::numeric, $3)`, id, amount, status); err != nil {
		t.Fatal(err)
	}
	return id
}

func zeroValueDraft(t *testing.T, a *apitest.App, c uuid.UUID) InvoiceOut {
	t.Helper()
	return post(t, a, map[string]any{"customer_id": c, "currency": "USD", "line_items": []any{
		map[string]any{"description": "Complimentary", "quantity": "1", "unit_price": "0"},
	}})
}

// Ports of tests/integration/test_zero_value_invoice.py. The settle service
// tests go through the mark-paid route, which is all the service is.

func TestZeroValueDraftHasZeroTotals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := zeroValueDraft(t, a, insertCustomer(t, a.Pool, "NONE", "", false))
	wantDec4(t, "amount", p4(out.Amount), "0")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "0")
	if out.Status != "DRAFT" {
		t.Errorf("status = %s", out.Status)
	}
}

func TestFinalizeZeroValueInvoiceBecomesPaid(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := finalize(t, a, zeroValueDraft(t, a, insertCustomer(t, a.Pool, "NONE", "", false)).InvoiceID)
	if out.Status != "PAID" {
		t.Errorf("status = %s", out.Status)
	}
	wantDec4(t, "balance_due", p4(out.BalanceDue), "0")
}

func TestFinalizeNonzeroInvoiceBecomesOpen(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := finalize(t, a, post(t, a, createBody(c, "100", nil)).InvoiceID)
	// Issued, not sent: no email went out.
	if out.Status != "OPEN" {
		t.Errorf("status = %s", out.Status)
	}
	wantDec4(t, "balance_due", p4(out.BalanceDue), "100")
}

func TestSettleZeroValueInvoiceService(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"OPEN", "SENT", "PARTIAL"} {
		id := insertWithStatus(t, a.Pool, status, "0")
		out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil), http.StatusOK)
		if out.Status != "PAID" {
			t.Errorf("from %s: status = %s", status, out.Status)
		}
		wantDec4(t, "balance_due", p4(out.BalanceDue), "0")
	}
}

func TestSettleRejectsNonzeroBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "SENT", "100")
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil), http.StatusBadRequest, "invoice_balance_outstanding",
		"invoice has an outstanding balance of 100.0000 — record a payment instead", "", "")
	if got := get(t, a, id); got.Status != "SENT" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
}

func TestSettleRejectsDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := zeroValueDraft(t, a, insertCustomer(t, a.Pool, "NONE", "", false))
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(out.InvoiceID, "mark-paid"), nil), http.StatusBadRequest, "invoice_not_settleable",
		"only OPEN, SENT or PARTIAL invoices can be settled this way (got DRAFT)", "", "")
	// The other states that are not settleable.
	for _, status := range []string{"PAID", "VOID"} {
		id := insertWithStatus(t, a.Pool, status, "0")
		wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil), http.StatusBadRequest, "invoice_not_settleable",
			"only OPEN, SENT or PARTIAL invoices can be settled this way (got "+status+")", "", "")
	}
}

func TestMarkPaidEndpointSettlesZeroValue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "SENT", "0")
	out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil), http.StatusOK)
	if out.Status != "PAID" {
		t.Errorf("status = %s", out.Status)
	}
}

func TestMarkPaidEndpointRejectsNonzeroBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "SENT", "1000")
	if rec := a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestMarkPaidUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "mark-paid"), nil), http.StatusBadRequest,
		"invoice_not_found", "invoice "+id.String()+" not found", "", "")
}

func TestFinalizeNonDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
		id := insertWithStatus(t, a.Pool, status, "5")
		wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "finalize"), nil), http.StatusBadRequest, "invoice_not_draft",
			"only DRAFT invoices can be finalized (got "+status+")", "", "")
	}
}

func TestFinalizeTemplateIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "DRAFT", "5")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "finalize"), nil), http.StatusBadRequest, "invoice_is_template",
		"cannot finalize a recurring template", "", "")
	if got := get(t, a, id); got.Status != "DRAFT" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
}

// TestFinalizeVoidTemplateIsNotDraft: the DRAFT check comes before the
// template check (invoicing.py:647-650), so a VOID template is refused as
// not a DRAFT.
func TestFinalizeVoidTemplateIsNotDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "VOID", "5")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "finalize"), nil), http.StatusBadRequest, "invoice_not_draft",
		"only DRAFT invoices can be finalized (got VOID)", "", "")
}

func TestFinalizeUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "finalize"), nil), http.StatusBadRequest,
		"invoice_not_found", "invoice "+id.String()+" not found", "", "")
}

func TestVoidFromEachNonPaidStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"DRAFT", "OPEN", "SENT", "PARTIAL", "VOID", "template"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			st := status
			if status == "template" {
				st = "DRAFT"
			}
			id := insertWithStatus(t, a.Pool, st, "250")
			if status == "template" {
				if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if st != "DRAFT" && st != "VOID" {
				// An issued invoice has its issue entry (PORT-NOTES 162):
				// issue it through finalize, then move it to the status.
				id = insertWithStatus(t, a.Pool, "DRAFT", "250")
				finalize(t, a, id)
				setStatus(t, a.Pool, id, st)
			}
			out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{"reason": "mistake"}), http.StatusOK)
			if out.Status != "VOID" {
				t.Errorf("status = %s", out.Status)
			}
			wantDec4(t, "balance_due", p4(out.BalanceDue), "0")
			wantDec4(t, "amount", p4(out.Amount), "250")
		})
	}
}

func TestVoidPaidIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "PAID", "0")
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{"reason": "x"}), http.StatusBadRequest, "invoice_paid",
		"cannot void a PAID invoice — reverse payments first", "", "")
	if got := get(t, a, id); got.Status != "PAID" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
}

func TestVoidUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{"reason": "x"}), http.StatusBadRequest,
		"invoice_not_found", "invoice "+id.String()+" not found", "", "")
}

func TestVoidRequiresReason(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "OPEN", "5")
	for _, body := range []map[string]any{{}, nil} {
		if rec := a.Do(t, http.MethodPost, actionPath(id, "void"), body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %v: status %d: %s", body, rec.Code, rec.Body)
		}
	}
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{}), http.StatusUnprocessableEntity, "", invalid, "reason", "expected required property reason to be present")
	if got := get(t, a, id); got.Status != "OPEN" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
}

func TestDeleteDraft204CascadesLines(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, createBody(c, "10", nil))
	rec := a.Do(t, http.MethodDelete, invoicePath(out.InvoiceID), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	wantProblem(t, a.Do(t, http.MethodGet, invoicePath(out.InvoiceID), nil), http.StatusNotFound, "invoice_not_found", "invoice not found", "", "")
	var lines int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM invoice_line_items WHERE invoice_id = $1`, out.InvoiceID).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 0 {
		t.Errorf("%d lines left", lines)
	}
}

func TestDeleteNonDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "OPEN", "5")
	wantProblem(t, a.Do(t, http.MethodDelete, invoicePath(id), nil), http.StatusBadRequest, "invoice_not_draft",
		"only DRAFT invoices can be deleted (got OPEN)", "", "")
	get(t, a, id)
	unknown := uuid.New()
	wantProblem(t, a.Do(t, http.MethodDelete, invoicePath(unknown), nil), http.StatusBadRequest,
		"invoice_not_found", "invoice "+unknown.String()+" not found", "", "")
}

const referencedSentence = "invoice is referenced by a payment or quotation"

func TestDeleteReferencedByQuotationIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	id := insertWithStatus(t, a.Pool, "DRAFT", "5")
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO quotations (quotation_id, customer_id, currency, amount, converted_invoice_id)
VALUES ($1, $2, 'USD', 5, $3)`, uuid.New(), c, id); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, a.Do(t, http.MethodDelete, invoicePath(id), nil), http.StatusConflict, "invoice_referenced", referencedSentence, "", "")
	get(t, a, id)
}

func TestDeleteReferencedByPaymentIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "DRAFT", "5")
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, $2, 5, 'USD', 'Payer', '2026-01-15', 'MANUAL', 'CLEARED')`, uuid.New(), id); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, a.Do(t, http.MethodDelete, invoicePath(id), nil), http.StatusConflict, "invoice_referenced", referencedSentence, "", "")
	get(t, a, id)
}
