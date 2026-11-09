package business_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
)

const (
	profilePath  = "/api/v1/business-profile"
	accountsPath = profilePath + "/payment-accounts"
)

// seedUpdatedAt is the updated_at of row 1 in 00001_baseline.sql.
var seedUpdatedAt = time.Date(2026, 11, 8, 20, 27, 22, 570386000, time.UTC)

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) { business.Register(api, d.Pool, d.Clock) })
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail || p.Code != code {
		t.Errorf("got %q %q, want %q %q", p.Code, p.Detail, code, detail)
	}
}

func exec(t *testing.T, a *apitest.App, sql string, args ...any) error {
	t.Helper()
	_, err := a.Pool.Exec(context.Background(), sql, args...)
	return err
}

func profileRows(t *testing.T, a *apitest.App) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM business_profile`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func getProfile(t *testing.T, a *apitest.App) business.ProfileOut {
	t.Helper()
	return apitest.Decode[business.ProfileOut](t, a.Do(t, http.MethodGet, profilePath, nil), http.StatusOK)
}

func putProfile(t *testing.T, a *apitest.App, body any) business.ProfileOut {
	t.Helper()
	return apitest.Decode[business.ProfileOut](t, a.Do(t, http.MethodPut, profilePath, body), http.StatusOK)
}

// allNull reports the profile fields that are not null.
func allNull(p business.ProfileOut) []string {
	var set []string
	v := reflect.ValueOf(p)
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
			set = append(set, v.Type().Field(i).Name)
		}
	}
	return set
}

func TestProfileGetReturnsSeedRow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	rec := a.Do(t, http.MethodGet, profilePath, nil)
	p := apitest.Decode[business.ProfileOut](t, rec, http.StatusOK)
	if set := allNull(p); len(set) != 0 || !p.UpdatedAt.Equal(seedUpdatedAt) {
		t.Errorf("set %v, updated_at %v", set, p.UpdatedAt)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"name", "address", "contact_email", "contact_phone", "invoice_title", "invoice_summary",
		"logo_url", "default_notes", "npwp", "npwp_name", "updated_at",
	} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %s: %s", k, rec.Body)
		}
	}
}

func TestProfileGetMissingRowWritesNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if err := exec(t, a, `DELETE FROM business_profile`); err != nil {
		t.Fatal(err)
	}
	p := getProfile(t, a)
	if set := allNull(p); len(set) != 0 || !p.UpdatedAt.Equal(apitest.Start) {
		t.Errorf("set %v, updated_at %v", set, p.UpdatedAt)
	}
	if n := profileRows(t, a); n != 0 {
		t.Errorf("%d rows after GET", n)
	}
	// The exported reader answers the same defaults.
	got, err := business.Profile(context.Background(), businessdb.New(a.Pool), apitest.Start)
	if err != nil || len(allNull(got)) != 0 || !got.UpdatedAt.Equal(apitest.Start) {
		t.Errorf("%+v %v", got, err)
	}
}

func TestProfilePutPartialAndNullClears(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	p := putProfile(t, a, map[string]any{"name": "Wenhao Software", "npwp": "01.234.567.8-901.000", "address": "Batam"})
	if *p.Name != "Wenhao Software" || *p.Npwp != "01.234.567.8-901.000" || *p.Address != "Batam" ||
		p.ContactEmail != nil || p.NpwpName != nil || p.UpdatedAt.Equal(seedUpdatedAt) {
		t.Errorf("%+v", p)
	}
	p = putProfile(t, a, map[string]any{"address": nil, "invoice_title": "INVOICE"})
	if *p.Name != "Wenhao Software" || *p.Npwp != "01.234.567.8-901.000" || p.Address != nil || *p.InvoiceTitle != "INVOICE" {
		t.Errorf("%+v", p)
	}
	if back := getProfile(t, a); !reflect.DeepEqual(allNull(back), allNull(p)) || *back.InvoiceTitle != "INVOICE" {
		t.Errorf("GET %+v", back)
	}
	got, err := business.Profile(context.Background(), businessdb.New(a.Pool), apitest.Start)
	if err != nil || *got.Name != "Wenhao Software" || !got.UpdatedAt.Equal(p.UpdatedAt.Time) {
		t.Errorf("Profile %+v %v", got, err)
	}

	// Overlong values are refused at validation, not by the column.
	for field, limit := range map[string]int{
		"name": 255, "contact_email": 255, "contact_phone": 50,
		"invoice_title": 255, "npwp": 32, "npwp_name": 255,
	} {
		if rec := a.Do(t, http.MethodPut, profilePath, map[string]any{field: strings.Repeat("x", limit+1)}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", field, rec.Code, rec.Body)
		}
		putProfile(t, a, map[string]any{field: strings.Repeat("x", limit)})
	}

	// A PUT creates the row when it is missing.
	if err := exec(t, a, `DELETE FROM business_profile`); err != nil {
		t.Fatal(err)
	}
	p = putProfile(t, a, map[string]any{"contact_phone": "+62 778"})
	if *p.ContactPhone != "+62 778" || p.Name != nil || profileRows(t, a) != 1 {
		t.Errorf("%+v, %d rows", p, profileRows(t, a))
	}
}

func TestProfilePutEmptyKeepsUpdatedAt(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	p := putProfile(t, a, map[string]any{})
	if !p.UpdatedAt.Equal(seedUpdatedAt) || len(allNull(p)) != 0 {
		t.Errorf("%+v", p)
	}
}

type acct = map[string]any

func putAccounts(t *testing.T, a *apitest.App, body any) []business.PaymentAccountOut {
	t.Helper()
	return apitest.Decode[[]business.PaymentAccountOut](t, a.Do(t, http.MethodPut, accountsPath, body), http.StatusOK)
}

func listAccounts(t *testing.T, a *apitest.App) []business.PaymentAccountOut {
	t.Helper()
	return apitest.Decode[[]business.PaymentAccountOut](t, a.Do(t, http.MethodGet, accountsPath, nil), http.StatusOK)
}

// summary is "CUR:instructions:default" per account, in order.
func summary(accounts []business.PaymentAccountOut) []string {
	out := []string{}
	for _, x := range accounts {
		d := ""
		if x.IsDefault {
			d = "*"
		}
		out = append(out, x.Currency+":"+x.Instructions+d)
	}
	return out
}

func TestReplaceAndListPaymentAccounts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	got := putAccounts(t, a, []acct{
		{"currency": "SGD", "instructions": "DBS SGD", "is_default": false},
		{"currency": "IDR", "instructions": "OCBC NISP IDR", "is_default": true},
	})
	if s := summary(got); !reflect.DeepEqual(s, []string{"IDR:OCBC NISP IDR*", "SGD:DBS SGD"}) {
		t.Errorf("%v", s)
	}
	for _, x := range got {
		if x.ID == 0 || x.UpdatedAt.IsZero() {
			t.Errorf("%+v", x)
		}
	}
	if !reflect.DeepEqual(listAccounts(t, a), got) {
		t.Errorf("GET differs from PUT")
	}
	all, err := business.PaymentAccounts(context.Background(), businessdb.New(a.Pool))
	if err != nil || !reflect.DeepEqual(all, got) {
		t.Errorf("PaymentAccounts %+v %v", all, err)
	}
}

func TestReplaceIsFullOverwrite(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	putAccounts(t, a, []acct{{"currency": "USD", "instructions": "Wells", "is_default": true}})
	putAccounts(t, a, []acct{{"currency": "SGD", "instructions": "DBS", "is_default": true}})
	if s := summary(listAccounts(t, a)); !reflect.DeepEqual(s, []string{"SGD:DBS*"}) {
		t.Errorf("%v", s)
	}
}

func TestRejectDuplicateCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPut, accountsPath, []acct{
		{"currency": "SGD", "instructions": "DBS", "is_default": false},
		{"currency": "SGD", "instructions": "UOB", "is_default": true},
	}), http.StatusUnprocessableEntity, "duplicate_currency", "Duplicate currency in payment accounts")
}

func TestDuplicateCurrencyCaseInsensitive(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPut, accountsPath, []acct{
		{"currency": "sgd", "instructions": "DBS"},
		{"currency": "SGD", "instructions": "UOB"},
	}), http.StatusUnprocessableEntity, "duplicate_currency", "Duplicate currency in payment accounts")
}

func TestRejectTwoDefaults(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPut, accountsPath, []acct{
		{"currency": "SGD", "instructions": "DBS", "is_default": true},
		{"currency": "USD", "instructions": "Wells", "is_default": true},
	}), http.StatusUnprocessableEntity, "multiple_default_accounts", "At most one account can be the default")
}

func TestRejectUnsupportedCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPut, accountsPath, []acct{{"currency": "GBP", "instructions": "Barclays", "is_default": true}}),
		http.StatusUnprocessableEntity, "unsupported_currency", "Unsupported currency: GBP")
	// Sorted and joined; checked after duplicates and before defaults.
	wantProblem(t, a.Do(t, http.MethodPut, accountsPath, []acct{
		{"currency": "jpy", "instructions": "x", "is_default": true},
		{"currency": "EUR", "instructions": "y", "is_default": true},
	}), http.StatusUnprocessableEntity, "unsupported_currency", "Unsupported currency: EUR, JPY")
	if got := listAccounts(t, a); len(got) != 0 {
		t.Errorf("%v", summary(got))
	}
}

func TestPaymentAccountFieldsRequired(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, body := range []acct{
		{"instructions": "x"},
		{"currency": "IDR"},
		{"currency": nil, "instructions": "x"},
		{"currency": "IDR", "instructions": nil},
		{"currency": "IDR", "instructions": "x", "is_default": nil},
	} {
		if rec := a.Do(t, http.MethodPut, accountsPath, []acct{body}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestPaymentAccountsLowercaseCurrencyUppercased(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	got := putAccounts(t, a, []acct{{"currency": "idr", "instructions": "BCA"}})
	if s := summary(got); !reflect.DeepEqual(s, []string{"IDR:BCA"}) {
		t.Errorf("%v", s)
	}
	var cur string
	if err := a.Pool.QueryRow(context.Background(), `SELECT currency FROM payment_account`).Scan(&cur); err != nil || cur != "IDR" {
		t.Errorf("%q %v", cur, err)
	}
}

func TestPaymentAccountsEmptyIsArray(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	putAccounts(t, a, []acct{{"currency": "USD", "instructions": "Wells"}})
	if rec := a.Do(t, http.MethodPut, accountsPath, []acct{}); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
	if rec := a.Do(t, http.MethodGet, accountsPath, nil); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

// A null body is not an empty list: Python's list[PaymentAccountIn] refuses
// it, and the accounts stay.
func TestPaymentAccountsNullBodyRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	putAccounts(t, a, []acct{{"currency": "USD", "instructions": "Wells"}})
	if rec := a.Do(t, http.MethodPut, accountsPath, "null"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("%d %s", rec.Code, rec.Body)
	} else {
		t.Logf("PUT null: %s", rec.Body)
	}
	if s := summary(listAccounts(t, a)); !reflect.DeepEqual(s, []string{"USD:Wells"}) {
		t.Errorf("%v", s)
	}
}

func TestPaymentAccountsPutCreatesMissingProfile(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if err := exec(t, a, `DELETE FROM business_profile`); err != nil {
		t.Fatal(err)
	}
	if s := summary(putAccounts(t, a, []acct{{"currency": "SGD", "instructions": "DBS"}})); !reflect.DeepEqual(s, []string{"SGD:DBS"}) {
		t.Errorf("%v", s)
	}
	if n := profileRows(t, a); n != 1 {
		t.Errorf("%d profile rows", n)
	}
}

func TestRelationshipOrdersAndReadsAccounts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if err := exec(t, a, `INSERT INTO payment_account (business_profile_id, currency, instructions, is_default)
		VALUES (1, 'USD', 'Wells Fargo USD', false), (1, 'IDR', 'OCBC NISP IDR', true)`); err != nil {
		t.Fatal(err)
	}
	want := []string{"IDR:OCBC NISP IDR*", "USD:Wells Fargo USD"}
	if s := summary(listAccounts(t, a)); !reflect.DeepEqual(s, want) {
		t.Errorf("%v", s)
	}
	all, err := business.PaymentAccounts(context.Background(), businessdb.New(a.Pool))
	if s := summary(all); err != nil || !reflect.DeepEqual(s, want) {
		t.Errorf("%v %v", s, err)
	}
}

// constraint is the name of the constraint err violated, or "".
func constraint(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.ConstraintName
	}
	return ""
}

func TestDuplicateCurrencyRejected(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const ins = `INSERT INTO payment_account (business_profile_id, currency, instructions, is_default) VALUES (1, 'SGD', $1, false)`
	if err := exec(t, a, ins, "DBS SGD"); err != nil {
		t.Fatal(err)
	}
	if c := constraint(exec(t, a, ins, "UOB SGD")); c != "uq_payment_account_currency" {
		t.Errorf("constraint %q", c)
	}
}

func TestSecondDefaultRejected(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const ins = `INSERT INTO payment_account (business_profile_id, currency, instructions, is_default) VALUES (1, $1, $2, true)`
	if err := exec(t, a, ins, "SGD", "DBS SGD"); err != nil {
		t.Fatal(err)
	}
	if c := constraint(exec(t, a, ins, "USD", "Wells USD")); c != "uq_payment_account_one_default" {
		t.Errorf("constraint %q", c)
	}
}

// Two admins replacing the list at once: each replace runs whole, one after
// the other, so the result is one of the two lists and no request fails.
func TestReplacePaymentAccountsConcurrent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	lists := [][]acct{
		{{"currency": "IDR", "instructions": "BCA", "is_default": true}, {"currency": "SGD", "instructions": "DBS"}},
		{{"currency": "SGD", "instructions": "UOB", "is_default": true}, {"currency": "USD", "instructions": "Wells"}},
	}
	wants := [][]string{{"IDR:BCA*", "SGD:DBS"}, {"SGD:UOB*", "USD:Wells"}}
	var wg sync.WaitGroup
	codes := make(chan string, 40)
	for _, body := range lists {
		wg.Go(func() {
			for range 20 {
				if rec := a.Do(t, http.MethodPut, accountsPath, body); rec.Code != http.StatusOK {
					codes <- rec.Body.String()
				}
			}
		})
	}
	wg.Wait()
	close(codes)
	for body := range codes {
		t.Errorf("failed PUT: %s", body)
	}
	got := summary(listAccounts(t, a))
	if !reflect.DeepEqual(got, wants[0]) && !reflect.DeepEqual(got, wants[1]) {
		t.Errorf("mixed list %v", got)
	}
}
