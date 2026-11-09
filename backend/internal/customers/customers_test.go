package customers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
)

const path = "/api/v1/customers"

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) { customers.Register(api, d.Pool) })
}

func create(t *testing.T, a *apitest.App, body map[string]any) customers.CustomerOut {
	t.Helper()
	return apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodPost, path, body), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, body map[string]any) customers.CustomerOut {
	t.Helper()
	return apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodPatch, customerPath(id), body), http.StatusOK)
}

func customerPath(id uuid.UUID) string { return path + "/" + id.String() }

// wantProblem checks the status and detail, the code when one is given, and
// that the errors list location with message when a location is given.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail, location, message string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail {
		t.Errorf("detail %q, want %q: %s", p.Detail, detail, rec.Body)
	}
	if code != "" && p.Code != code {
		t.Errorf("code %q, want %q: %s", p.Code, code, rec.Body)
	}
	if location == "" {
		return
	}
	for _, e := range p.Errors {
		if e.Location == location && e.Message == message {
			return
		}
	}
	t.Errorf("no error %q %q: %s", location, message, rec.Body)
}

// wht checks the three withholding fields; rate "" means null.
func wht(t *testing.T, c customers.CustomerOut, typ, rate string, grossUp bool) {
	t.Helper()
	got := "<nil>"
	if c.DefaultWhtRate != nil {
		got = c.DefaultWhtRate.StringFixed(4)
	}
	want := rate
	if want == "" {
		want = "<nil>"
	}
	if c.DefaultWhtType != typ || got != want || c.DefaultWhtGrossUp != grossUp {
		t.Errorf("wht %s %s %v, want %s %s %v", c.DefaultWhtType, got, c.DefaultWhtGrossUp, typ, want, grossUp)
	}
}

// --- tests/unit/test_customers_service.py ---

func TestCreateCustomerSetsFields(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{"name": "Beta Pte Ltd", "contact_email": "ap@beta.com"})
	if got.CustomerID == uuid.Nil || got.Name != "Beta Pte Ltd" || got.ContactEmail == nil || *got.ContactEmail != "ap@beta.com" {
		t.Errorf("%+v", got)
	}
	if got.DefaultCurrency != "IDR" || !got.Active {
		t.Errorf("defaults %+v", got)
	}
	back := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodGet, customerPath(got.CustomerID), nil), http.StatusOK)
	if back.CustomerID != got.CustomerID || back.Name != "Beta Pte Ltd" {
		t.Errorf("GET %+v", back)
	}
}

func TestUpdateCustomerEditsFields(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Acme", "website": "acme.example", "matching_aliases": []string{"ACME"}})
	got := patch(t, a, c.CustomerID, map[string]any{"notes": "VIP account", "contact_phone": "+65 1234 5678"})
	if got.Notes == nil || *got.Notes != "VIP account" || got.ContactPhone == nil || *got.ContactPhone != "+65 1234 5678" {
		t.Errorf("%+v", got)
	}
	if got.Name != "Acme" || got.Website == nil || *got.Website != "acme.example" || !slices.Equal(got.MatchingAliases, []string{"ACME"}) {
		t.Errorf("kept %+v", got)
	}
	cleared := patch(t, a, c.CustomerID, map[string]any{"website": nil, "name": "Acme Corp"})
	if cleared.Website != nil || cleared.Name != "Acme Corp" || *cleared.Notes != "VIP account" {
		t.Errorf("cleared %+v", cleared)
	}
}

func TestUpdateCustomerNotFound(t *testing.T) {
	a := newApp(t)
	for _, m := range []string{http.MethodPatch, http.MethodGet, http.MethodDelete} {
		var body any
		if m == http.MethodPatch {
			body = map[string]any{"notes": "x"}
		}
		wantProblem(t, a.Do(t, m, customerPath(uuid.New()), body), http.StatusNotFound, "customer_not_found", "customer not found", "", "")
	}
}

// --- tests/integration/test_customer_cc.py ---

func TestCustomerCCListsRoundtrip(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{
		"name":                "CC Co",
		"quotation_cc_emails": []string{"sales@cc.example", "boss@cc.example"},
		"billing_cc_emails":   []string{"finance@cc.example"},
	})
	if !slices.Equal(c.QuotationCcEmails, []string{"sales@cc.example", "boss@cc.example"}) ||
		!slices.Equal(c.BillingCcEmails, []string{"finance@cc.example"}) {
		t.Errorf("%+v", c)
	}
	got := patch(t, a, c.CustomerID, map[string]any{"billing_cc_emails": []string{}})
	if len(got.BillingCcEmails) != 0 || !slices.Equal(got.QuotationCcEmails, []string{"sales@cc.example", "boss@cc.example"}) {
		t.Errorf("%+v", got)
	}
}

func TestCustomerCCRejectsInvalidAddress(t *testing.T) {
	a := newApp(t)
	for _, m := range []string{http.MethodPost, http.MethodPatch} {
		u, body := path, map[string]any{"name": "Bad CC", "billing_cc_emails": []string{"ok@x.com", "not-an-email"}}
		if m == http.MethodPatch {
			u = customerPath(create(t, a, map[string]any{"name": "Fine"}).CustomerID)
		}
		rec := a.Do(t, m, u, body)
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		if !strings.Contains(rec.Body.String(), "invalid email address: not-an-email") {
			t.Errorf("%s: %s", m, rec.Body)
		}
		found := false
		for _, e := range p.Errors {
			found = found || e.Location == "billing_cc_emails.1"
		}
		if !found {
			t.Errorf("%s: no error at billing_cc_emails.1: %s", m, rec.Body)
		}
	}
}

// --- tests/integration/test_customer_wht_defaults.py ---

func TestCreateCustomerWithPph23GrossUp(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Wht Co", "default_wht_type": "PPH23", "default_wht_gross_up": true})
	wht(t, c, "PPH23", "2.0000", true)
	rec := a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil)
	if !strings.Contains(rec.Body.String(), `"default_wht_rate":"2.0000"`) {
		t.Errorf("wire: %s", rec.Body)
	}
}

func TestCustomerDefaultsToNone(t *testing.T) {
	a := newApp(t)
	wht(t, create(t, a, map[string]any{"name": "Plain Co"}), "NONE", "", false)
}

func TestCustomerWhtInvalidRate422(t *testing.T) {
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "Bad Rate", "default_wht_type": "PPH21", "default_wht_rate": 150}),
		http.StatusUnprocessableEntity, "invalid_withholding", "Withholding tax rate must be greater than 0 and less than 100", "", "")
}

func TestCustomerWhtUnknownType422(t *testing.T) {
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "Bad Type", "default_wht_type": "PPH99"}),
		http.StatusUnprocessableEntity, "invalid_withholding", "Unknown withholding tax type: PPH99", "", "")
}

func TestCustomerNoneClearsRate(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Clear Co", "default_wht_type": "PPH21", "default_wht_rate": 3, "default_wht_gross_up": true})
	wht(t, c, "PPH21", "3.0000", true)
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_type": "NONE"}), "NONE", "", false)
}

func TestPatchWithoutWhtFieldsKeepsStored(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Keep Co", "default_wht_type": "PPH23", "default_wht_gross_up": true})
	wht(t, patch(t, a, c.CustomerID, map[string]any{"notes": "hi"}), "PPH23", "2.0000", true)
}

func TestPatchPartialWhtMergesWithStored(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Merge Co", "default_wht_type": "PPH23", "default_wht_gross_up": true})
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_rate": 4}), "PPH23", "4.0000", true)
	wantProblem(t, a.Do(t, http.MethodPatch, customerPath(c.CustomerID), map[string]any{"default_wht_rate": 150}),
		http.StatusUnprocessableEntity, "invalid_withholding", "Withholding tax rate must be greater than 0 and less than 100", "", "")
	// The refused PATCH changed nothing.
	back := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil), http.StatusOK)
	wht(t, back, "PPH23", "4.0000", true)
}

func TestPatchTypeSwitchUsesNewDefaultRate(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Switch Co", "default_wht_type": "PPH23", "default_wht_gross_up": true})
	wht(t, c, "PPH23", "2.0000", true)
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_type": "PPH21"}), "PPH21", "2.5000", true)
	// The same type sent again keeps the stored rate.
	patch(t, a, c.CustomerID, map[string]any{"default_wht_rate": "3"})
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_type": "PPH21"}), "PPH21", "3.0000", true)
}

// --- new ---

func TestCustomerWhtRateAcceptsNumberAndString(t *testing.T) {
	a := newApp(t)
	n := create(t, a, map[string]any{"name": "Num", "default_wht_type": "PPH23", "default_wht_rate": 1.5})
	wht(t, n, "PPH23", "1.5000", false)
	s := create(t, a, map[string]any{"name": "Str", "default_wht_type": "PPH23", "default_wht_rate": "1.75"})
	wht(t, s, "PPH23", "1.7500", false)
	// A null rate is no rate: the type's default.
	wht(t, patch(t, a, s.CustomerID, map[string]any{"default_wht_rate": nil}), "PPH23", "2.0000", false)
}

// A null default_wht_type or default_wht_gross_up keeps the stored value (on
// create, NONE and false), as Python's data.get(...) is None does; it is not
// a 422. The withholding is still normalised, as the key was sent.
func TestCustomerNullWhtTypeAndGrossUpKeepStored(t *testing.T) {
	a := newApp(t)
	wht(t, create(t, a, map[string]any{"name": "Null Co", "default_wht_type": nil, "default_wht_gross_up": nil}), "NONE", "", false)
	c := create(t, a, map[string]any{"name": "Kept Co", "default_wht_type": "PPH23", "default_wht_rate": 4, "default_wht_gross_up": true})
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_type": nil}), "PPH23", "4.0000", true)
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_gross_up": nil}), "PPH23", "4.0000", true)
	// With the rate also null, the stored type's default rate.
	wht(t, patch(t, a, c.CustomerID, map[string]any{"default_wht_type": nil, "default_wht_rate": nil}), "PPH23", "2.0000", true)
}

// A rate with more than 4 decimal places is refused at its field, not
// rounded into numeric(7,4).
func TestCustomerWhtRateFivePlacesIs422(t *testing.T) {
	a := newApp(t)
	// Dec4 refuses it at the field, listed as Huma's own validation errors.
	const (
		invalid = "Request validation failed"
		places  = "value has too many decimal places (max 4)"
	)
	for _, rate := range []any{99.99999, 2.00001} {
		wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "Places Co", "default_wht_type": "PPH23", "default_wht_rate": rate}),
			http.StatusUnprocessableEntity, "", invalid, "default_wht_rate", places)
	}
	// A string with five places also fails Dec4's schema pattern, at the field.
	for _, msg := range []string{"expected value to match exactly one schema but matched none", places} {
		wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "Places Co", "default_wht_type": "PPH23", "default_wht_rate": "99.99999"}),
			http.StatusUnprocessableEntity, "", invalid, "default_wht_rate", msg)
	}
	if list := apitest.Decode[[]customers.CustomerOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK); len(list) != 0 {
		t.Errorf("stored %+v", list)
	}
	c := create(t, a, map[string]any{"name": "Kept Co", "default_wht_type": "PPH23", "default_wht_rate": "1.2345"})
	wht(t, c, "PPH23", "1.2345", false)
	wantProblem(t, a.Do(t, http.MethodPatch, customerPath(c.CustomerID), map[string]any{"default_wht_rate": 1.23456}),
		http.StatusUnprocessableEntity, "", invalid, "default_wht_rate", places)
	back := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil), http.StatusOK)
	wht(t, back, "PPH23", "1.2345", false)
}

func TestCustomerCCClearAndKeep(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{
		"name":                "Keep CC",
		"quotation_cc_emails": []string{" q@x.com ", "", "  "},
		"billing_cc_emails":   []string{"b@x.com"},
	})
	if !slices.Equal(c.QuotationCcEmails, []string{"q@x.com"}) {
		t.Errorf("blanks and spaces: %q", c.QuotationCcEmails)
	}
	got := patch(t, a, c.CustomerID, map[string]any{"notes": "x"})
	if !slices.Equal(got.QuotationCcEmails, []string{"q@x.com"}) || !slices.Equal(got.BillingCcEmails, []string{"b@x.com"}) {
		t.Errorf("kept %+v", got)
	}
	got = patch(t, a, c.CustomerID, map[string]any{"quotation_cc_emails": []string{}})
	if got.QuotationCcEmails == nil || len(got.QuotationCcEmails) != 0 || !slices.Equal(got.BillingCcEmails, []string{"b@x.com"}) {
		t.Errorf("cleared %+v", got)
	}
	for _, f := range []string{"quotation_cc_emails", "billing_cc_emails"} {
		wantProblem(t, a.Do(t, http.MethodPatch, customerPath(c.CustomerID), map[string]any{f: nil}),
			http.StatusUnprocessableEntity, "", "Request validation failed", f, "value cannot be null")
	}
}

func TestCustomerCCDedupesCaseInsensitively(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Dup", "billing_cc_emails": []string{"a@x.com", "A@x.com", "b@X.com"}})
	// Each address is kept as sent (trimmed): Python validated it and stored
	// the original string.
	if !slices.Equal(c.BillingCcEmails, []string{"a@x.com", "b@X.com"}) {
		t.Errorf("%q", c.BillingCcEmails)
	}
}

func TestCustomerListOrderAndFilter(t *testing.T) {
	a := newApp(t)
	b := create(t, a, map[string]any{"name": "Bravo"})
	create(t, a, map[string]any{"name": "Alpha"})
	create(t, a, map[string]any{"name": "Charlie", "active": false})
	names := func(q string) (out []string) {
		for _, c := range apitest.Decode[[]customers.CustomerOut](t, a.Do(t, http.MethodGet, path+q, nil), http.StatusOK) {
			out = append(out, c.Name)
		}
		return out
	}
	if got := names(""); !slices.Equal(got, []string{"Alpha", "Bravo", "Charlie"}) {
		t.Errorf("all: %v", got)
	}
	if got := names("?active=false"); !slices.Equal(got, []string{"Charlie"}) {
		t.Errorf("inactive: %v", got)
	}
	if got := names("?active=true"); !slices.Equal(got, []string{"Alpha", "Bravo"}) {
		t.Errorf("active: %v", got)
	}
	patch(t, a, b.CustomerID, map[string]any{"active": false})
	if got := names("?active=true"); !slices.Equal(got, []string{"Alpha"}) {
		t.Errorf("after deactivating Bravo: %v", got)
	}
	// An empty list is [], never null.
	rec := a.Do(t, http.MethodGet, path+"?active=true", nil)
	patch(t, a, apitest.Decode[[]customers.CustomerOut](t, rec, http.StatusOK)[0].CustomerID, map[string]any{"active": false})
	if body := strings.TrimSpace(a.Do(t, http.MethodGet, path+"?active=true", nil).Body.String()); body != "[]" {
		t.Errorf("empty list: %s", body)
	}
}

func TestCustomerDeleteDeactivates(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Gone", "notes": "keep"})
	got := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodDelete, customerPath(c.CustomerID), nil), http.StatusOK)
	if got.Active || got.CustomerID != c.CustomerID || *got.Notes != "keep" {
		t.Errorf("%+v", got)
	}
	back := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil), http.StatusOK)
	if back.Active {
		t.Errorf("still active: %+v", back)
	}
}

func TestCustomerPatchNullNameIs422(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Named"})
	notNull := []string{
		"name", "active", "default_currency", "matching_aliases", "quotation_cc_emails",
		"billing_cc_emails",
	}
	for _, f := range notNull {
		wantProblem(t, a.Do(t, http.MethodPatch, customerPath(c.CustomerID), map[string]any{f: nil}),
			http.StatusUnprocessableEntity, "", "Request validation failed", f, "value cannot be null")
	}
	for _, f := range []string{"active", "default_currency", "matching_aliases", "quotation_cc_emails"} {
		wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "N", f: nil}),
			http.StatusUnprocessableEntity, "", "Request validation failed", f, "value cannot be null")
	}
	// name is required on create: null is refused as a missing string is.
	if rec := a.Do(t, http.MethodPost, path, map[string]any{"name": nil}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("create with null name: %d %s", rec.Code, rec.Body)
	}
	back := apitest.Decode[customers.CustomerOut](t, a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil), http.StatusOK)
	if back.Name != "Named" {
		t.Errorf("%+v", back)
	}
}

func TestCustomerResponseHasEveryField(t *testing.T) {
	a := newApp(t)
	c := create(t, a, map[string]any{"name": "Bare"})
	rec := a.Do(t, http.MethodGet, customerPath(c.CustomerID), nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	fields := []string{
		"customer_id", "name", "matching_aliases", "active", "contact_first_name", "contact_last_name", "contact_email",
		"quotation_cc_emails", "billing_cc_emails", "contact_phone", "contact_phone_2", "account_number", "website",
		"notes", "default_currency", "billing_address", "billing_address1", "billing_address2", "billing_country",
		"billing_state", "billing_city", "billing_postal_code", "ship_to_name", "shipping_address1",
		"shipping_address2", "shipping_country", "shipping_state", "shipping_city", "shipping_postal_code",
		"shipping_phone", "shipping_delivery_instructions", "default_wht_type", "default_wht_rate",
		"default_wht_gross_up",
	}
	if len(fields) != 34 || len(raw) != 34 {
		t.Errorf("%d fields, want 34: %s", len(raw), rec.Body)
	}
	set := map[string]string{
		"customer_id": `"` + c.CustomerID.String() + `"`, "name": `"Bare"`, "active": "true", "default_currency": `"IDR"`,
		"matching_aliases": "[]", "quotation_cc_emails": "[]", "billing_cc_emails": "[]",
		"default_wht_type": `"NONE"`, "default_wht_gross_up": "false",
	}
	for _, k := range fields {
		v, ok := raw[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		want, isSet := set[k]
		if !isSet {
			want = "null"
		}
		if string(v) != want {
			t.Errorf("%s = %s, want %s", k, v, want)
		}
	}
	// The wire order is Python's CustomerOut.
	body := rec.Body.String()
	last := -1
	for _, k := range fields {
		i := strings.Index(body, `"`+k+`":`)
		if i < last {
			t.Errorf("%s out of order: %s", k, body)
		}
		last = i
	}
}

func TestCustomerFieldLengths(t *testing.T) {
	a := newApp(t)
	long := func(n int) string { return strings.Repeat("x", n) }
	for f, n := range map[string]int{
		"name": 255, "contact_first_name": 120, "contact_phone": 64, "billing_postal_code": 32,
		"shipping_country": 100, "website": 255, "default_wht_type": 16,
	} {
		rec := a.Do(t, http.MethodPost, path, map[string]any{"name": "N", f: long(n + 1)})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s of %d: %d %s", f, n+1, rec.Code, rec.Body)
		}
	}
	for _, cur := range []string{"US", "USDX"} {
		if rec := a.Do(t, http.MethodPost, path, map[string]any{"name": "N", "default_currency": cur}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("currency %q: %d", cur, rec.Code)
		}
	}
	if rec := a.Do(t, http.MethodPost, path, map[string]any{"name": ""}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty name: %d", rec.Code)
	}
	if rec := a.Do(t, http.MethodPost, path, map[string]any{}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no name: %d", rec.Code)
	}
}
