package vendors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors"
)

const path = "/api/v1/vendors"

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
	return apitest.New(t, func(api huma.API, d apitest.Deps) { vendors.Register(api, d.Pool) })
}

func create(t *testing.T, a *apitest.App, body any) vendors.VendorOut {
	t.Helper()
	return apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodPost, path, body), http.StatusCreated)
}

func vendorPath(id uuid.UUID) string { return path + "/" + id.String() }

func str(s string) *string { return &s }

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, detail, location, message string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail {
		t.Errorf("detail %q, want %q: %s", p.Detail, detail, rec.Body)
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

func TestCreateVendorSetsFields(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{
		"name": "Beta Supplies", "contact_email": "ap@beta.com", "contact_name": "Bea", "default_currency": "USD",
		"payment_terms_days": 45, "tax_id": "01.234", "notes": "net 45", "active": false,
	})
	want := vendors.VendorOut{
		Name: "Beta Supplies", ContactEmail: str("ap@beta.com"), ContactName: str("Bea"), DefaultCurrency: "USD",
		PaymentTermsDays: 45, TaxID: str("01.234"), Notes: str("net 45"), Active: false,
	}
	got.VendorID, got.CreatedAt = uuid.Nil, want.CreatedAt
	if got.VendorID != want.VendorID || *got.ContactEmail != *want.ContactEmail || got.Name != want.Name ||
		*got.ContactName != *want.ContactName || got.DefaultCurrency != want.DefaultCurrency ||
		got.PaymentTermsDays != want.PaymentTermsDays || *got.TaxID != *want.TaxID || *got.Notes != *want.Notes || got.Active {
		t.Errorf("%+v, want %+v", got, want)
	}
	// Every field is present in the body, nulls included.
	var raw map[string]json.RawMessage
	body := a.Do(t, http.MethodPost, path, map[string]any{"name": "Bare"}).Body.Bytes()
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active", "vendor_id", "created_at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %s: %s", k, body)
		}
	}
}

func TestVendorDefaults(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{"name": "Plain"})
	if got.DefaultCurrency != "IDR" || got.PaymentTermsDays != 30 || !got.Active ||
		got.ContactEmail != nil || got.ContactName != nil || got.TaxID != nil || got.Notes != nil || got.VendorID == uuid.Nil {
		t.Errorf("%+v", got)
	}
	back := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodGet, vendorPath(got.VendorID), nil), http.StatusOK)
	if back.VendorID != got.VendorID || back.Name != "Plain" {
		t.Errorf("GET %+v", back)
	}
}

func TestUpdateVendorEditsFields(t *testing.T) {
	a := newApp(t)
	v := create(t, a, map[string]any{"name": "Old", "contact_email": "x@old.com", "tax_id": "T1", "notes": "keep"})
	got := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodPatch, vendorPath(v.VendorID), map[string]any{
		"name": "New", "payment_terms_days": 60, "contact_name": "Cy",
	}), http.StatusOK)
	if got.Name != "New" || got.PaymentTermsDays != 60 || *got.ContactName != "Cy" ||
		*got.ContactEmail != "x@old.com" || *got.TaxID != "T1" || *got.Notes != "keep" || got.DefaultCurrency != "IDR" || !got.Active {
		t.Errorf("%+v", got)
	}
}

func TestUpdateVendorNotFound(t *testing.T) {
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPatch, vendorPath(uuid.New()), map[string]any{"name": "x"}), http.StatusNotFound, "vendor not found", "", "")
	wantProblem(t, a.Do(t, http.MethodGet, vendorPath(uuid.New()), nil), http.StatusNotFound, "vendor not found", "", "")
	wantProblem(t, a.Do(t, http.MethodDelete, vendorPath(uuid.New()), nil), http.StatusNotFound, "vendor not found", "", "")
}

func TestVendorListOrderAndFilter(t *testing.T) {
	a := newApp(t)
	b := create(t, a, map[string]any{"name": "Bravo"})
	create(t, a, map[string]any{"name": "Alpha"})
	create(t, a, map[string]any{"name": "Charlie", "active": false})
	names := func(q string) (out []string) {
		for _, v := range apitest.Decode[[]vendors.VendorOut](t, a.Do(t, http.MethodGet, path+q, nil), http.StatusOK) {
			out = append(out, v.Name)
		}
		return out
	}
	if got := names(""); len(got) != 3 || got[0] != "Alpha" || got[1] != "Bravo" || got[2] != "Charlie" {
		t.Errorf("all: %v", got)
	}
	if got := names("?active=false"); len(got) != 1 || got[0] != "Charlie" {
		t.Errorf("inactive: %v", got)
	}
	if got := names("?active=true"); len(got) != 2 || got[0] != "Alpha" {
		t.Errorf("active: %v", got)
	}
	apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodDelete, vendorPath(b.VendorID), nil), http.StatusOK)
	if rec := a.Do(t, http.MethodGet, path+"?active=maybe", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad filter: %d", rec.Code)
	}
}

func TestVendorEmptyListIsArray(t *testing.T) {
	a := newApp(t)
	if rec := a.Do(t, http.MethodGet, path, nil); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

func TestVendorEmailNormalisedAndValidated(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{"name": "Mail", "contact_email": "AP@Beta.COM"})
	if got.ContactEmail == nil || *got.ContactEmail != "AP@beta.com" {
		t.Errorf("%+v", got.ContactEmail)
	}
	wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "Mail", "contact_email": "nope"}),
		http.StatusUnprocessableEntity, "value is not a valid email address", "contact_email", "value is not a valid email address")
	patched := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodPatch, vendorPath(got.VendorID), map[string]any{"contact_email": "New@X.org"}), http.StatusOK)
	if *patched.ContactEmail != "New@x.org" {
		t.Errorf("%v", *patched.ContactEmail)
	}
	rec := a.Do(t, http.MethodPatch, vendorPath(got.VendorID), map[string]any{"contact_email": "nope"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("patch bad email: %d %s", rec.Code, rec.Body)
	}
}

func TestVendorTermsOutOfRangeIs422(t *testing.T) {
	a := newApp(t)
	v := create(t, a, map[string]any{"name": "T"})
	for _, days := range []int{366, -1} {
		if rec := a.Do(t, http.MethodPost, path, map[string]any{"name": "T2", "payment_terms_days": days}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("create %d: %d", days, rec.Code)
		}
		if rec := a.Do(t, http.MethodPatch, vendorPath(v.VendorID), map[string]any{"payment_terms_days": days}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("patch %d: %d", days, rec.Code)
		}
	}
	if rec := a.Do(t, http.MethodPost, path, map[string]any{"name": "T3", "payment_terms_days": 365}); rec.Code != http.StatusCreated {
		t.Errorf("365: %d", rec.Code)
	}
	for _, body := range []map[string]any{{"name": ""}, {"name": "x", "default_currency": "ID"}, {"name": "x", "tax_id": string(make([]byte, 101))}} {
		if rec := a.Do(t, http.MethodPost, path, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", body, rec.Code)
		}
	}
}

func TestVendorPatchNullNameIs422(t *testing.T) {
	a := newApp(t)
	v := create(t, a, map[string]any{"name": "Keep"})
	for _, f := range []string{"name", "default_currency", "payment_terms_days", "active"} {
		wantProblem(t, a.Do(t, http.MethodPatch, vendorPath(v.VendorID), map[string]any{f: nil}),
			http.StatusUnprocessableEntity, "Request validation failed", f, "value cannot be null")
	}
	wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "N", "active": nil}),
		http.StatusUnprocessableEntity, "Request validation failed", "active", "value cannot be null")
	back := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodGet, vendorPath(v.VendorID), nil), http.StatusOK)
	if back.Name != "Keep" || !back.Active {
		t.Errorf("row changed: %+v", back)
	}
}

func TestVendorPatchNullContactEmailClears(t *testing.T) {
	a := newApp(t)
	v := create(t, a, map[string]any{"name": "C", "contact_email": "a@b.com", "contact_name": "N", "tax_id": "T", "notes": "n"})
	got := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodPatch, vendorPath(v.VendorID),
		map[string]any{"contact_email": nil, "tax_id": nil, "notes": nil}), http.StatusOK)
	if got.Name != "C" || got.DefaultCurrency != "IDR" || got.PaymentTermsDays != 30 || !got.Active || got.ContactEmail != nil || got.TaxID != nil || got.Notes != nil || got.ContactName == nil || *got.ContactName != "N" {
		t.Errorf("%+v", got)
	}
}

func TestVendorDeleteDeactivates(t *testing.T) {
	a := newApp(t)
	v := create(t, a, map[string]any{"name": "D"})
	got := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodDelete, vendorPath(v.VendorID), nil), http.StatusOK)
	if got.Active || got.VendorID != v.VendorID {
		t.Errorf("%+v", got)
	}
	back := apitest.Decode[vendors.VendorOut](t, a.Do(t, http.MethodGet, vendorPath(v.VendorID), nil), http.StatusOK)
	if back.Active {
		t.Errorf("still active: %+v", back)
	}
}

func TestVendorsRequireAdmin(t *testing.T) {
	a := newApp(t)
	id := uuid.New()
	for _, c := range []struct{ method, p string }{
		{http.MethodGet, path},
		{http.MethodPost, path},
		{http.MethodGet, vendorPath(id)},
		{http.MethodPatch, vendorPath(id)},
		{http.MethodDelete, vendorPath(id)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{"name": "x"}), http.StatusUnauthorized, "not authenticated", "", "")
	}
}
