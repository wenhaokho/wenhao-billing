package hosting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgertest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
)

// Ports of tests/integration/test_hosting.py (the four enforcement and
// restore tests; Cloudflare is httptest), then Go's own.

const webhookSecret = "hosting-test-webhook-secret"

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// env is a fresh database with the payment routes registered over the real
// Restorer and a fake Cloudflare.
type env struct {
	app      *apitest.App
	pool     *pgxpool.Pool
	cf       *fakeCF
	restorer Restorer
	svc      payments.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{cf: newFakeCF(t)}
	e.app = apitest.New(t, func(api huma.API, d apitest.Deps) {
		e.restorer = Restorer{Pool: d.Pool, Client: e.cf.client()}
		e.svc = payments.Service{BaseCurrency: "IDR", Hosting: e.restorer}
		cfg := d.Config
		cfg.WebhookHMACSecret = webhookSecret
		payments.Register(api, d.Pool, e.svc, cfg, d.Clock, payments.ReceiptDeps{Sender: d.Mail, Renderer: payments.StubRenderer{}})
	})
	e.pool = e.app.Pool
	return e
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// customer is Python's _mk_customer: Hosted Client, alias Hosted Client.
func (e *env) customer(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO customers (customer_id, name, matching_aliases) VALUES ($1, 'Hosted Client', '["Hosted Client"]'::jsonb)`, id)
	return id
}

// hostingItem is Python's _mk_hosting_item: grace 3 days, suspension on,
// ACTIVE, with its Cloudflare target.
func (e *env) hostingItem(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO items (item_id, name, item_type, default_currency, default_unit_price, is_sold, is_purchased, active,
is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status)
VALUES ($1, 'Acme hosting', 'SERVICE', 'USD', 240, true, false, true, true, 'example.com', 3, true, 'ACTIVE')`, id)
	e.exec(t, `INSERT INTO cloudflare_targets (target_id, item_id, zone_id, record_id, record_name, record_type, live_content,
maintenance_content, proxied) VALUES ($1, $2, 'zone_123', 'record_123', 'example.com', 'CNAME', 'live.example.net',
'maintenance.example.net', true)`, uuid.New(), id)
	return id
}

// invoice is a SENT USD invoice of amount for item, issued 2026-06-01 (the
// recurring cycle Python triggers) with its issue entry, due due.
func (e *env) invoice(t *testing.T, customer, item uuid.UUID, amount string, due time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status,
issue_date, due_date, wht_type, wht_amount, wht_booked)
VALUES ($1, $2, 'RECURRING', 'USD', $3, $3, 'SENT', '2026-06-01', $4, 'NONE', 0, 0)`, id, customer, decimal.RequireFromString(amount), due)
	e.exec(t, `INSERT INTO invoice_line_items (line_item_id, invoice_id, item_id, position, description, quantity, unit_price, amount)
VALUES ($1, $2, $3, 0, 'Acme hosting - example.com', 1, $4, $4)`, uuid.New(), id, item, decimal.RequireFromString(amount))
	e.issue(t, id)
	return id
}

// issue posts the issue entry of invoice id (PORT-NOTES 163: a payment
// needs one).
func (e *env) issue(t *testing.T, id uuid.UUID) {
	t.Helper()
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	ledgertest.IssueFixture(t, tx, id)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// fixture is Python's: customer, hosting item, and its SENT 240 invoice due due.
func (e *env) fixture(t *testing.T, due time.Time) (item, invoice uuid.UUID) {
	t.Helper()
	c := e.customer(t)
	item = e.hostingItem(t)
	return item, e.invoice(t, c, item, "240.0000", due)
}

type state struct {
	ItemStatus     string
	LastPaidAt     *time.Time
	ProviderStatus string
	LastError      *string
	LastActionAt   *time.Time
	UpdatedAt      time.Time
}

func (e *env) state(t *testing.T, item uuid.UUID) state {
	t.Helper()
	var s state
	err := e.pool.QueryRow(context.Background(), `SELECT i.hosting_status, i.hosting_last_paid_at, t.provider_status, t.last_error,
t.last_action_at, t.updated_at FROM items i JOIN cloudflare_targets t ON t.item_id = i.item_id WHERE i.item_id = $1`, item).
		Scan(&s.ItemStatus, &s.LastPaidAt, &s.ProviderStatus, &s.LastError, &s.LastActionAt, &s.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) invoiceStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE invoice_id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) enforce(t *testing.T, today time.Time) Result {
	t.Helper()
	res, err := Enforce(context.Background(), e.pool, e.cf.client(), today)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func sameOps(got []string, want ...string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// wholeSeconds reports whether ts is set at seconds precision.
func wholeSeconds(ts *time.Time) bool { return ts != nil && ts.Nanosecond() == 0 }

// test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates:
// the payment goes through ProcessIncoming (a USD 240 webhook payload), the
// restore after its commit.
func TestHostingEnforcementSuspendsAndReconciliationRestoreReactivates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, invoice := e.fixture(t, day(2026, 6, 8))

	if res := e.enforce(t, day(2026, 6, 12)); res.Suspended != 1 {
		t.Fatalf("result %+v", res)
	}
	s := e.state(t, item)
	if s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "SUSPENDED" || !wholeSeconds(s.LastActionAt) || s.LastError != nil {
		t.Fatalf("after enforcement %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123") {
		t.Fatalf("calls %v", e.cf.ops())
	}

	ref := "HOST-240"
	var p payments.PaymentOut
	err := db.InTx(context.Background(), e.pool, func(tx pgx.Tx) error {
		got, err := e.svc.ProcessIncoming(context.Background(), tx, payments.IntakePayload{
			Amount: decimal.RequireFromString("240.0000"), Currency: "USD", PayerName: "Hosted Client", PayerReference: &ref,
			PaymentDate: day(2026, 6, 12), IntakeSource: "WEBHOOK", ExternalRef: "host-240",
		})
		p = payments.PaymentOutFrom(got)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sameOps(e.cf.ops(), "suspend record_123") {
		t.Fatalf("Cloudflare called inside the payment's transaction: %v", e.cf.ops())
	}
	e.restorer.RestoreAfterCommit(context.Background(), invoice)

	if p.Status != "CLEARED" || e.invoiceStatus(t, invoice) != "PAID" {
		t.Fatalf("payment %s, invoice %s", p.Status, e.invoiceStatus(t, invoice))
	}
	s = e.state(t, item)
	if s.ItemStatus != "ACTIVE" || s.ProviderStatus != "ACTIVE" || !wholeSeconds(s.LastPaidAt) || s.LastError != nil {
		t.Errorf("after payment %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123", "restore record_123") {
		t.Errorf("calls %v", e.cf.ops())
	}
}

// test_enforcement_suspend_failure_marks_provider_error
func TestEnforcementSuspendFailureMarksProviderError(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, _ := e.fixture(t, day(2026, 6, 8))
	e.cf.setReply(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success": false, "errors": ["cloudflare 500"]}`))
	})

	if res := e.enforce(t, day(2026, 6, 12)); res != (Result{Errors: 1}) {
		t.Fatalf("result %+v", res)
	}
	s := e.state(t, item)
	if s.ItemStatus != "SUSPEND_PENDING" || s.ProviderStatus != "ERROR" || s.LastError == nil || *s.LastError != "cloudflare 500" ||
		!wholeSeconds(s.LastActionAt) || !s.UpdatedAt.Equal(*s.LastActionAt) {
		t.Errorf("state %+v", s)
	}

	// Kept: the next run retries the SUSPEND_PENDING item.
	e.cf.setReply(nil)
	if res := e.enforce(t, day(2026, 6, 13)); res != (Result{Suspended: 1}) {
		t.Fatalf("retry %+v", res)
	}
	if s := e.state(t, item); s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "SUSPENDED" || s.LastError != nil {
		t.Errorf("after retry %+v", s)
	}
}

// test_enforcement_is_idempotent_for_already_suspended_hosting_item
func TestEnforcementIsIdempotentForAlreadySuspendedHostingItem(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, _ := e.fixture(t, day(2026, 6, 8))

	if res := e.enforce(t, day(2026, 6, 12)); res.Suspended != 1 {
		t.Fatalf("first %+v", res)
	}
	first := e.state(t, item)
	if res := e.enforce(t, day(2026, 6, 13)); res != (Result{}) {
		t.Fatalf("second %+v", res)
	}
	if !sameOps(e.cf.ops(), "suspend record_123") {
		t.Errorf("calls %v", e.cf.ops())
	}
	s := e.state(t, item)
	if s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "SUSPENDED" || !s.LastActionAt.Equal(*first.LastActionAt) {
		t.Errorf("state %+v, first %+v", s, first)
	}
}

// test_enforcement_ignores_non_overdue_invoice, plus the boundary: due +
// grace equal to today is not overdue (Python's strict <).
func TestEnforcementIgnoresNonOverdueInvoice(t *testing.T) {
	t.Parallel()
	for name, due := range map[string]time.Time{"python": day(2026, 6, 15), "grace ends today": day(2026, 6, 9)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			item, _ := e.fixture(t, due)
			if res := e.enforce(t, day(2026, 6, 12)); res.Suspended != 0 {
				t.Errorf("result %+v", res)
			}
			if s := e.state(t, item); s.ItemStatus != "ACTIVE" {
				t.Errorf("state %+v", s)
			}
			if ops := e.cf.ops(); len(ops) != 0 {
				t.Errorf("calls %v", ops)
			}
		})
	}
}

// One item's failure does not stop the run: items are processed oldest
// first and each counts on its own.
func TestEnforcementContinuesAfterItemError(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.customer(t)
	broken := e.hostingItem(t)
	e.exec(t, `UPDATE items SET created_at = '2026-01-01' WHERE item_id = $1`, broken)
	e.exec(t, `DELETE FROM cloudflare_targets WHERE item_id = $1`, broken) // "hosting item is missing a Cloudflare target"
	e.invoice(t, c, broken, "240.0000", day(2026, 6, 8))
	ok := e.hostingItem(t)
	e.exec(t, `UPDATE items SET created_at = '2026-02-01' WHERE item_id = $1`, ok)
	e.invoice(t, c, ok, "240.0000", day(2026, 6, 8))

	if res := e.enforce(t, day(2026, 6, 12)); res != (Result{Suspended: 1, Errors: 1}) {
		t.Fatalf("result %+v", res)
	}
	if s := e.state(t, ok); s.ItemStatus != "SUSPENDED" {
		t.Errorf("second item %+v", s)
	}
}

// The enforcement job restores a suspended item whose invoices are all paid.
func TestEnforcementRestoresPaidItem(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, invoice := e.fixture(t, day(2026, 6, 8))
	e.enforce(t, day(2026, 6, 12))
	e.exec(t, `UPDATE invoices SET balance_due = 0, status = 'PAID' WHERE invoice_id = $1`, invoice)
	if res := e.enforce(t, day(2026, 6, 13)); res != (Result{Restored: 1}) {
		t.Fatalf("result %+v", res)
	}
	if s := e.state(t, item); s.ItemStatus != "ACTIVE" || s.ProviderStatus != "ACTIVE" {
		t.Errorf("state %+v", s)
	}
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(webhookSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func (e *env) webhook(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/payments/intake/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sign(body))
	rec := httptest.NewRecorder()
	e.app.Handler.ServeHTTP(rec, req)
	return rec
}

func hook(payer, ref string) string {
	return `{"amount":"240.0000","currency":"usd","payer_name":"` + payer + `","payment_date":"2026-06-12","external_ref":"` + ref + `"}`
}

// Each of the three paid paths restores the suspended hosting after its
// transaction commits.
func TestPaymentRoutesRestoreAfterCommit(t *testing.T) {
	t.Parallel()
	paths := map[string]func(t *testing.T, e *env, invoice uuid.UUID){
		"webhook": func(t *testing.T, e *env, _ uuid.UUID) {
			p := apitest.Decode[payments.PaymentOut](t, e.webhook(t, hook("Hosted Client", "w-1")), http.StatusCreated)
			if p.Status != "CLEARED" {
				t.Fatalf("payment %s", p.Status)
			}
		},
		"record-payment": func(t *testing.T, e *env, invoice uuid.UUID) {
			rec := e.app.Do(t, http.MethodPost, "/api/v1/invoices/"+invoice.String()+"/record-payment",
				map[string]any{"amount": "240", "payment_date": "2026-06-12", "payer_name": "Hosted Client"})
			apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
		},
		"manual-review": func(t *testing.T, e *env, invoice uuid.UUID) {
			held := apitest.Decode[payments.PaymentOut](t, e.webhook(t, hook("Somebody Else Entirely", "w-2")), http.StatusCreated)
			if held.Status == "CLEARED" {
				t.Fatalf("payment cleared without review")
			}
			if s := e.invoiceStatus(t, invoice); s != "SENT" {
				t.Fatalf("invoice %s before review", s)
			}
			rec := e.app.Do(t, http.MethodPost, "/api/v1/payments/"+held.PaymentID.String()+"/manual-review",
				map[string]any{"invoice_id": invoice})
			if p := apitest.Decode[payments.PaymentOut](t, rec, http.StatusOK); p.Status != "CLEARED" {
				t.Fatalf("approved %s", p.Status)
			}
		},
	}
	for name, pay := range paths {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			item, invoice := e.fixture(t, day(2026, 6, 8))
			e.enforce(t, day(2026, 6, 12))
			pay(t, e, invoice)
			if s := e.invoiceStatus(t, invoice); s != "PAID" {
				t.Fatalf("invoice %s", s)
			}
			s := e.state(t, item)
			if s.ItemStatus != "ACTIVE" || s.ProviderStatus != "ACTIVE" || !wholeSeconds(s.LastPaidAt) {
				t.Errorf("state %+v", s)
			}
			if !sameOps(e.cf.ops(), "suspend record_123", "restore record_123") {
				t.Errorf("calls %v", e.cf.ops())
			}
		})
	}
}

// Go change 1: Cloudflare down never fails a payment. The payment commits,
// hosting_last_paid_at is stamped, and the failed restore marks the target
// ERROR (the daily job retries).
func TestPaymentSucceedsWhenCloudflareDown(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, invoice := e.fixture(t, day(2026, 6, 8))
	e.enforce(t, day(2026, 6, 12))
	before := e.state(t, item)
	e.cf.down()

	rec := e.app.Do(t, http.MethodPost, "/api/v1/invoices/"+invoice.String()+"/record-payment",
		map[string]any{"amount": "240", "payment_date": "2026-06-12", "payer_name": "Hosted Client"})
	apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
	if s := e.invoiceStatus(t, invoice); s != "PAID" {
		t.Fatalf("invoice %s", s)
	}
	s := e.state(t, item)
	want := "Server error '500 Internal Server Error' for url '" + e.cf.srv.URL + "/zones/zone_123/dns_records/record_123'\n" +
		"For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/500"
	if s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "ERROR" || s.LastError == nil || *s.LastError != want ||
		!wholeSeconds(s.LastPaidAt) || !wholeSeconds(s.LastActionAt) || s.LastActionAt.Before(*before.LastActionAt) {
		t.Errorf("state %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123", "restore record_123") {
		t.Errorf("calls %v", e.cf.ops())
	}

	// The daily job retries once Cloudflare is back.
	e.cf.setReply(nil)
	if res := e.enforce(t, day(2026, 6, 13)); res != (Result{Restored: 1}) {
		t.Fatalf("retry %+v", res)
	}
	if s := e.state(t, item); s.ItemStatus != "ACTIVE" || s.ProviderStatus != "ACTIVE" || s.LastError != nil {
		t.Errorf("after retry %+v", s)
	}
}

// Go change 2: a paid invoice restores neither a CANCELLED item nor one
// whose suspension is off (a hand-set suspension stays).
func TestRestoreSkipsCancelledAndDisabled(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]string{
		"cancelled":          `hosting_status = 'CANCELLED'`,
		"suspension off":     `hosting_status = 'SUSPENDED', hosting_suspension_enabled = false`,
		"suspension not set": `hosting_status = 'SUSPENDED', hosting_suspension_enabled = NULL`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			item, invoice := e.fixture(t, day(2026, 6, 8))
			e.exec(t, `UPDATE items SET `+set+` WHERE item_id = $1`, item)
			e.exec(t, `UPDATE cloudflare_targets SET provider_status = 'SUSPENDED' WHERE item_id = $1`, item)
			before := e.state(t, item)

			rec := e.app.Do(t, http.MethodPost, "/api/v1/invoices/"+invoice.String()+"/record-payment",
				map[string]any{"amount": "240", "payment_date": "2026-06-12", "payer_name": "Hosted Client"})
			apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
			if ops := e.cf.ops(); len(ops) != 0 {
				t.Errorf("calls %v", ops)
			}
			s := e.state(t, item)
			if s.ItemStatus != before.ItemStatus || s.ProviderStatus != "SUSPENDED" || !wholeSeconds(s.LastPaidAt) {
				t.Errorf("state %+v", s)
			}
		})
	}
}

// Python's _has_open_balance: an item billed on two open invoices is
// restored only when the last one is paid.
func TestRestoreWaitsForAllOpenInvoices(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.customer(t)
	item := e.hostingItem(t)
	first := e.invoice(t, c, item, "240.0000", day(2026, 6, 8))
	second := e.invoice(t, c, item, "100.0000", day(2026, 7, 8))
	e.enforce(t, day(2026, 6, 12))

	pay := func(invoice uuid.UUID, amount string) {
		rec := e.app.Do(t, http.MethodPost, "/api/v1/invoices/"+invoice.String()+"/record-payment",
			map[string]any{"amount": amount, "payment_date": "2026-06-12", "payer_name": "Hosted Client"})
		apitest.Decode[payments.PaymentOut](t, rec, http.StatusCreated)
	}
	pay(first, "240")
	if s := e.state(t, item); s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "SUSPENDED" || !wholeSeconds(s.LastPaidAt) {
		t.Fatalf("after the first payment %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123") {
		t.Fatalf("calls %v", e.cf.ops())
	}
	pay(second, "100")
	if s := e.state(t, item); s.ItemStatus != "ACTIVE" || s.ProviderStatus != "ACTIVE" {
		t.Errorf("after the second payment %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123", "restore record_123") {
		t.Errorf("calls %v", e.cf.ops())
	}
}

// A Restorer with a nil Client fails like a missing token instead of
// panicking: the target goes ERROR, the item stays suspended.
func TestRestoreWithNilClientIsMissingToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	item, invoice := e.fixture(t, day(2026, 6, 8))
	e.enforce(t, day(2026, 6, 12))
	e.exec(t, `UPDATE invoices SET balance_due = 0, status = 'PAID' WHERE invoice_id = $1`, invoice)

	Restorer{Pool: e.pool}.RestoreAfterCommit(context.Background(), invoice)

	s := e.state(t, item)
	if s.ItemStatus != "SUSPENDED" || s.ProviderStatus != "ERROR" || s.LastError == nil || *s.LastError != "cloudflare_api_token is not configured" {
		t.Errorf("state %+v", s)
	}
	if !sameOps(e.cf.ops(), "suspend record_123") {
		t.Errorf("calls %v", e.cf.ops())
	}
}
