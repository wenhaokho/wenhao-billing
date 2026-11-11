package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

func newReminderApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(huma.API, apitest.Deps) {})
}

// inTx runs fn in a transaction it commits when fn succeeds.
func inTx(t *testing.T, a *apitest.App, fn func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// remind prepares and sends a reminder for id at now, as the admin over REST.
func remind(t *testing.T, a *apitest.App, id uuid.UUID, force bool, now time.Time) (Reminder, error) {
	t.Helper()
	to := "ap@acme.example"
	in := ReminderIn{InvoiceID: id, ToEmail: &to, Force: force, Channel: "REST", ActorUserID: &a.AdminID}
	var out Reminder
	err := inTx(t, a, func(tx pgx.Tx) error {
		d, err := PrepareReminder(t.Context(), tx, in, now)
		if err != nil {
			return err
		}
		out, err = SendReminder(t.Context(), tx, a.Mail, d, in, now)
		return err
	})
	return out, err
}

func TestReminderRefusals(t *testing.T) {
	t.Parallel()
	a := newReminderApp(t)
	cust := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, c := range []struct {
		name, status string
		template     bool
		want         string
	}{
		{"void", "VOID", false, "cannot remind on a VOID invoice"},
		{"paid", "PAID", false, "cannot remind on a PAID invoice"},
		{"draft", "DRAFT", false, "finalize the invoice before sending it"},
		{"template", "DRAFT", true, "cannot send a recurring template"},
	} {
		r := newRow(cust)
		r.status, r.template = c.status, c.template
		id := insertRow(t, a.Pool, r)
		_, err := remind(t, a, id, false, a.Clock.Now())
		if err == nil || !errors.Is(err, ErrInvoicing) || err.Error() != c.want {
			t.Errorf("%s: err %v, want the refusal %q", c.name, err, c.want)
		}
	}
	// No recipient: none passed and the customer has no contact email.
	id := insertRow(t, a.Pool, newRow(cust))
	err := inTx(t, a, func(tx pgx.Tx) error {
		_, err := PrepareReminder(t.Context(), tx, ReminderIn{InvoiceID: id, Channel: "REST"}, a.Clock.Now())
		return err
	})
	if err == nil || !errors.Is(err, ErrInvoicing) || err.Error() != "no recipient: pass to_email or set customer contact_email" {
		t.Errorf("no recipient: err %v", err)
	}
	// An unknown invoice.
	err = inTx(t, a, func(tx pgx.Tx) error {
		_, err := PrepareReminder(t.Context(), tx, ReminderIn{InvoiceID: uuid.New(), Channel: "REST"}, a.Clock.Now())
		return err
	})
	if err == nil || !errors.Is(err, ErrInvoicing) || err.Error() != "invoice not found" {
		t.Errorf("unknown invoice: err %v", err)
	}
	if n := len(a.Mail.Messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	if n := countReminders(t, a); n != 0 {
		t.Fatalf("%d reminder rows, want 0", n)
	}
}

func countReminders(t *testing.T, a *apitest.App) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM invoice_reminders`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReminderGuardPerCustomer(t *testing.T) {
	t.Parallel()
	a := newReminderApp(t)
	acme := insertCustomer(t, a.Pool, "NONE", "", false)
	other := insertCustomer(t, a.Pool, "NONE", "", false)
	rx := newRow(acme)
	x := insertRow(t, a.Pool, rx)
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET invoice_number = 'INV-X' WHERE invoice_id = $1`, x); err != nil {
		t.Fatal(err)
	}
	y := insertRow(t, a.Pool, newRow(acme))
	z := insertRow(t, a.Pool, newRow(other))
	t0 := a.Clock.Now()

	first, err := remind(t, a, x, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	if first.InvoiceID != x || first.Channel != "REST" || first.Recipient != "ap@acme.example" ||
		first.SentByUserID == nil || *first.SentByUserID != a.AdminID || !first.SentAt.Equal(t0) || first.Currency != "USD" {
		t.Fatalf("first reminder %+v", first)
	}

	// Invoice Y of the same customer an hour later: refused, naming X.
	_, err = remind(t, a, y, false, t0.Add(time.Hour))
	var recent *RecentReminderError
	if !errors.As(err, &recent) {
		t.Fatalf("same customer within 24 h: err %v, want RecentReminderError", err)
	}
	if recent.InvoiceID != x || recent.InvoiceNumber == nil || *recent.InvoiceNumber != "INV-X" || !recent.SentAt.Equal(t0) {
		t.Fatalf("recent %+v", recent)
	}
	if msg := err.Error(); !strings.Contains(msg, "INV-X") || !strings.Contains(msg, t0.UTC().Format(time.RFC3339)) {
		t.Fatalf("message %q does not name the invoice and time", msg)
	}

	// With force it goes.
	if _, err := remind(t, a, y, true, t0.Add(time.Hour)); err != nil {
		t.Fatalf("forced: %v", err)
	}
	// Another customer is not guarded.
	if _, err := remind(t, a, z, false, t0.Add(time.Hour)); err != nil {
		t.Fatalf("other customer: %v", err)
	}
	// 25 h after the last reminder (the forced one) the customer is free.
	a.Clock.Advance(26 * time.Hour)
	if _, err := remind(t, a, y, false, a.Clock.Now()); err != nil {
		t.Fatalf("25 h later: %v", err)
	}
	if n := len(a.Mail.Messages()); n != 4 {
		t.Fatalf("%d emails, want 4", n)
	}

	list, err := ListReminders(t.Context(), a.Pool, y)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].SentAt.After(list[1].SentAt.Time) {
		t.Fatalf("history of Y %+v, want 2 latest first", list)
	}
	last, err := LastReminderByCustomer(t.Context(), a.Pool, []uuid.UUID{acme, other, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || !last[acme].Equal(a.Clock.Now()) || !last[other].Equal(t0.Add(time.Hour)) {
		t.Fatalf("last by customer %v", last)
	}
}

// An invoice with no customer is guarded on its own reminders only.
func TestReminderGuardWithoutCustomer(t *testing.T) {
	t.Parallel()
	a := newReminderApp(t)
	noCust := func() uuid.UUID {
		id := uuid.New()
		if _, err := a.Pool.Exec(t.Context(), `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status)
VALUES ($1, 'MILESTONE', 'USD', 100, 100, 'SENT')`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	p, q := noCust(), noCust()
	t0 := a.Clock.Now()
	if _, err := remind(t, a, p, false, t0); err != nil {
		t.Fatal(err)
	}
	var recent *RecentReminderError
	if _, err := remind(t, a, p, false, t0.Add(time.Minute)); !errors.As(err, &recent) || recent.InvoiceID != p {
		t.Fatalf("same invoice: err %v, want RecentReminderError", err)
	}
	if _, err := remind(t, a, q, false, t0.Add(time.Minute)); err != nil {
		t.Fatalf("another customerless invoice: %v", err)
	}
}

func TestReminderAmountExpectedCash(t *testing.T) {
	t.Parallel()
	a := newReminderApp(t)
	cust := insertCustomer(t, a.Pool, "NONE", "", false)
	r := newRow(cust)
	r.amount, r.balance = "100", "100"
	id := insertRow(t, a.Pool, r)
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET invoice_number = 'INV-W', wht_type = 'PPH23', wht_rate = 2, wht_amount = 2, wht_booked = 0 WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	got, err := remind(t, a, id, false, a.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s := got.Amount.Decimal().StringFixed(4); s != "98.0000" {
		t.Fatalf("amount %s, want 98.0000", s)
	}
	var stored string
	if err := a.Pool.QueryRow(t.Context(), `SELECT amount::text FROM invoice_reminders WHERE invoice_id = $1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "98.0000" {
		t.Fatalf("stored amount %s, want 98.0000", stored)
	}
	msgs := a.Mail.Messages()
	if len(msgs) != 1 || msgs[0].To != "ap@acme.example" || msgs[0].Subject != "Payment reminder: invoice INV-W" ||
		msgs[0].Text != "This is a reminder that invoice INV-W has an outstanding balance of 98.0000 USD." || msgs[0].IdempotencyKey == "" {
		t.Fatalf("messages %+v", msgs)
	}
}

func reminderApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		RegisterReminders(api, d.Pool, d.Clock, d.Mail)
	})
}

func remindersPath(id uuid.UUID) string { return "/api/v1/invoices/" + id.String() + "/reminders" }

func TestReminderRoutes(t *testing.T) {
	t.Parallel()
	a := reminderApp(t)
	acme := docCustomer(t, a, "ap@acme.example")
	x := docInv{number: str("INV-X"), customer: &acme, status: "SENT"}.insert(t, a)
	y := docInv{number: str("INV-Y"), customer: &acme, status: "SENT"}.insert(t, a)
	paid := docInv{status: "PAID", balance: "0"}.insert(t, a)

	rec := a.Do(t, http.MethodPost, remindersPath(x), map[string]any{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	var first Reminder
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.InvoiceID != x || first.Channel != "REST" || first.Recipient != "ap@acme.example" || first.SentByUserID == nil || *first.SentByUserID != a.AdminID {
		t.Errorf("first %+v", first)
	}
	if n := len(a.Mail.Messages()); n != 1 {
		t.Fatalf("%d emails, want 1", n)
	}

	// Same customer, other invoice: 409 naming INV-X.
	rec = a.Do(t, http.MethodPost, remindersPath(y), map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("guard: %d %s", rec.Code, rec.Body)
	}
	for _, want := range []string{"INV-X", "invoice_number", "sent_at", "reminder_recent"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("409 body lacks %q: %s", want, rec.Body)
		}
	}
	if n := len(a.Mail.Messages()); n != 1 {
		t.Fatalf("guarded send emailed: %d", n)
	}

	// Forced: 201.
	rec = a.Do(t, http.MethodPost, remindersPath(y), map[string]any{"force": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("forced: %d %s", rec.Code, rec.Body)
	}

	// PAID: 422 with the refusal sentence.
	rec = a.Do(t, http.MethodPost, remindersPath(paid), map[string]any{"to_email": "a@b.example"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cannot remind on a PAID invoice") {
		t.Errorf("paid: %d %s", rec.Code, rec.Body)
	}
	// Unknown: 404.
	rec = a.Do(t, http.MethodPost, remindersPath(uuid.New()), map[string]any{"to_email": "a@b.example"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d %s", rec.Code, rec.Body)
	}

	// History: newest first, per invoice.
	a.Clock.Advance(time.Hour)
	rec = a.Do(t, http.MethodPost, remindersPath(x), map[string]any{"force": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("third: %d %s", rec.Code, rec.Body)
	}
	rec = a.Do(t, http.MethodGet, remindersPath(x), nil)
	var list []Reminder
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s %v", rec.Code, rec.Body, err)
	}
	if len(list) != 2 || !list[0].SentAt.After(list[1].SentAt.Time) || list[1].ReminderID != first.ReminderID {
		t.Errorf("list %+v", list)
	}
	if rec = a.Do(t, http.MethodGet, remindersPath(uuid.New()), nil); rec.Code != http.StatusNotFound {
		t.Errorf("list unknown: %d", rec.Code)
	}
}
