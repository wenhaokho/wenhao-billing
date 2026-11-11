package stats_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

const dashPath = "/api/v1/stats/dashboard"

func getDashboard(t *testing.T, a *apitest.App) (stats.DashboardStats, string) {
	t.Helper()
	rec := a.Do(t, http.MethodGet, dashPath, nil)
	return apitest.Decode[stats.DashboardStats](t, rec, http.StatusOK), rec.Body.String()
}

// logRow inserts a payment (invoice and customer optional) and one log row on
// it, returning the payment and log ids.
type logSpec struct {
	invoice, customer *uuid.UUID
	actor             *uuid.UUID
	action            string
	createdAt         time.Time
	logID             *uuid.UUID
}

func addPayment(t *testing.T, a *apitest.App, s logSpec) (pay, log uuid.UUID) {
	t.Helper()
	pay, log = uuid.New(), uuid.New()
	if s.logID != nil {
		log = *s.logID
	}
	exec(t, a, `INSERT INTO payments (payment_id, invoice_id, customer_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status)
VALUES ($1, $2, $3, 10, 'USD', 'Unknown Payer', '2026-07-01', 'TEST', $4, 'PENDING_MANUAL_REVIEW')`, pay, s.invoice, s.customer, uuid.NewString())
	if s.createdAt.IsZero() {
		s.createdAt = apitest.Start
	}
	if s.action == "" {
		s.action = "HELD"
	}
	exec(t, a, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes, actor_user_id, created_at) VALUES ($1, $2, $3, ARRAY['no-match'], ARRAY['OTHER'], $4, $5)`,
		log, pay, s.action, s.actor, s.createdAt)
	return pay, log
}

func TestDashboardEndpointMatchesServiceFunction(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	invoice(t, a, c, "USD", "500", "SENT")
	quote(t, a, c, "USD", "750", "ACCEPTED")

	want, err := stats.Dashboard(t.Context(), a.Pool, "IDR", today(a), a.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := getDashboard(t, a)

	if got.AwaitingReviewCount != want.AwaitingReviewCount || got.DraftCount != want.DraftCount || got.SentCount != want.SentCount ||
		got.CustomerCount != want.CustomerCount || got.AutoClearedLast30d != want.AutoClearedLast30d || got.OpenQuotationCount != want.OpenQuotationCount {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	eq(t, "open_ar_by_currency", amounts(got.OpenARByCurrency), amounts(want.OpenARByCurrency))
	eq(t, "open_quotation_pipeline", amounts(got.OpenQuotationPipeline), amounts(want.OpenQuotationPipeline))
	eq(t, "open_ar_by_currency", amounts(got.OpenARByCurrency), []string{"USD 500.0000"})
	eq(t, "open_quotation_pipeline", amounts(got.OpenQuotationPipeline), []string{"USD 750.0000"})
	if got.RecentActivity == nil || len(got.RecentActivity) != 0 || got.UpcomingInvoices == nil || len(got.UpcomingInvoices) != 0 {
		t.Fatalf("recent_activity %v upcoming_invoices %v, want empty non-nil lists", got.RecentActivity, got.UpcomingInvoices)
	}
}

func TestRecentActivityIncludesPaymentInvoiceAndActorDetails(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	actor := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role, display_name) VALUES ($1, 'actor@example.com', 'placeholder: never signs in', 'admin', 'Dana')`, actor)
	inv := invoice(t, a, c, "USD", "0", "PAID")
	exec(t, a, `UPDATE invoices SET invoice_number = 'INV-TEST-1' WHERE invoice_id = $1`, inv)
	pay, _ := addPayment(t, a, logSpec{invoice: &inv, actor: &actor, action: "CLEARED"})
	exec(t, a, `UPDATE payments SET payer_name = 'Someone Else Ltd', payer_reference = 'BANK-REF-9', wht_amount = 20, confidence_score = 97.5 WHERE payment_id = $1`, pay)

	got, body := getDashboard(t, a)
	if len(got.RecentActivity) != 1 {
		t.Fatalf("recent_activity = %+v", got.RecentActivity)
	}
	row := got.RecentActivity[0]
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	if row.PaymentID != pay || row.Action != "CLEARED" || str(row.PayerName) != "Someone Else Ltd" || str(row.CustomerName) != "Acme Corp" ||
		row.InvoiceID == nil || *row.InvoiceID != inv || str(row.InvoiceNumber) != "INV-TEST-1" || str(row.PayerReference) != "BANK-REF-9" ||
		str(row.IntakeSource) != "TEST" || str(row.ActorName) != "Dana" || str(row.Currency) != "USD" {
		t.Fatalf("row = %+v", row)
	}
	if row.PaymentDate == nil || row.PaymentDate.Format("2006-01-02") != "2026-07-01" {
		t.Fatalf("payment_date = %v", row.PaymentDate)
	}
	if row.ActorUserID == nil || *row.ActorUserID != actor || len(row.Reasons) != 1 || row.Reasons[0] != "no-match" ||
		!slices.Equal(row.ReasonCodes, []string{"OTHER"}) {
		t.Fatalf("actor %v reasons %v", row.ActorUserID, row.Reasons)
	}
	for _, want := range []string{`"wht_amount":"20.0000"`, `"confidence_score":"97.50"`, `"amount":"10.0000"`, `"created_at":"2026-01-15T09:00:00Z"`, `"payment_date":"2026-07-01"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
}

func TestRecentActivityDetailsEmptyForUnmatchedSystemPayment(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	pay, _ := addPayment(t, a, logSpec{})
	got, _ := getDashboard(t, a)
	if len(got.RecentActivity) != 1 || got.RecentActivity[0].PaymentID != pay {
		t.Fatalf("recent_activity = %+v", got.RecentActivity)
	}
	row := got.RecentActivity[0]
	if row.InvoiceID != nil || row.InvoiceNumber != nil || row.CustomerName != nil || row.ActorName != nil || row.ActorUserID != nil {
		t.Fatalf("row = %+v, want invoice, customer and actor null", row)
	}
}

// The customer is the payment's own when it has one, else the invoice's.
func TestRecentActivityCustomerCoalescesPaymentBeforeInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	invCust := newCustomer(t, a, "Invoice Customer")
	payCust := newCustomer(t, a, "Payment Customer")
	inv := invoice(t, a, invCust, "USD", "10", "SENT")
	both, _ := addPayment(t, a, logSpec{invoice: &inv, customer: &payCust, createdAt: apitest.Start.Add(2 * time.Minute)})
	onlyInv, _ := addPayment(t, a, logSpec{invoice: &inv, createdAt: apitest.Start.Add(time.Minute)})
	onlyPay, _ := addPayment(t, a, logSpec{customer: &payCust, createdAt: apitest.Start})
	got, _ := getDashboard(t, a)
	names := map[uuid.UUID]string{}
	for _, r := range got.RecentActivity {
		if r.CustomerName != nil {
			names[r.PaymentID] = *r.CustomerName
		}
	}
	if len(got.RecentActivity) != 3 || names[both] != "Payment Customer" || names[onlyInv] != "Invoice Customer" || names[onlyPay] != "Payment Customer" {
		t.Fatalf("customer names = %v over %d rows", names, len(got.RecentActivity))
	}
}

// actor_name is coalesce(display_name, email): an empty name is kept, a NULL
// one falls back to the email.
func TestRecentActivityActorNameCoalesce(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	empty, nul := uuid.New(), uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role, display_name) VALUES ($1, 'empty@example.com', 'x', 'staff', '')`, empty)
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'null@example.com', 'x', 'staff')`, nul)
	pe, _ := addPayment(t, a, logSpec{actor: &empty, createdAt: apitest.Start.Add(time.Minute)})
	pn, _ := addPayment(t, a, logSpec{actor: &nul, createdAt: apitest.Start})
	got, _ := getDashboard(t, a)
	names := map[uuid.UUID]string{}
	for _, r := range got.RecentActivity {
		if r.ActorName != nil {
			names[r.PaymentID] = *r.ActorName
		}
	}
	if len(names) != 2 || names[pe] != "" || names[pn] != "null@example.com" {
		t.Fatalf("actor names = %q", names)
	}
}

// Newest first, the log id breaking ties, and ten rows.
func TestRecentActivityNewestFirstTenRowsLogIDBreaksTies(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Twelve logs at distinct minutes 0..11, plus two more at minute 11 whose
	// log ids order them against each other.
	byMinute := map[int]uuid.UUID{}
	for m := range 12 {
		_, byMinute[m] = addPayment(t, a, logSpec{createdAt: apitest.Start.Add(time.Duration(m) * time.Minute)})
	}
	lo := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	hi := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	addPayment(t, a, logSpec{createdAt: apitest.Start.Add(11 * time.Minute), logID: &lo})
	addPayment(t, a, logSpec{createdAt: apitest.Start.Add(11 * time.Minute), logID: &hi})
	got, _ := getDashboard(t, a)
	if len(got.RecentActivity) != 10 {
		t.Fatalf("len = %d, want 10", len(got.RecentActivity))
	}
	// Minute 11 holds three logs: hi, then the random one and lo in log-id
	// descending order; then minutes 10, 9, ... 4 descending.
	ids := []string{}
	for _, r := range got.RecentActivity {
		ids = append(ids, r.LogID.String())
	}
	if ids[0] != hi.String() || ids[2] != lo.String() {
		t.Fatalf("tie order: %v", ids[:3])
	}
	if mid := ids[1]; mid != byMinute[11].String() || byMinute[11].String() < lo.String() || byMinute[11].String() > hi.String() {
		t.Fatalf("middle %s want %s", mid, byMinute[11])
	}
	for i, m := 0, 10; m >= 4; i, m = i+1, m-1 {
		if ids[3+i] != byMinute[m].String() {
			t.Fatalf("row %d = %s, want minute %d (%s)", 3+i, ids[3+i], m, byMinute[m])
		}
	}
}

func TestDashboardEndpointExposesNewFields(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	template(t, a, c, "USD", "1200", with(monthly(), "start_date", "2026-01-05"))
	invoice(t, a, c, "USD", "100", "SENT")

	got, body := getDashboard(t, a)
	if got.BaseCurrency != "IDR" || len(got.MRRByCurrency) != 1 || got.MRRByCurrency[0].Currency != "USD" {
		t.Fatalf("base %q mrr %v", got.BaseCurrency, amounts(got.MRRByCurrency))
	}
	if !strings.Contains(body, `"base_amount":"1600000.0000"`) {
		t.Errorf("body lacks the base amount string: %s", body)
	}
	// The clock is 2026-01-15, so the template's next cycle is 2026-02-05.
	if len(got.UpcomingInvoices) != 1 {
		t.Fatalf("upcoming = %+v", got.UpcomingInvoices)
	}
	u := got.UpcomingInvoices[0]
	if u.Mode != "RECURRING" || u.NextDate.Format("2006-01-02") != "2026-02-05" || u.CustomerName == nil || *u.CustomerName != "Acme Corp" ||
		u.Amount.Decimal().StringFixed(4) != "1200.0000" || u.AmountIsEstimate {
		t.Fatalf("upcoming[0] = %+v", u)
	}
}

// The fields are in the order of Python's DashboardStats.
func TestDashboardFieldOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	_, body := getDashboard(t, a)
	dec := json.NewDecoder(strings.NewReader(body))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, fmt.Sprint(k))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, k := range keys {
		if k != "$schema" {
			got = append(got, k)
		}
	}
	eq(t, "keys", got, []string{
		"awaiting_review_count", "draft_count", "sent_count", "customer_count", "auto_cleared_last_30d",
		"open_ar_by_currency", "open_quotation_count", "open_quotation_pipeline", "recent_activity",
		"mrr_by_currency", "base_currency", "open_ar_base_by_currency", "open_ar_unconverted", "upcoming_invoices",
	})
}

type problem struct {
	Detail string `json:"detail"`
}

func TestDashboardRequiresAdmin(t *testing.T) {
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
	if p := apitest.Decode[problem](t, a.Anon(t, http.MethodGet, dashPath, nil), http.StatusUnauthorized); p.Detail != "not authenticated" {
		t.Errorf("anon: %q", p.Detail)
	}
	if p := apitest.Decode[problem](t, staff.Do(t, http.MethodGet, dashPath, nil), http.StatusForbidden); p.Detail != "admin required" {
		t.Errorf("staff: %q", p.Detail)
	}
}
