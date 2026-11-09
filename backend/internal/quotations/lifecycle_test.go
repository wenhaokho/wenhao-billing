package quotations_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
)

func transition(t *testing.T, a *apitest.App, id uuid.UUID, action string, b any) quotations.QuotationOut {
	t.Helper()
	return apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPost, quotePath(id)+"/"+action, b), http.StatusOK)
}

func TestMarkSentFromDraftAndSent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertCustomer(t, a), nil))
	if out.LastSentAt != nil {
		t.Fatalf("last_sent_at = %v before sending", out.LastSentAt)
	}
	got := transition(t, a, out.QuotationID, "mark-sent", nil)
	if got.Status != "SENT" || got.LastSentAt == nil || !got.LastSentAt.Equal(apitest.Start) {
		t.Fatalf("status %s, last_sent_at %v, want SENT at %v", got.Status, got.LastSentAt, apitest.Start)
	}
	// Re-marking a SENT quotation refreshes the instant.
	a.Clock.Advance(2 * time.Hour)
	got = transition(t, a, out.QuotationID, "mark-sent", nil)
	if want := apitest.Start.Add(2 * time.Hour); got.Status != "SENT" || !got.LastSentAt.Equal(want) {
		t.Fatalf("status %s, last_sent_at %v, want SENT at %v", got.Status, got.LastSentAt, want)
	}
}

func TestMarkSentFromAcceptedIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, status)
		wantProblem(t, a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/mark-sent", nil),
			http.StatusBadRequest, "quotation_not_sendable", "cannot mark-sent from status "+status, "", "")
		if got := get(t, a, out.QuotationID); got.Status != status || got.LastSentAt != nil {
			t.Errorf("%s: row changed: %s, %v", status, got.Status, got.LastSentAt)
		}
	}
}

func TestAcceptSetsAcceptedAtAndBy(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, from := range []string{"DRAFT", "SENT"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, from)
		got := transition(t, a, out.QuotationID, "accept", map[string]any{"accepted_by": "Jane Client"})
		if got.Status != "ACCEPTED" || got.AcceptedBy == nil || *got.AcceptedBy != "Jane Client" ||
			got.AcceptedAt == nil || !got.AcceptedAt.Equal(apitest.Start) {
			t.Fatalf("%s: %s by %v at %v", from, got.Status, got.AcceptedBy, got.AcceptedAt)
		}
	}
	// {} and {accepted_by: null} are fine and store null.
	for _, b := range []map[string]any{{}, {"accepted_by": nil}} {
		out := post(t, a, body(c, nil))
		got := transition(t, a, out.QuotationID, "accept", b)
		if got.Status != "ACCEPTED" || got.AcceptedBy != nil || got.AcceptedAt == nil {
			t.Fatalf("%v: %s by %v at %v", b, got.Status, got.AcceptedBy, got.AcceptedAt)
		}
	}
	// Stored as sent, an empty string included.
	out := post(t, a, body(c, nil))
	if got := transition(t, a, out.QuotationID, "accept", map[string]any{"accepted_by": ""}); got.AcceptedBy == nil || *got.AcceptedBy != "" {
		t.Fatalf("accepted_by = %v, want empty", got.AcceptedBy)
	}
}

func TestAcceptRefusals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, status)
		wantProblem(t, a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/accept", map[string]any{}),
			http.StatusBadRequest, "quotation_not_acceptable", "cannot accept from status "+status, "", "")
		if got := get(t, a, out.QuotationID); got.Status != status || got.AcceptedAt != nil {
			t.Errorf("%s: row changed: %s, %v", status, got.Status, got.AcceptedAt)
		}
	}
	// accepted_by longer than its column is the 422 at the field.
	out := post(t, a, body(c, nil))
	rec := a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/accept", map[string]any{"accepted_by": strings.Repeat("x", 256)})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := get(t, a, out.QuotationID); got.Status != "DRAFT" {
		t.Errorf("status %s after a refused accept", got.Status)
	}
	if got := transition(t, a, out.QuotationID, "accept", map[string]any{"accepted_by": strings.Repeat("x", 255)}); got.Status != "ACCEPTED" {
		t.Errorf("255 characters: status %s", got.Status)
	}
}

func TestAcceptRequiresBody(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertCustomer(t, a), nil))
	// A JSON null body is Python's missing body: 422. A request with no body
	// at all is huma's own 400 "request body is required" (PORT-NOTES).
	for _, c := range []struct {
		name string
		body any
		code int
	}{
		{"null", map[string]any(nil), http.StatusUnprocessableEntity},
		{"empty", nil, http.StatusBadRequest},
	} {
		if rec := a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/accept", c.body); rec.Code != c.code {
			t.Errorf("%s body: %d, want %d: %s", c.name, rec.Code, c.code, rec.Body)
		}
	}
	if got := get(t, a, out.QuotationID); got.Status != "DRAFT" {
		t.Errorf("status %s after a body-less accept", got.Status)
	}
}

func TestDeclineFromSent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, from := range []string{"DRAFT", "SENT"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, from)
		if got := transition(t, a, out.QuotationID, "decline", nil); got.Status != "DECLINED" {
			t.Errorf("%s: status %s", from, got.Status)
		}
	}
	for _, status := range []string{"ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, status)
		wantProblem(t, a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/decline", nil),
			http.StatusBadRequest, "quotation_not_declinable", "cannot decline from status "+status, "", "")
		if got := get(t, a, out.QuotationID); got.Status != status {
			t.Errorf("%s: status became %s", status, got.Status)
		}
	}
}

func TestVoidFromEveryStatusButInvoiced(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, from := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, from)
		if got := transition(t, a, out.QuotationID, "void", nil); got.Status != "VOID" {
			t.Errorf("%s: status %s", from, got.Status)
		}
	}
}

func TestVoidInvoicedIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertCustomer(t, a), nil))
	setStatus(t, a, out.QuotationID, "INVOICED")
	wantProblem(t, a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/void", nil),
		http.StatusBadRequest, "quotation_invoiced", "cannot void an already-invoiced quotation", "", "")
	if got := get(t, a, out.QuotationID); got.Status != "INVOICED" {
		t.Errorf("status %s after a refused void", got.Status)
	}
}

func TestLifecycleUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	for _, action := range []string{"mark-sent", "accept", "decline", "void"} {
		wantProblem(t, a.Do(t, http.MethodPost, quotePath(id)+"/"+action, map[string]any{}),
			http.StatusBadRequest, "quotation_not_found", "quotation "+id.String()+" not found", "", "")
	}
}

func TestLifecycleTimestampsAreUTC(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertCustomer(t, a), nil))
	sent := a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/mark-sent", nil)
	if !strings.Contains(sent.Body.String(), `"last_sent_at":"2026-01-15T09:00:00Z"`) {
		t.Errorf("mark-sent: %s", sent.Body)
	}
	acc := a.Do(t, http.MethodPost, quotePath(out.QuotationID)+"/accept", map[string]any{})
	if !strings.Contains(acc.Body.String(), `"accepted_at":"2026-01-15T09:00:00Z"`) {
		t.Errorf("accept: %s", acc.Body)
	}
}
