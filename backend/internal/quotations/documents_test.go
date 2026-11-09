package quotations_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
)

type failingMail struct{}

func (failingMail) Send(context.Context, email.Message) error { return errors.New("resend down") }

func newDocApp(t *testing.T, failing bool) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		quotations.Register(api, d.Pool, d.Clock)
		var mail email.Sender = d.Mail
		if failing {
			mail = failingMail{}
		}
		quotations.RegisterDocuments(api, d.Pool, d.Clock, mail)
	})
}

func insertQuotation(t *testing.T, a *apitest.App, number *string, status string, customer *uuid.UUID, validUntil *string) uuid.UUID {
	t.Helper()
	if customer == nil {
		c := contactCustomer(t, a, "")
		customer = &c
	}
	id := uuid.New()
	exec(t, a, `INSERT INTO quotations (quotation_id, customer_id, quotation_number, currency, amount, status, valid_until)
VALUES ($1, $2, $3, 'SGD', 5000, $4, $5)`, id, customer, number, status, validUntil)
	return id
}

func contactCustomer(t *testing.T, a *apitest.App, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var e *string
	if email != "" {
		e = &email
	}
	exec(t, a, `INSERT INTO customers (customer_id, name, contact_email) VALUES ($1, 'Acme Pte Ltd', $2)`, id, e)
	return id
}

func sptr(s string) *string { return &s }

func sendPath(id uuid.UUID) string { return quotePath(id) + "/send" }
func pdfPath(id uuid.UUID) string  { return quotePath(id) + "/pdf" }

type stored struct {
	Status     string
	LastSentAt *time.Time
}

func storedOf(t *testing.T, a *apitest.App, id uuid.UUID) stored {
	t.Helper()
	var s stored
	if err := a.Pool.QueryRow(t.Context(), `SELECT status, last_sent_at FROM quotations WHERE quotation_id = $1`, id).
		Scan(&s.Status, &s.LastSentAt); err != nil {
		t.Fatal(err)
	}
	return s
}

func detail(t *testing.T, a *apitest.App, method, path string, body any, status int) string {
	t.Helper()
	return apitest.Decode[struct{ Detail string }](t, a.Do(t, method, path, body), status).Detail
}

func TestQuotationPdfHeadersAndBytes(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, false)
	for name, c := range map[string]struct {
		number *string
		status string
		want   func(id uuid.UUID) string
	}{
		"number":    {sptr("Q20260001"), "SENT", func(uuid.UUID) string { return `inline; filename="quotation-Q20260001.pdf"` }},
		"no number": {nil, "DRAFT", func(id uuid.UUID) string { return `inline; filename="quotation-` + id.String()[:8] + `.pdf"` }},
		"invoiced":  {sptr("Q20260002"), "INVOICED", func(uuid.UUID) string { return `inline; filename="quotation-Q20260002.pdf"` }},
		"non-ascii": {sptr("Q-é"), "SENT", func(uuid.UUID) string {
			return `inline; filename="quotation-Q-_.pdf"; filename*=UTF-8''quotation-Q-%C3%A9.pdf`
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id := insertQuotation(t, a, c.number, c.status, nil, nil)
			rec := a.Do(t, http.MethodGet, pdfPath(id), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
				t.Errorf("content type %q", got)
			}
			if got, want := rec.Header().Get("Content-Disposition"), c.want(id); got != want {
				t.Errorf("disposition %q, want %q", got, want)
			}
			if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
				t.Errorf("body %.10q", rec.Body)
			}
		})
	}
	if got := detail(t, a, http.MethodGet, pdfPath(uuid.New()), nil, http.StatusNotFound); got != "quotation not found" {
		t.Errorf("detail %q", got)
	}
	if got := detail(t, a, http.MethodPost, sendPath(uuid.New()), map[string]any{"to_email": "a@acme.example"}, http.StatusNotFound); got != "quotation not found" {
		t.Errorf("send detail %q", got)
	}
}

func TestQuotationSendBodyAndLastSentAt(t *testing.T) {
	t.Parallel()
	for _, from := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()
			a := newDocApp(t, false)
			id := insertQuotation(t, a, sptr("Q20260001"), from, nil, sptr("2026-12-31"))
			out := apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPost, sendPath(id),
				map[string]any{"to_email": "ap@acme.example", "cc_email": "me@acme.example; me@acme.example"}), http.StatusOK)
			want := from
			if from == "DRAFT" {
				want = "SENT"
			}
			if out.Status != want {
				t.Errorf("status %s, want %s", out.Status, want)
			}
			got := storedOf(t, a, id)
			if got.Status != want || got.LastSentAt == nil || !got.LastSentAt.Equal(time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)) {
				t.Errorf("stored %+v (last_sent_at must be stamped for %s)", got, from)
			}
			msgs := a.Mail.Messages()
			if len(msgs) != 1 {
				t.Fatalf("%d messages", len(msgs))
			}
			m := msgs[0]
			// Go change 3: FmtAmount, not the raw decimal.
			wantBody := "Hi Acme Pte Ltd,\n\nPlease find attached our quotation Q20260001 for 5,000.00 SGD.\nValid until: 2026-12-31.\n\nThank you."
			if m.Text != wantBody || m.Subject != "Quotation Q20260001" || m.To != "ap@acme.example" {
				t.Errorf("message %q %q to %q", m.Text, m.Subject, m.To)
			}
			if len(m.Cc) != 1 || m.Cc[0] != "me@acme.example" {
				t.Errorf("cc %q", m.Cc)
			}
			if len(m.Attachments) != 1 || m.Attachments[0].Filename != "quotation-Q20260001.pdf" ||
				!strings.HasPrefix(string(m.Attachments[0].Content), "%PDF-") {
				t.Errorf("attachments %+v", m.Attachments)
			}
			if want := func() string { k := m; k.IdempotencyKey = ""; return email.IdempotencyKey(id.String(), k) }(); m.IdempotencyKey != want {
				t.Errorf("idempotency key %q", m.IdempotencyKey)
			}
		})
	}
}

func TestQuotationSendCustomSubjectAndMessage(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, false)
	id := insertQuotation(t, a, nil, "DRAFT", nil, nil)
	apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPost, sendPath(id),
		map[string]any{"to_email": "ap@acme.example", "subject": "Hello", "message": "Custom"}), http.StatusOK)
	m := a.Mail.Messages()[0]
	if m.Subject != "Hello" || m.Text != "Custom" {
		t.Errorf("%q %q", m.Subject, m.Text)
	}
	b := newDocApp(t, false)
	id = insertQuotation(t, b, nil, "DRAFT", nil, nil)
	apitest.Decode[quotations.QuotationOut](t, b.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "ap@acme.example", "subject": ""}), http.StatusOK)
	if m := b.Mail.Messages()[0]; m.Subject != "Quotation" || !strings.Contains(m.Text, "our quotation  for") {
		t.Errorf("%q %q", m.Subject, m.Text)
	}
}

func TestQuotationSendRefusals(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, false)
	for _, s := range []string{"VOID", "INVOICED"} {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			id := insertQuotation(t, a, nil, s, nil, nil)
			got := detail(t, a, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"}, http.StatusBadRequest)
			if want := "cannot send a " + s + " quotation"; got != want {
				t.Errorf("detail %q, want %q", got, want)
			}
			if st := storedOf(t, a, id); st.Status != s || st.LastSentAt != nil {
				t.Errorf("stored %+v", st)
			}
		})
	}
	t.Run("no recipient", func(t *testing.T) {
		t.Parallel()
		c := contactCustomer(t, a, "")
		id := insertQuotation(t, a, nil, "DRAFT", &c, nil)
		got := detail(t, a, http.MethodPost, sendPath(id), map[string]any{}, http.StatusBadRequest)
		if got != "no recipient: provide to_email or set a contact_email on the customer" {
			t.Errorf("detail %q", got)
		}
		if st := storedOf(t, a, id); st.Status != "DRAFT" || st.LastSentAt != nil {
			t.Errorf("stored %+v", st)
		}
	})
	t.Run("customer fallback", func(t *testing.T) {
		t.Parallel()
		c := contactCustomer(t, a, "billing@acme.example")
		id := insertQuotation(t, a, nil, "DRAFT", &c, nil)
		apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPost, sendPath(id), map[string]any{}), http.StatusOK)
		for _, m := range a.Mail.Messages() {
			if m.To == "billing@acme.example" && strings.HasPrefix(m.Text, "Hi Acme Pte Ltd,") {
				return
			}
		}
		t.Error("no message to the customer's contact email")
	})
}

func TestQuotationSendFailure502LeavesRow(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, true)
	id := insertQuotation(t, a, nil, "DRAFT", nil, nil)
	if got := detail(t, a, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"}, http.StatusBadGateway); got != "failed to send quotation email" {
		t.Errorf("detail %q", got)
	}
	if st := storedOf(t, a, id); st.Status != "DRAFT" || st.LastSentAt != nil {
		t.Errorf("stored %+v after a failed send", st)
	}
}

func TestQuotationBadToEmail422(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, false)
	id := insertQuotation(t, a, nil, "DRAFT", nil, nil)
	rec := a.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "nope"})
	p := apitest.Decode[struct {
		Errors []struct{ Location, Message string }
	}](t, rec, http.StatusUnprocessableEntity)
	if len(p.Errors) != 1 || p.Errors[0].Location != "to_email" || p.Errors[0].Message != "invalid email address: nope" {
		t.Errorf("errors %+v", p.Errors)
	}
	if n := len(a.Mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}

func TestQuotationDocumentRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newDocApp(t, false)
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	id := insertQuotation(t, a, nil, "DRAFT", nil, nil)
	for _, c := range []struct{ method, p string }{{http.MethodGet, pdfPath(id)}, {http.MethodPost, sendPath(id)}} {
		body := map[string]any{"to_email": "a@acme.example"}
		if rec := a.Anon(t, c.method, c.p, body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", c.p, rec.Code)
		}
		if rec := staff.Do(t, c.method, c.p, body); rec.Code != http.StatusForbidden {
			t.Errorf("%s staff: %d", c.p, rec.Code)
		}
	}
	if n := len(a.Mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}
