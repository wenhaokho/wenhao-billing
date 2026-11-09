package stats_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// newApp is a migrated database with the test FX rates; stats has no routes yet.
func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(huma.API, apitest.Deps) {})
}

func exec(t *testing.T, a *apitest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func newCustomer(t *testing.T, a *apitest.App, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name) VALUES ($1, $2)`, id, name)
	return id
}

// invoice inserts a MILESTONE invoice whose amount is its balance.
func invoice(t *testing.T, a *apitest.App, customer uuid.UUID, cur, balance, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status)
VALUES ($1, $2, 'MILESTONE', $3, $4, $4, $5)`, id, customer, cur, balance, status)
	return id
}

// invoiceFull inserts an invoice with its amount apart from its balance and
// the template flag.
func invoiceFull(t *testing.T, a *apitest.App, customer uuid.UUID, cur, amount, balance, status string, template bool) {
	t.Helper()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template)
VALUES ($1, $2, 'MILESTONE', $3, $4, $5, $6, $7)`, uuid.New(), customer, cur, amount, balance, status, template)
}

func quote(t *testing.T, a *apitest.App, customer uuid.UUID, cur, amount, status string) {
	t.Helper()
	exec(t, a, `INSERT INTO quotations (quotation_id, customer_id, currency, amount, status) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), customer, cur, amount, status)
}

// payment inserts a payment with the given status and, when createdAt is not
// the zero time, that created_at.
func payment(t *testing.T, a *apitest.App, inv uuid.UUID, status string, createdAt time.Time) {
	t.Helper()
	if createdAt.IsZero() {
		createdAt = apitest.Start
	}
	exec(t, a, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, created_at)
VALUES ($1, $2, 10, 'USD', 'Acme Corp', '2026-01-01', 'TEST', $3, $4, $5)`, uuid.New(), inv, uuid.NewString(), status, createdAt)
}

// template inserts a RECURRING template with the given schedule config.
func template(t *testing.T, a *apitest.App, customer uuid.UUID, cur, amount string, cfg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template, billing_cycle_ref)
VALUES ($1, $2, 'RECURRING', $3, $4, 0, 'DRAFT', true, $5)`, uuid.New(), customer, cur, amount, raw)
}

// monthly is a MONTHLY schedule from 2026-01-01 that never ends.
func monthly() map[string]any {
	return map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-01", "end_mode": "NEVER"}
}

func with(cfg map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// today is the clock's UTC calendar date.
func today(a *apitest.App) time.Time { return platform.TodayUTC(a.Clock) }

// amounts renders a currency-amount list as "CUR amount" strings, in order.
func amounts(rows []invoices.CurrencyAmount) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Currency+" "+r.Amount.Decimal().StringFixed(4))
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// appCtx bundles the test and its app for the dashboard setups.
type appCtx struct {
	t *testing.T
	a *apitest.App
}

func (c *appCtx) customer(name string) uuid.UUID { return newCustomer(c.t, c.a, name) }

func (c *appCtx) invoice(cust uuid.UUID, cur, balance, status string) uuid.UUID {
	return invoice(c.t, c.a, cust, cur, balance, status)
}

func (c *appCtx) invoiceFull(cust uuid.UUID, cur, amount, balance, status string, tmpl bool) {
	invoiceFull(c.t, c.a, cust, cur, amount, balance, status, tmpl)
}
