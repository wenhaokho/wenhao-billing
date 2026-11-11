package reports_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports"
)

const (
	arPath = "/api/v1/accounting/reports/ar-aging"
	apPath = "/api/v1/accounting/reports/ap-aging"
	asOf   = "2026-03-31"
)

type agingBuckets struct {
	Current string `json:"current"`
	D1To30  string `json:"d_1_30"`
	D31To60 string `json:"d_31_60"`
	D61To90 string `json:"d_61_90"`
	D90Plus string `json:"d_90_plus"`
	Total   string `json:"total"`
}

type agingRow struct {
	PartyID   string       `json:"party_id"`
	PartyName string       `json:"party_name"`
	Currency  string       `json:"currency"`
	Buckets   agingBuckets `json:"buckets"`
}

type agingReport struct {
	AsOf             string                  `json:"as_of"`
	Kind             string                  `json:"kind"`
	Rows             []agingRow              `json:"rows"`
	TotalsByCurrency map[string]agingBuckets `json:"totals_by_currency"`
}

func getAging(t *testing.T, a *apitest.App, path, query string) (agingReport, string) {
	t.Helper()
	rec := a.Do(t, http.MethodGet, path+query, nil)
	return apitest.Decode[agingReport](t, rec, http.StatusOK), rec.Body.String()
}

func newCustomer(t *testing.T, a *apitest.App, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name) VALUES ($1, $2)`, id, name)
	return id
}

func newVendor(t *testing.T, a *apitest.App, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO vendors (vendor_id, name) VALUES ($1, $2)`, id, name)
	return id
}

// dueDays is a due date days before as_of (negative: after it).
func dueDays(days int) *time.Time {
	d := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
	return &d
}

// invoice inserts one invoice; a nil customer leaves it without one.
func invoice(t *testing.T, a *apitest.App, customer *uuid.UUID, cur, balance, status string, due *time.Time, template bool) {
	t.Helper()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, due_date, is_template)
VALUES ($1, $2, 'MILESTONE', $3, $4, $4, $5, $6, $7)`, uuid.New(), customer, cur, balance, status, due, template)
}

func bill(t *testing.T, a *apitest.App, vendor uuid.UUID, cur, balance, status string, due *time.Time) {
	t.Helper()
	exec(t, a, `INSERT INTO bills (bill_id, vendor_id, currency, amount, balance_due, status, due_date)
VALUES ($1, $2, $3, $4, $4, $5, $6)`, uuid.New(), vendor, cur, balance, status, due)
}

// which names the buckets holding a non-zero amount.
func (b agingBuckets) which() string {
	var out []string
	for _, p := range []struct{ name, v string }{
		{"current", b.Current}, {"d_1_30", b.D1To30}, {"d_31_60", b.D31To60}, {"d_61_90", b.D61To90}, {"d_90_plus", b.D90Plus},
	} {
		if p.v != "0.0000" {
			out = append(out, p.name)
		}
	}
	return strings.Join(out, ",")
}

func TestAgingBucketEdges(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	edges := []struct {
		days   int
		bucket string
	}{
		{-5, "current"},
		{0, "current"},
		{1, "d_1_30"},
		{30, "d_1_30"},
		{31, "d_31_60"},
		{60, "d_31_60"},
		{61, "d_61_90"},
		{90, "d_61_90"},
		{91, "d_90_plus"},
	}
	want := map[string]string{}
	for _, e := range edges {
		name := fmt.Sprintf("edge %d", e.days)
		c := newCustomer(t, a, name)
		invoice(t, a, &c, "USD", "100", "OPEN", dueDays(e.days), false)
		want[name] = e.bucket
	}
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	if len(got.Rows) != len(edges) {
		t.Fatalf("rows %d, want %d: %+v", len(got.Rows), len(edges), got.Rows)
	}
	for _, r := range got.Rows {
		if r.Buckets.Total != "100.0000" || r.Buckets.which() != want[r.PartyName] {
			t.Errorf("%s: %+v, want only %s", r.PartyName, r.Buckets, want[r.PartyName])
		}
	}
}

func TestAgingNullDueDateIsCurrent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	invoice(t, a, &c, "IDR", "250.5", "OPEN", nil, false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	if len(got.Rows) != 1 || got.Rows[0].Buckets.Current != "250.5000" || got.Rows[0].Buckets.Total != "250.5000" || got.Rows[0].Buckets.which() != "current" {
		t.Errorf("rows %+v", got.Rows)
	}
}

func TestAgingGroupsPerCustomerAndCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	acme, beta := newCustomer(t, a, "Acme"), newCustomer(t, a, "Beta")
	invoice(t, a, &acme, "USD", "100", "OPEN", dueDays(10), false)
	invoice(t, a, &acme, "USD", "50", "PARTIAL", dueDays(40), false)
	invoice(t, a, &acme, "SGD", "7", "SENT", dueDays(0), false)
	invoice(t, a, &beta, "USD", "1", "OPEN", dueDays(100), false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	if got.Kind != "AR" || got.AsOf != asOf || len(got.Rows) != 3 {
		t.Fatalf("got %+v", got)
	}
	type k struct{ name, cur string }
	by := map[k]agingRow{}
	for _, r := range got.Rows {
		by[k{r.PartyName, r.Currency}] = r
	}
	if r := by[k{"Acme", "USD"}]; r.PartyID != acme.String() || r.Buckets.D1To30 != "100.0000" || r.Buckets.D31To60 != "50.0000" || r.Buckets.Total != "150.0000" || r.Buckets.Current != "0.0000" {
		t.Errorf("Acme USD %+v", r)
	}
	if r := by[k{"Acme", "SGD"}]; r.Buckets.Current != "7.0000" || r.Buckets.Total != "7.0000" {
		t.Errorf("Acme SGD %+v", r)
	}
	if r := by[k{"Beta", "USD"}]; r.PartyID != beta.String() || r.Buckets.D90Plus != "1.0000" {
		t.Errorf("Beta USD %+v", r)
	}
}

func TestAgingTotalsByCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	acme, beta := newCustomer(t, a, "Acme"), newCustomer(t, a, "Beta")
	invoice(t, a, &acme, "USD", "100", "OPEN", dueDays(10), false)
	invoice(t, a, &beta, "USD", "25.25", "OPEN", dueDays(-3), false)
	invoice(t, a, &beta, "SGD", "9", "OPEN", dueDays(95), false)
	got, body := getAging(t, a, arPath, "?as_of="+asOf)
	if u := got.TotalsByCurrency["USD"]; u.Current != "25.2500" || u.D1To30 != "100.0000" || u.Total != "125.2500" {
		t.Errorf("USD %+v", u)
	}
	if s := got.TotalsByCurrency["SGD"]; s.D90Plus != "9.0000" || s.Total != "9.0000" {
		t.Errorf("SGD %+v", s)
	}
	// An object keyed by currency, keys in currency order, buckets in field order.
	_, totals, _ := strings.Cut(body, `"totals_by_currency"`)
	if i, j := strings.Index(totals, `"SGD"`), strings.Index(totals, `"USD"`); i < 0 || j < i {
		t.Errorf("keys not in currency order: %s", body)
	}
	if !strings.Contains(body, `"current":"25.2500","d_1_30":"100.0000","d_31_60":"0.0000","d_61_90":"0.0000","d_90_plus":"0.0000","total":"125.2500"`) {
		t.Errorf("bucket field order: %s", body)
	}
}

func TestAgingExcludesDraftTemplateAndZeroBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	invoice(t, a, &c, "USD", "100", "OPEN", dueDays(1), false)
	invoice(t, a, &c, "USD", "1000", "DRAFT", dueDays(1), false)
	invoice(t, a, &c, "USD", "2000", "PAID", dueDays(1), false)
	invoice(t, a, &c, "USD", "4000", "VOID", dueDays(1), false)
	invoice(t, a, &c, "USD", "8000", "OPEN", dueDays(1), true)
	invoice(t, a, &c, "USD", "0", "OPEN", dueDays(1), false)
	invoice(t, a, &c, "USD", "-5", "PARTIAL", dueDays(1), false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	if len(got.Rows) != 1 || got.Rows[0].Buckets.Total != "100.0000" || got.TotalsByCurrency["USD"].Total != "100.0000" {
		t.Errorf("got %+v", got)
	}
}

func TestAgingNoCustomerRow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	invoice(t, a, nil, "USD", "30", "OPEN", dueDays(2), false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	if len(got.Rows) != 1 || got.Rows[0].PartyID != "-" || got.Rows[0].PartyName != "(no customer)" || got.Rows[0].Buckets.D1To30 != "30.0000" {
		t.Errorf("rows %+v", got.Rows)
	}
}

func TestAPAgingExcludesSent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	v := newVendor(t, a, "Supplier")
	bill(t, a, v, "IDR", "10", "OPEN", dueDays(5))
	bill(t, a, v, "IDR", "20", "PARTIAL", dueDays(35))
	bill(t, a, v, "IDR", "400", "SENT", dueDays(5))
	bill(t, a, v, "IDR", "800", "DRAFT", dueDays(5))
	bill(t, a, v, "IDR", "1600", "PAID", dueDays(5))
	bill(t, a, v, "IDR", "0", "OPEN", dueDays(5))
	got, _ := getAging(t, a, apPath, "?as_of="+asOf)
	if got.Kind != "AP" || len(got.Rows) != 1 {
		t.Fatalf("got %+v", got)
	}
	r := got.Rows[0]
	if r.PartyID != v.String() || r.PartyName != "Supplier" || r.Buckets.D1To30 != "10.0000" || r.Buckets.D31To60 != "20.0000" || r.Buckets.Total != "30.0000" {
		t.Errorf("row %+v", r)
	}
	if got.TotalsByCurrency["IDR"].Total != "30.0000" {
		t.Errorf("totals %+v", got.TotalsByCurrency)
	}
}

func TestAgingOrderDeterministic(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Case-insensitive name order, then currency, then party id for equal names.
	zed, apple, bob := newCustomer(t, a, "zed"), newCustomer(t, a, "Apple"), newCustomer(t, a, "bob")
	dupUpper, dupLower := newCustomer(t, a, "Dup"), newCustomer(t, a, "dup")
	for _, c := range []uuid.UUID{zed, apple, bob, dupUpper, dupLower} {
		invoice(t, a, &c, "USD", "1", "OPEN", dueDays(0), false)
	}
	invoice(t, a, &apple, "IDR", "1", "OPEN", dueDays(0), false)
	invoice(t, a, nil, "USD", "1", "OPEN", dueDays(0), false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	var names []string
	for _, r := range got.Rows {
		names = append(names, r.PartyName+"/"+r.Currency)
	}
	dupFirst, dupSecond := "Dup/USD", "dup/USD"
	if dupLower.String() < dupUpper.String() {
		dupFirst, dupSecond = dupSecond, dupFirst
	}
	want := []string{"(no customer)/USD", "Apple/IDR", "Apple/USD", "bob/USD", dupFirst, dupSecond, "zed/USD"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order %v, want %v", names, want)
	}
	again, _ := getAging(t, a, arPath, "?as_of="+asOf)
	b1, _ := json.Marshal(got)
	b2, _ := json.Marshal(again)
	if string(b1) != string(b2) {
		t.Errorf("two calls differ")
	}
}

func TestAgingEmpty(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, path := range []string{arPath, apPath} {
		_, body := getAging(t, a, path, "?as_of="+asOf)
		if !strings.Contains(body, `"rows":[]`) || !strings.Contains(body, `"totals_by_currency":{}`) {
			t.Errorf("%s: %s", path, body)
		}
	}
}

func TestAgingUsesAsOf(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	// Due on as_of: current that day, 10 days past due ten days later.
	invoice(t, a, &c, "USD", "5", "OPEN", dueDays(0), false)
	if r, _ := getAging(t, a, arPath, "?as_of=2026-04-10"); len(r.Rows) != 1 || r.Rows[0].Buckets.which() != "d_1_30" || r.AsOf != "2026-04-10" {
		t.Errorf("%+v", r)
	}
	if r, _ := getAging(t, a, arPath, ""); r.AsOf != a.Clock.Now().UTC().Format("2006-01-02") {
		t.Errorf("default as_of %s", r.AsOf)
	}
}

func TestAgingRequiresAdminAndValidatesAsOf(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	for _, path := range []string{arPath, apPath} {
		if p := apitest.Decode[problem](t, a.Anon(t, http.MethodGet, path, nil), http.StatusUnauthorized); p.Detail != "not authenticated" {
			t.Errorf("%s anon: %q", path, p.Detail)
		}
		if p := apitest.Decode[problem](t, staff.Do(t, http.MethodGet, path, nil), http.StatusForbidden); p.Detail != "admin required" {
			t.Errorf("%s staff: %q", path, p.Detail)
		}
		apitest.Decode[problem](t, a.Do(t, http.MethodGet, path+"?as_of=2026-13-45", nil), http.StatusUnprocessableEntity)
	}
}

func TestAgingEmptyCustomerNameShowsNoCustomer(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "")
	invoice(t, a, &c, "USD", "12", "OPEN", dueDays(1), false)
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	// Python's `name or "(no customer)"`: an empty name falls back, the id stays.
	if len(got.Rows) != 1 || got.Rows[0].PartyName != "(no customer)" || got.Rows[0].PartyID != c.String() {
		t.Errorf("rows %+v", got.Rows)
	}
}

func TestAgingSameCustomerCurrenciesOrderedByCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Several customers with three currencies each, so a missing tie-break
	// cannot pass on map iteration order by luck.
	for _, name := range []string{"Acme", "Beta", "Gamma", "Delta", "Eps"} {
		c := newCustomer(t, a, name)
		for _, cur := range []string{"USD", "SGD", "IDR"} {
			invoice(t, a, &c, cur, "1", "OPEN", dueDays(0), false)
		}
	}
	got, _ := getAging(t, a, arPath, "?as_of="+asOf)
	for i := 0; i+2 < len(got.Rows); i += 3 {
		if got.Rows[i].Currency != "IDR" || got.Rows[i+1].Currency != "SGD" || got.Rows[i+2].Currency != "USD" {
			t.Fatalf("rows %d..%d: %+v", i, i+2, got.Rows[i:i+3])
		}
	}
}

// AP aging follows bill payments and their reversals (PORT-NOTES 165), and
// agrees with the payable on the ledger.
func TestApAgingAfterBillPaymentAndReversal(t *testing.T) {
	t.Parallel()
	cfg := platform.Config{BaseCurrency: "IDR"}
	a := apitest.New(t, func(api huma.API, d apitest.Deps) {
		reports.Register(api, d.Pool, cfg, d.Clock)
		bills.Register(api, d.Pool, d.Clock, cfg.BaseCurrency)
	})
	v := newVendor(t, a, "Supplier")
	created := apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, "/api/v1/bills", map[string]any{
		"vendor_id": v, "currency": "IDR", "issue_date": "2026-01-10", "due_date": dueDays(5).Format(time.DateOnly),
		"line_items": []any{map[string]any{"description": "Work", "quantity": 1, "unit_price": 1000}},
	}), http.StatusCreated)
	billPath := "/api/v1/bills/" + created.BillID.String()
	apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPatch, billPath, map[string]any{"status": "OPEN"}), http.StatusOK)

	wantAP := func(total string) {
		t.Helper()
		got, _ := getAging(t, a, apPath, "?as_of="+asOf)
		have := "0.0000"
		if len(got.Rows) == 1 {
			have = got.Rows[0].Buckets.Total
		} else if len(got.Rows) > 1 {
			t.Fatalf("rows %+v", got.Rows)
		}
		if have != total {
			t.Errorf("AP aging total %s, want %s", have, total)
		}
		var ledgerAP string
		if err := a.Pool.QueryRow(t.Context(), `SELECT COALESCE(sum(l.base_amount_credit - l.base_amount_debit), 0)::numeric(19,4)::text
FROM journal_lines l JOIN chart_of_accounts c ON c.account_id = l.account_id WHERE c.code = '2000'`).Scan(&ledgerAP); err != nil {
			t.Fatal(err)
		}
		if ledgerAP != total {
			t.Errorf("ledger AP %s, want %s", ledgerAP, total)
		}
	}
	wantAP("1000.0000")
	apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, billPath+"/record-payment",
		map[string]any{"amount": 400, "payment_date": "2026-01-20"}), http.StatusOK)
	wantAP("600.0000")
	ps := apitest.Decode[[]bills.BillPaymentOut](t, a.Do(t, http.MethodGet, billPath+"/payments", nil), http.StatusOK)
	if len(ps) != 1 {
		t.Fatalf("payments %+v", ps)
	}
	apitest.Decode[bills.BillPaymentOut](t, a.Do(t, http.MethodPost, billPath+"/payments/"+ps[0].BillPaymentID.String()+"/reverse",
		map[string]any{"reason": "bounced"}), http.StatusOK)
	wantAP("1000.0000")
	apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPatch, billPath, map[string]any{"status": "PAID"}), http.StatusOK)
	wantAP("0.0000")
}
