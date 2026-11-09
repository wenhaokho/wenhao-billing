package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Ports of tests/integration/test_record_payment_withholding.py ([IN]: the
// nine record-payment tests), then Go's own.

// api is the payment routes on a fresh database; the env shares its pool.
type api struct {
	*env
	app *apitest.App
}

func newAPI(t *testing.T, hosting HostingRestorer) *api {
	t.Helper()
	svc := Service{BaseCurrency: "IDR", Hosting: hosting}
	a := apitest.New(t, func(h huma.API, d apitest.Deps) { Register(h, d.Pool, svc, intakeConfig(d.Config), d.Clock) })
	return &api{env: &env{pool: a.Pool, svc: svc, admin: a.AdminID}, app: a}
}

type problemBody struct {
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func recordPath(id uuid.UUID) string   { return "/api/v1/invoices/" + id.String() + "/record-payment" }
func paymentsPath(id uuid.UUID) string { return "/api/v1/invoices/" + id.String() + "/payments" }

// body is Python's _post body: payment_date and payer_name plus extra.
func body(extra map[string]any) map[string]any {
	b := map[string]any{"payment_date": "2026-04-20", "payer_name": "Acme"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func (a *api) record(t *testing.T, id uuid.UUID, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return a.app.Do(t, http.MethodPost, recordPath(id), body(extra))
}

func (a *api) mustRecord(t *testing.T, id uuid.UUID, extra map[string]any) PaymentOut {
	t.Helper()
	return apitest.Decode[PaymentOut](t, a.record(t, id, extra), http.StatusCreated)
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail string) {
	t.Helper()
	p := apitest.Decode[problemBody](t, rec, status)
	if p.Detail != detail || (code != "" && p.Code != code) {
		t.Errorf("problem %q %q, want %q %q", p.Code, p.Detail, code, detail)
	}
}

func wantFieldError(t *testing.T, rec *httptest.ResponseRecorder, location string) {
	t.Helper()
	p := apitest.Decode[problemBody](t, rec, http.StatusUnprocessableEntity)
	for _, e := range p.Errors {
		if e.Location == location {
			return
		}
	}
	t.Errorf("no error at %q: %s", location, rec.Body)
}

// whtInvoice is the withholding fixture: GROSS due, WHT withheld, OPEN, IDR.
func (a *api) whtInvoice(t *testing.T) uuid.UUID { return a.env.whtInvoice(t, "IDR", gross, wht) }

func TestRecordNetPlusWithheldMarksPaid(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	got := a.mustRecord(t, id, map[string]any{"amount": net, "wht_amount": wht})
	if !got.WhtAmount.Decimal().Equal(dec(wht)) {
		t.Errorf("wht_amount %s", got.WhtAmount.Decimal())
	}
	checkInvoice(t, a.env, id, "0", wht, "PAID")
}

func TestRecordExceedingBalanceIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	rec := a.record(t, id, map[string]any{"amount": net, "wht_amount": "204083"})
	wantProblem(t, rec, http.StatusBadRequest, "payment_exceeds_balance", "amount + withheld 10204083 exceeds balance due 10204082.0000")
	checkInvoice(t, a.env, id, gross, "0", "OPEN")
}

func TestRecordWithoutWhtUnchanged(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	got := a.mustRecord(t, id, map[string]any{"amount": "1000000"})
	if !got.WhtAmount.Decimal().IsZero() {
		t.Errorf("wht_amount %s", got.WhtAmount.Decimal())
	}
	checkInvoice(t, a.env, id, "9204082", "0", "PARTIAL")
}

func TestRecordPartialWithWhtReducesBalanceByBoth(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	a.mustRecord(t, id, map[string]any{"amount": "5000000", "wht_amount": "100000"})
	checkInvoice(t, a.env, id, "5104082", "100000", "PARTIAL")
}

func TestExceedingErrorMentionsWithheld(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	rec := a.record(t, id, map[string]any{"amount": net, "wht_amount": "204083"})
	p := apitest.Decode[problemBody](t, rec, http.StatusBadRequest)
	if !strings.Contains(p.Detail, "amount + withheld") {
		t.Errorf("detail %q", p.Detail)
	}
}

func TestWithheldAboveOutstandingIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	// Fits the balance due, but withholds more PPh than the invoice carries.
	rec := a.record(t, id, map[string]any{"amount": "1000000", "wht_amount": "204083"})
	wantProblem(t, rec, http.StatusBadRequest, "withholding_exceeds_outstanding", "withheld 204083 exceeds outstanding withholding 204082.0000")
	checkInvoice(t, a.env, id, gross, "0", "OPEN")
}

func TestWithheldAboveRemainingAfterPartialIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	a.mustRecord(t, id, map[string]any{"amount": "5000000", "wht_amount": "100000"})
	rec := a.record(t, id, map[string]any{"amount": "1000000", "wht_amount": "104083"})
	p := apitest.Decode[problemBody](t, rec, http.StatusBadRequest)
	if !strings.Contains(p.Detail, "outstanding withholding 104082") {
		t.Errorf("detail %q", p.Detail)
	}
	a.mustRecord(t, id, map[string]any{"amount": "1000000", "wht_amount": "104082"})
}

func TestPphOnlyPaymentSettlesStuckPartial(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	// The customer paid the full net in cash without a PPh slip; what remains
	// is the outstanding PPh, so the cash to receive is 0.
	a.mustRecord(t, id, map[string]any{"amount": net})
	checkInvoice(t, a.env, id, wht, "0", "PARTIAL")
	a.mustRecord(t, id, map[string]any{"amount": "0", "wht_amount": wht})
	checkInvoice(t, a.env, id, "0", wht, "PAID")
}

func TestZeroAmountAndZeroWithheldIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	rec := a.record(t, id, map[string]any{"amount": "0", "wht_amount": "0"})
	wantProblem(t, rec, http.StatusBadRequest, "payment_zero", "amount and withheld cannot both be zero")
	checkInvoice(t, a.env, id, gross, "0", "OPEN")
}

func TestRecordPaymentOnDraftAndVoidIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	for _, status := range []string{"DRAFT", "VOID"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			id := a.invoice(t, inv{customer: c, currency: "IDR", status: status, balance: "1000"})
			rec := a.record(t, id, map[string]any{"amount": "100"})
			wantProblem(t, rec, http.StatusBadRequest, "invoice_not_payable", "cannot record payment on "+status+" invoice")
			checkInvoice(t, a.env, id, "1000", "0", status)
			if n := a.count(t, `SELECT count(*) FROM payments WHERE invoice_id = $1`, id); n != 0 {
				t.Errorf("%d payments stored", n)
			}
		})
	}
}

func TestRecordPaymentUnknownInvoiceIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	wantProblem(t, a.record(t, uuid.New(), map[string]any{"amount": "1"}), http.StatusNotFound, "invoice_not_found", "invoice not found")
}

// A PAID invoice is not refused as such: its balance is 0, so any payment
// falls to the balance sentence (kept).
func TestRecordPaymentPaidInvoiceExceedsSentence(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "PAID", balance: "0"})
	wantProblem(t, a.record(t, id, map[string]any{"amount": "100"}), http.StatusBadRequest, "payment_exceeds_balance",
		"amount 100 exceeds balance due 0.0000")
}

// The sentences print the amounts as Python held them: JSON numbers went
// through float, integers and strings keep their digits and scale.
func TestRecordPaymentSentencePrintsAmountAsSent(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "100"})
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"integer", `{"amount": 150, "payment_date": "2026-04-20"}`, "amount 150 exceeds balance due 100.0000"},
		{"float", `{"amount": 150.50, "payment_date": "2026-04-20"}`, "amount 150.5 exceeds balance due 100.0000"},
		{"exponent", `{"amount": 1.5e2, "payment_date": "2026-04-20"}`, "amount 150.0 exceeds balance due 100.0000"},
		{"string", `{"amount": "150.50", "payment_date": "2026-04-20"}`, "amount 150.50 exceeds balance due 100.0000"},
		{"with withheld", `{"amount": 100, "wht_amount": "0.50", "payment_date": "2026-04-20"}`, "amount + withheld 100.50 exceeds balance due 100.0000"},
		{"float withheld", `{"amount": 100.5, "wht_amount": 0.25, "payment_date": "2026-04-20"}`, "amount + withheld 100.75 exceeds balance due 100.0000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantProblem(t, a.app.Do(t, http.MethodPost, recordPath(id), tc.raw), http.StatusBadRequest, "payment_exceeds_balance", tc.want)
		})
	}
}

func TestRecordPaymentStoresManualClearedPayment(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "USD", status: "SENT", balance: "1000"})
	rec := a.app.Do(t, http.MethodPost, recordPath(id), map[string]any{
		"amount": 400, "payment_date": "2026-04-20", "payer_name": "Bank", "payer_reference": "TRX-9", "notes": "wire",
	})
	got := apitest.Decode[PaymentOut](t, rec, http.StatusCreated)
	if got.InvoiceID == nil || *got.InvoiceID != id || got.CustomerID == nil || *got.CustomerID != c {
		t.Errorf("invoice %v customer %v", got.InvoiceID, got.CustomerID)
	}
	if got.Currency != "USD" || got.PayerName != "Bank" || got.PayerReference == nil || *got.PayerReference != "TRX-9" ||
		got.IntakeSource != "MANUAL" || got.Status != "CLEARED" || got.AdjustmentType != "NONE" || got.ConfidenceScore != nil ||
		got.PaymentDate.Format("2006-01-02") != "2026-04-20" {
		t.Errorf("payment %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"amount":"400.0000","wht_amount":"0.0000"`) {
		t.Errorf("decimals not at DB scale: %s", rec.Body)
	}
	logs := a.logs(t, got.PaymentID)
	if len(logs) != 1 || logs[0].action != "CLEARED" || !slices.Equal(logs[0].reasons, []string{"manual-entry", "wire"}) ||
		logs[0].actor == nil || *logs[0].actor != a.admin {
		t.Errorf("logs %+v", logs)
	}
	checkInvoice(t, a.env, id, "600", "0", "PARTIAL")
}

func TestRecordPaymentDefaultsPayerName(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	for name, raw := range map[string]string{
		"null":   `{"amount": 1, "payment_date": "2026-04-20", "payer_name": null}`,
		"empty":  `{"amount": 1, "payment_date": "2026-04-20", "payer_name": ""}`,
		"absent": `{"amount": 1, "payment_date": "2026-04-20"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := apitest.Decode[PaymentOut](t, a.app.Do(t, http.MethodPost, recordPath(id), raw), http.StatusCreated)
			if got.PayerName != "(manual entry)" {
				t.Errorf("payer_name %q", got.PayerName)
			}
			if got.PayerReference != nil {
				t.Errorf("payer_reference %v", got.PayerReference)
			}
			if logs := a.logs(t, got.PaymentID); len(logs) != 1 || !slices.Equal(logs[0].reasons, []string{"manual-entry"}) {
				t.Errorf("logs %+v", logs)
			}
		})
	}
}

func TestRecordPaymentExternalRefShape(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	re := regexp.MustCompile(`^manual-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 5 {
		got := a.mustRecord(t, id, map[string]any{"amount": "1"})
		if got.ExternalRef == nil || !re.MatchString(*got.ExternalRef) {
			t.Fatalf("external_ref %v", got.ExternalRef)
		}
		if seen[*got.ExternalRef] {
			t.Errorf("repeated external_ref %s", *got.ExternalRef)
		}
		seen[*got.ExternalRef] = true
	}
}

func TestRecordPaymentNotesRuneCut(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	notes := strings.Repeat("é世🙂x", 62) + "ab" // 248 + 2 = 250 runes, 1 to 4 bytes each
	if n := len([]rune(notes)); n != 250 {
		t.Fatalf("fixture has %d runes", n)
	}
	got := a.mustRecord(t, id, map[string]any{"amount": "1", "notes": notes})
	logs := a.logs(t, got.PaymentID)
	if len(logs) != 1 || len(logs[0].reasons) != 2 {
		t.Fatalf("logs %+v", logs)
	}
	if want := string([]rune(notes)[:200]); logs[0].reasons[1] != want {
		t.Errorf("reason has %d runes, want the first 200 of the notes", len([]rune(logs[0].reasons[1])))
	}
	// Empty notes add no reason.
	got = a.mustRecord(t, id, map[string]any{"amount": "1", "notes": ""})
	if logs := a.logs(t, got.PaymentID); len(logs[0].reasons) != 1 {
		t.Errorf("reasons %v", logs[0].reasons)
	}
}

func TestRecordPaymentValidation(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	long := strings.Repeat("x", 256)
	for _, tc := range []struct {
		name, location string
		body           map[string]any
	}{
		{"five decimals", "amount", map[string]any{"amount": "1.00001"}},
		{"five decimals withheld", "wht_amount", map[string]any{"amount": "1", "wht_amount": "0.00001"}},
		{"negative amount", "amount", map[string]any{"amount": "-1"}},
		{"negative withheld", "wht_amount", map[string]any{"amount": "1", "wht_amount": "-1"}},
		{"missing amount", "", map[string]any{}},
		{"payer_name too long", "payer_name", map[string]any{"amount": "1", "payer_name": long}},
		{"payer_reference too long", "payer_reference", map[string]any{"amount": "1", "payer_reference": long}},
		{"null withheld", "", map[string]any{"amount": "1", "wht_amount": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := a.record(t, id, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422: %s", rec.Code, rec.Body)
			}
			if tc.location != "" {
				wantFieldError(t, rec, tc.location)
			}
		})
	}
}

// Exactly 255 characters, multi-byte included, is accepted.
func TestRecordPaymentPayerNameLimitIsCharacters(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	got := a.mustRecord(t, id, map[string]any{"amount": "1", "payer_name": strings.Repeat("世", 255)})
	if len([]rune(got.PayerName)) != 255 {
		t.Errorf("payer_name has %d runes", len([]rune(got.PayerName)))
	}
}

func TestRecordPaymentFiveDecimalsIs422(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	wantFieldError(t, a.record(t, id, map[string]any{"amount": 1.23456}), "amount")
	checkInvoice(t, a.env, id, "1000", "0", "OPEN")
	if n := a.count(t, `SELECT count(*) FROM payments`); n != 0 {
		t.Errorf("%d payments stored", n)
	}
}

func TestRecordPaymentWritesNoJournal(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	a.mustRecord(t, id, map[string]any{"amount": net, "wht_amount": wht})
	if n := a.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries", n)
	}
}

func TestRecordPaymentPaidTellsHosting(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	a := newAPI(t, rec)
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	a.mustRecord(t, id, map[string]any{"amount": "400"})
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("restored %v after a partial payment", got)
	}
	a.mustRecord(t, id, map[string]any{"amount": "600"})
	if got := rec.got(); !slices.Equal(got, []uuid.UUID{id}) {
		t.Errorf("restored %v, want [%s]", got, id)
	}
	checkInvoice(t, a.env, id, "0", "0", "PAID")
}

// Eight payments of 300 race for a balance of 1000: exactly three fit, and
// the balance ends at 100. Without the invoice lock they all read 1000.
func TestRecordPaymentConcurrentNeverOverdraws(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	const n = 8
	codes := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = a.app.Do(t, http.MethodPost, recordPath(id), body(map[string]any{"amount": "300"})).Code
		}()
	}
	close(start)
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusBadRequest:
		default:
			t.Errorf("status %d", c)
		}
	}
	if created != 3 {
		t.Errorf("%d payments recorded (%v), want 3", created, codes)
	}
	checkInvoice(t, a.env, id, "100", "0", "PARTIAL")
	var sum decimal.Decimal
	if err := a.pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0) FROM payments WHERE invoice_id = $1`, id).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if !sum.Equal(dec("900")) {
		t.Errorf("payments sum %s, want 900", sum)
	}
}

func TestPaymentRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	staffID := uuid.New()
	a.exec(t, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.app.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a.app
	staff.Cookie = platform.SessionCookie + "=" + token
	id := uuid.New()
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, recordPath(id)},
		{http.MethodGet, paymentsPath(id)},
	} {
		wantProblem(t, a.app.Anon(t, c.method, c.path, body(map[string]any{"amount": "1"})), http.StatusUnauthorized, "", "not authenticated")
		wantProblem(t, staff.Do(t, c.method, c.path, body(map[string]any{"amount": "1"})), http.StatusForbidden, "", "admin required")
	}
}
