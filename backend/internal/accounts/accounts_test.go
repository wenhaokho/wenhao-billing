package accounts_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/accounts"
	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const path = "/api/v1/accounting/chart-of-accounts"

// seedCount is how many accounts the baseline migration inserts.
const seedCount = 14

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
	return apitest.New(t, func(api huma.API, d apitest.Deps) { accounts.Register(api, d.Pool) })
}

func accountPath(id int32) string { return fmt.Sprintf("%s/%d", path, id) }

// create adds an account and returns it, failing the test on anything but 201.
func create(t *testing.T, a *apitest.App, body any) accounts.AccountOut {
	t.Helper()
	return apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodPost, path, body), http.StatusCreated)
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail string) problem {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail || (code != "" && p.Code != code) {
		t.Errorf("got %q %q, want %q %q: %s", p.Code, p.Detail, code, detail, rec.Body)
	}
	return p
}

func TestAccountsListOrderedByCodeAndFiltered(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	all := apitest.Decode[[]accounts.AccountOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK)
	if len(all) != seedCount {
		t.Fatalf("%d accounts, want %d", len(all), seedCount)
	}
	if !slices.IsSortedFunc(all, func(x, y accounts.AccountOut) int {
		if x.Code < y.Code {
			return -1
		}
		if x.Code > y.Code {
			return 1
		}
		return 0
	}) {
		t.Errorf("not ordered by code: %+v", all)
	}
	if all[0] != (accounts.AccountOut{AccountID: 1, Code: "1000", Name: "Operating Bank", Type: "ASSET", Active: true}) {
		t.Errorf("first account %+v", all[0])
	}

	gone := all[3]
	apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodDelete, accountPath(gone.AccountID), nil), http.StatusOK)
	inactive := apitest.Decode[[]accounts.AccountOut](t, a.Do(t, http.MethodGet, path+"?active=false", nil), http.StatusOK)
	gone.Active = false
	if len(inactive) != 1 || inactive[0] != gone {
		t.Errorf("active=false: %+v, want [%+v]", inactive, gone)
	}
	active := apitest.Decode[[]accounts.AccountOut](t, a.Do(t, http.MethodGet, path+"?active=true", nil), http.StatusOK)
	if len(active) != seedCount-1 || slices.ContainsFunc(active, func(x accounts.AccountOut) bool { return !x.Active }) {
		t.Errorf("active=true: %+v", active)
	}
}

func TestAccountsEmptyFilterIsArray(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	rec := a.Do(t, http.MethodGet, path+"?active=false", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("%d %q, want 200 []", rec.Code, rec.Body)
	}
}

func TestAccountCreate201(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	got := create(t, a, map[string]any{"code": "6100", "name": "Software Subscriptions", "type": "EXPENSE"})
	if got.AccountID <= seedCount || got.Code != "6100" || got.Name != "Software Subscriptions" || got.Type != "EXPENSE" || !got.Active {
		t.Errorf("%+v", got)
	}
	// active is the client's when sent.
	if off := create(t, a, map[string]any{"code": "6200", "name": "Old", "type": "EXPENSE", "active": false}); off.Active {
		t.Errorf("%+v", off)
	}
	back := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodGet, accountPath(got.AccountID), nil), http.StatusOK)
	if back != got {
		t.Errorf("GET %+v, want %+v", back, got)
	}
	// Every field is present, account_id as a JSON integer.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(a.Do(t, http.MethodGet, accountPath(got.AccountID), nil).Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"account_id", "code", "name", "type", "active"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %s: %s", k, raw)
		}
	}
	if string(raw["account_id"]) != fmt.Sprint(got.AccountID) {
		t.Errorf("account_id %s", raw["account_id"])
	}
}

func TestAccountCreateDuplicateCodeIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	rec := a.Do(t, http.MethodPost, path, map[string]any{"code": "1000", "name": "Second Bank", "type": "ASSET"})
	wantProblem(t, rec, http.StatusConflict, "account_code_exists", "account code already exists")
}

func TestAccountPatchDuplicateCodeIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	rec := a.Do(t, http.MethodPatch, accountPath(2), map[string]any{"code": "1000"})
	wantProblem(t, rec, http.StatusConflict, "account_code_exists", "account code already exists")
	back := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodGet, accountPath(2), nil), http.StatusOK)
	if back.Code != "1100" {
		t.Errorf("code changed: %+v", back)
	}
}

func TestAccountPatchPartial(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	before := create(t, a, map[string]any{"code": "6100", "name": "Software", "type": "EXPENSE", "active": false})
	got := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodPatch, accountPath(before.AccountID), map[string]any{"name": "Software Subscriptions"}), http.StatusOK)
	want := before
	want.Name = "Software Subscriptions"
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	// Every field sent is applied.
	got = apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodPatch, accountPath(before.AccountID),
		map[string]any{"code": "6110", "name": "SaaS", "type": "COGS", "active": true}), http.StatusOK)
	want = accounts.AccountOut{AccountID: before.AccountID, Code: "6110", Name: "SaaS", Type: "COGS", Active: true}
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

func TestAccountTypeOutsideEnumIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"create": a.Do(t, http.MethodPost, path, map[string]any{"code": "6100", "name": "X", "type": "REVENUE"}),
		"patch":  a.Do(t, http.MethodPatch, accountPath(1), map[string]any{"type": "REVENUE"}),
	} {
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		if len(p.Errors) == 0 || p.Errors[0].Location != "type" {
			t.Errorf("%s: %s", name, rec.Body)
		}
	}
	// Lengths are Python's: code 1-10, name 1-100.
	for _, body := range []map[string]any{
		{"code": "", "name": "X", "type": "ASSET"},
		{"code": "12345678901", "name": "X", "type": "ASSET"},
		{"code": "6100", "name": "", "type": "ASSET"},
	} {
		if rec := a.Do(t, http.MethodPost, path, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	// active may be left out (true), never null, as Python's `active: bool = True`.
	wantNullRefused(t, a.Do(t, http.MethodPost, path, map[string]any{"code": "6100", "name": "X", "type": "ASSET", "active": nil}), "active")
	if rec := a.Do(t, http.MethodPatch, accountPath(1), map[string]any{"code": ""}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("patch empty code: %d %s", rec.Code, rec.Body)
	}
	if rec := a.Do(t, http.MethodGet, path+"/abc", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-integer id: %d %s", rec.Code, rec.Body)
	}
}

// wantNullRefused checks rec is the 422 for nulls sent for fields:
// platform.NotNull refuses each at its own location, all listed.
func wantNullRefused(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
	t.Helper()
	p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation_error", "Request validation failed")
	var got []string
	for _, e := range p.Errors {
		got = append(got, e.Location)
		if e.Message != "value cannot be null" {
			t.Errorf("%s: message %q", e.Location, e.Message)
		}
	}
	slices.Sort(got)
	slices.Sort(fields)
	if !slices.Equal(got, fields) {
		t.Errorf("locations %v, want %v: %s", got, fields, rec.Body)
	}
}

// Python answers 409 here (the NOT NULL violation is an IntegrityError); Go
// refuses the null before any query (platform.NotNull), reporting every one.
func TestAccountPatchNullCodeIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, field := range []string{"code", "name", "type", "active"} {
		wantNullRefused(t, a.Do(t, http.MethodPatch, accountPath(1), map[string]any{field: nil}), field)
	}
	wantNullRefused(t, a.Do(t, http.MethodPatch, accountPath(1), map[string]any{"code": nil, "name": nil, "type": nil, "active": nil}),
		"code", "name", "type", "active")
	back := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodGet, accountPath(1), nil), http.StatusOK)
	if back != (accounts.AccountOut{AccountID: 1, Code: "1000", Name: "Operating Bank", Type: "ASSET", Active: true}) {
		t.Errorf("changed: %+v", back)
	}
}

func TestAccountNotFound404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		var body any
		if method == http.MethodPatch {
			body = map[string]any{"name": "X"}
		}
		rec := a.Do(t, method, accountPath(999999), body)
		wantProblem(t, rec, http.StatusNotFound, "account_not_found", "account not found")
	}
}

func TestAccountDeleteDeactivates(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	got := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodDelete, accountPath(1), nil), http.StatusOK)
	want := accounts.AccountOut{AccountID: 1, Code: "1000", Name: "Operating Bank", Type: "ASSET", Active: false}
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	back := apitest.Decode[accounts.AccountOut](t, a.Do(t, http.MethodGet, accountPath(1), nil), http.StatusOK)
	if back != want {
		t.Errorf("GET after delete %+v", back)
	}
}

func TestAccountsRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, req := range []struct {
		method, path string
	}{
		{http.MethodGet, path},
		{http.MethodPost, path},
		{http.MethodGet, accountPath(1)},
		{http.MethodPatch, accountPath(1)},
		{http.MethodDelete, accountPath(1)},
	} {
		wantProblem(t, a.Anon(t, req.method, req.path, nil), http.StatusUnauthorized, "", "not authenticated")
	}
}

// AccountTypes is Python's AccountType, and the request enum accepts each.
func TestAccountTypesAreTheEnum(t *testing.T) {
	t.Parallel()
	want := []string{"ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"}
	if !slices.Equal(accounts.AccountTypes, want) {
		t.Errorf("%v", accounts.AccountTypes)
	}
	a := newApp(t)
	for i, typ := range accounts.AccountTypes {
		if got := create(t, a, map[string]any{"code": fmt.Sprint(7000 + i), "name": typ, "type": typ}); got.Type != typ {
			t.Errorf("%s: %+v", typ, got)
		}
	}
}

// Every type field's enum, in requests and the response, is AccountTypes
// exactly: same values, same order, nothing extra either way.
func TestAccountTypeSchemasEnumIsAccountTypes(t *testing.T) {
	t.Parallel()
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	accounts.Register(api, nil)
	reg := api.OpenAPI().Components.Schemas
	for _, name := range []string{"AccountCreate", "AccountUpdate", "AccountOut"} {
		s := reg.SchemaFromRef("#/components/schemas/" + name)
		if s == nil || s.Properties["type"] == nil {
			t.Fatalf("%s: no type property", name)
		}
		var got []string
		for _, v := range s.Properties["type"].Enum {
			str, _ := v.(string)
			got = append(got, str)
		}
		if !slices.Equal(got, accounts.AccountTypes) {
			t.Errorf("%s.type enum %v, want %v", name, got, accounts.AccountTypes)
		}
	}
}
