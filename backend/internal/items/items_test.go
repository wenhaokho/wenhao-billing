package items_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
)

const path = "/api/v1/items"

// expenseAccount is a seeded COGS account (5000 External Contractor Fees).
const expenseAccount = 9

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
	return apitest.New(t, func(api huma.API, d apitest.Deps) { items.Register(api, d.Pool) })
}

func create(t *testing.T, a *apitest.App, body map[string]any) items.ItemOut {
	t.Helper()
	return apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodPost, path, body), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, body map[string]any) items.ItemOut {
	t.Helper()
	return apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodPatch, itemPath(id), body), http.StatusOK)
}

func itemPath(id uuid.UUID) string { return path + "/" + id.String() }

// minimal is Python's _minimal_kwargs plus extra.
func minimal(extra map[string]any) map[string]any {
	b := map[string]any{"name": "Support retainer", "item_type": "SERVICE", "default_currency": "USD"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func cfTarget() map[string]any {
	return map[string]any{
		"zone_id": "zone1", "record_id": "rec1", "record_name": "WWW", "record_type": "a",
		"live_content": "1.2.3.4", "maintenance_content": "5.6.7.8",
	}
}

func hosting(extra map[string]any) map[string]any {
	b := map[string]any{
		"name": "Website hosting", "item_type": "SERVICE", "is_hosting": true, "hosting_domain": "Example.COM",
		"hosting_grace_days": 7, "hosting_suspension_enabled": true, "cloudflare_target": cfTarget(),
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail, location, message string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail || (code != "" && p.Code != code) {
		t.Errorf("code %q detail %q, want %q %q: %s", p.Code, p.Detail, code, detail, rec.Body)
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

func price(t *testing.T, d interface{ StringFixed(int32) string }, want string) {
	t.Helper()
	if reflect.ValueOf(d).IsNil() {
		t.Errorf("price nil, want %s", want)
		return
	}
	if got := d.StringFixed(4); got != want {
		t.Errorf("price %s, want %s", got, want)
	}
}

func countTargets(t *testing.T, a *apitest.App, itemID uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM cloudflare_targets WHERE item_id = $1`, itemID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateItemBasic(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{"name": "Consulting", "item_type": "SERVICE", "default_currency": "USD"})
	if got.ItemID == uuid.Nil || got.Name != "Consulting" || got.IsHosting || got.HostingStatus != nil || got.CloudflareTarget != nil {
		t.Errorf("%+v", got)
	}
}

func TestCreateHostingItemSetsActiveStatusAndNormalizes(t *testing.T) {
	a := newApp(t)
	got := create(t, a, hosting(nil))
	if got.HostingStatus == nil || *got.HostingStatus != "ACTIVE" || got.HostingDomain == nil || *got.HostingDomain != "example.com" {
		t.Errorf("%+v", got)
	}
	tg := got.CloudflareTarget
	if tg == nil || tg.RecordName != "www" || tg.RecordType != "A" {
		t.Fatalf("target %+v", tg)
	}
	// Every string is trimmed.
	spaced := hosting(map[string]any{"name": "Spaced", "hosting_domain": "  Spaced.COM ", "cloudflare_target": map[string]any{
		"zone_id": " z ", "record_id": " r ", "record_name": " Api.Example.COM ", "record_type": " cname ",
		"live_content": " live ", "maintenance_content": " maint ",
	}})
	s := create(t, a, spaced)
	st := s.CloudflareTarget
	if *s.HostingDomain != "spaced.com" || st.ZoneID != "z" || st.RecordID != "r" || st.RecordName != "api.example.com" ||
		st.RecordType != "CNAME" || st.LiveContent != "live" || st.MaintenanceContent != "maint" || !st.Proxied {
		t.Errorf("%+v %+v", s, st)
	}
}

func TestUpdateItemNotFound(t *testing.T) {
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(uuid.New()), map[string]any{"name": "x"}), http.StatusNotFound, "item_not_found", "item not found", "", "")
	wantProblem(t, a.Do(t, http.MethodGet, itemPath(uuid.New()), nil), http.StatusNotFound, "item_not_found", "item not found", "", "")
	wantProblem(t, a.Do(t, http.MethodDelete, itemPath(uuid.New()), nil), http.StatusNotFound, "item_not_found", "item not found", "", "")
}

func TestUpdateItemEditsFields(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(map[string]any{"name": "Web Hosting", "sku": "WH-1", "description": "keep"}))
	got := patch(t, a, it.ItemID, map[string]any{"name": "Web Hosting Renamed"})
	if got.Name != "Web Hosting Renamed" || *got.Sku != "WH-1" || *got.Description != "keep" || got.DefaultCurrency != "USD" {
		t.Errorf("%+v", got)
	}
	back := apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodGet, itemPath(it.ItemID), nil), http.StatusOK)
	if back.Name != "Web Hosting Renamed" {
		t.Errorf("GET %+v", back)
	}
	cleared := patch(t, a, it.ItemID, map[string]any{"sku": nil, "description": nil})
	if cleared.Sku != nil || cleared.Description != nil || cleared.Name != "Web Hosting Renamed" {
		t.Errorf("cleared %+v", cleared)
	}
}

func TestItemDefaultsSoldTruePurchasedFalse(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{"name": "Plain", "item_type": "USAGE"})
	if !got.IsSold || got.IsPurchased || got.ExpenseAccountID != nil || got.DefaultPurchasePrice != nil ||
		!got.Active || got.IsHosting || got.DefaultCurrency != "IDR" {
		t.Errorf("%+v", got)
	}
}

func TestItemCanBePurchasedOnly(t *testing.T) {
	a := newApp(t)
	got := create(t, a, minimal(map[string]any{"name": "Cloud hosting", "is_sold": false, "is_purchased": true, "default_purchase_price": "120.00"}))
	if got.IsSold || !got.IsPurchased {
		t.Errorf("%+v", got)
	}
	price(t, got.DefaultPurchasePrice, "120.0000")
	body := a.Do(t, http.MethodGet, itemPath(got.ItemID), nil).Body.String()
	if !strings.Contains(body, `"default_purchase_price":"120.0000"`) {
		t.Errorf("wire: %s", body)
	}
}

func TestItemCanBeBothSoldAndPurchased(t *testing.T) {
	a := newApp(t)
	got := create(t, a, minimal(map[string]any{
		"name": "Reseller SaaS seat", "is_sold": true, "is_purchased": true,
		"default_unit_price": 50.00, "default_purchase_price": "30.00",
	}))
	if !got.IsSold || !got.IsPurchased {
		t.Errorf("%+v", got)
	}
	price(t, got.DefaultUnitPrice, "50.0000")
	price(t, got.DefaultPurchasePrice, "30.0000")
}

func TestItemCreateAcceptsPurchaseFields(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{
		"name": "AWS", "item_type": "USAGE", "is_sold": false, "is_purchased": true,
		"default_purchase_price": 42, "expense_account_id": expenseAccount,
	})
	if got.IsSold || !got.IsPurchased || got.ExpenseAccountID == nil || *got.ExpenseAccountID != expenseAccount {
		t.Errorf("%+v", got)
	}
	price(t, got.DefaultPurchasePrice, "42.0000")
}

func TestItemOutSerializesNewFields(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(map[string]any{
		"name": "Reseller", "is_sold": true, "is_purchased": true,
		"default_unit_price": 100, "default_purchase_price": 60,
	}))
	rec := a.Do(t, http.MethodGet, itemPath(it.ItemID), nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"is_sold": "true", "is_purchased": "true", "default_purchase_price": `"60.0000"`, "default_unit_price": `"100.0000"`}
	for k, v := range want {
		if string(raw[k]) != v {
			t.Errorf("%s = %s, want %s", k, raw[k], v)
		}
	}
	// Every field is present, nulls included, in the order of Python's ItemOut.
	fields := []string{
		"sku", "name", "item_type", "description", "default_currency", "default_unit_price",
		"default_purchase_price", "revenue_account_id", "expense_account_id", "is_sold", "is_purchased", "active",
		"is_hosting", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "item_id", "created_at",
		"hosting_status", "hosting_last_paid_at", "hosting_last_action_at", "hosting_last_error", "cloudflare_target",
	}
	if len(raw) != len(fields) {
		t.Errorf("%d fields, want %d: %s", len(raw), len(fields), rec.Body)
	}
	for _, k := range fields {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestItemUpdateCanTogglePurchased(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(nil))
	got := patch(t, a, it.ItemID, map[string]any{"is_purchased": true, "default_purchase_price": 10})
	if !got.IsPurchased || !got.IsSold {
		t.Errorf("%+v", got)
	}
	price(t, got.DefaultPurchasePrice, "10.0000")
}

func TestHostingItemCreatesWithCloudflareTarget(t *testing.T) {
	a := newApp(t)
	got := create(t, a, map[string]any{
		"name": "Acme hosting", "item_type": "SERVICE", "default_currency": "USD", "default_unit_price": "240.0000",
		"is_sold": true, "is_purchased": false, "active": true, "is_hosting": true, "hosting_domain": "example.com",
		"hosting_grace_days": 3, "hosting_suspension_enabled": true,
		"cloudflare_target": map[string]any{
			"zone_id": "zone_123", "record_id": "record_123", "record_name": "example.com", "record_type": "CNAME",
			"live_content": "live.example.net", "maintenance_content": "maintenance.example.net", "proxied": true,
		},
	})
	if !got.IsHosting || *got.HostingStatus != "ACTIVE" || *got.HostingDomain != "example.com" || *got.HostingGraceDays != 3 || !*got.HostingSuspensionEnabled {
		t.Errorf("%+v", got)
	}
	tg := got.CloudflareTarget
	if tg == nil || tg.RecordName != "example.com" || tg.ProviderStatus != "ACTIVE" || tg.ItemID != got.ItemID ||
		tg.TargetID == uuid.Nil || tg.LastActionAt != nil || tg.LastError != nil {
		t.Errorf("target %+v", tg)
	}
	// Get, for later plans, reads the same item and target.
	out, err := items.Get(context.Background(), itemsdb.New(a.Pool), got.ItemID)
	if err != nil || out.CloudflareTarget == nil || out.CloudflareTarget.TargetID != tg.TargetID {
		t.Errorf("Get: %v %+v", err, out)
	}
	if _, err := items.Get(context.Background(), itemsdb.New(a.Pool), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Get unknown: %v", err)
	}
}

func TestHostingItemMissingFieldsIs422(t *testing.T) {
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPost, path, map[string]any{"name": "H", "item_type": "SERVICE", "is_hosting": true}),
		http.StatusUnprocessableEntity, "hosting_item_incomplete",
		"hosting item requires: hosting_domain, hosting_grace_days, hosting_suspension_enabled, cloudflare_target", "", "")
	wantProblem(t, a.Do(t, http.MethodPost, path, hosting(map[string]any{"hosting_domain": "", "cloudflare_target": nil})),
		http.StatusUnprocessableEntity, "hosting_item_incomplete", "hosting item requires: hosting_domain, cloudflare_target", "", "")
	// A non-hosting item needs none of them.
	create(t, a, map[string]any{"name": "N", "item_type": "SERVICE", "is_hosting": false})
}

func TestItemPatchUpdatesExistingTarget(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(nil))
	first := patch(t, a, it.ItemID, map[string]any{"cloudflare_target": cfTarget()})
	if first.CloudflareTarget == nil || first.CloudflareTarget.RecordName != "www" {
		t.Fatalf("%+v", first.CloudflareTarget)
	}
	tg := cfTarget()
	tg["live_content"], tg["record_type"], tg["proxied"] = "9.9.9.9", "aaaa", false
	second := patch(t, a, it.ItemID, map[string]any{"cloudflare_target": tg})
	st := second.CloudflareTarget
	if st.TargetID != first.CloudflareTarget.TargetID || st.LiveContent != "9.9.9.9" || st.RecordType != "AAAA" || st.Proxied ||
		st.ProviderStatus != "ACTIVE" || !st.CreatedAt.Equal(first.CloudflareTarget.CreatedAt.Time) {
		t.Errorf("%+v", st)
	}
	// updated_at moves on every write (each PATCH is its own transaction).
	if !st.UpdatedAt.After(first.CloudflareTarget.UpdatedAt.Time) {
		t.Errorf("updated_at %v, first %v", st.UpdatedAt, first.CloudflareTarget.UpdatedAt)
	}
	if n := countTargets(t, a, it.ItemID); n != 1 {
		t.Errorf("%d targets, want 1", n)
	}
	// A PATCH without a target (or with null) keeps it.
	kept := patch(t, a, it.ItemID, map[string]any{"name": "Renamed", "cloudflare_target": nil})
	if kept.CloudflareTarget == nil || kept.CloudflareTarget.LiveContent != "9.9.9.9" {
		t.Errorf("kept %+v", kept.CloudflareTarget)
	}
	list := apitest.Decode[[]items.ItemOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK)
	if len(list) != 1 || list[0].CloudflareTarget == nil || list[0].CloudflareTarget.TargetID != st.TargetID {
		t.Errorf("list %+v", list)
	}
}

func TestItemPatchHostingSetsActive(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(nil))
	got := patch(t, a, it.ItemID, map[string]any{"is_hosting": true, "hosting_domain": " Shop.Example.COM "})
	if got.HostingStatus == nil || *got.HostingStatus != "ACTIVE" || *got.HostingDomain != "shop.example.com" {
		t.Errorf("%+v", got)
	}
	suspended := patch(t, a, it.ItemID, map[string]any{"hosting_status": "SUSPENDED"})
	if *suspended.HostingStatus != "SUSPENDED" {
		t.Errorf("%+v", suspended.HostingStatus)
	}
	// Cleared on a hosting item, the status comes back as ACTIVE.
	if back := patch(t, a, it.ItemID, map[string]any{"hosting_status": nil}); *back.HostingStatus != "ACTIVE" {
		t.Errorf("%+v", back.HostingStatus)
	}
	off := patch(t, a, it.ItemID, map[string]any{"is_hosting": false, "hosting_status": nil})
	if off.HostingStatus != nil || off.IsHosting {
		t.Errorf("off %+v", off)
	}
	if rec := a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{"hosting_status": "PAUSED"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad status: %d", rec.Code)
	}
}

func TestItemPriceFivePlacesIs422(t *testing.T) {
	a := newApp(t)
	const (
		places = "Decimal input should have no more than 4 decimal places"
		whole  = "Decimal input should have no more than 15 digits before the decimal point"
	)
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"default_unit_price": 1.23456})),
		http.StatusUnprocessableEntity, "", places, "default_unit_price", places)
	// A string with five places already fails Dec4's schema pattern, at the field.
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"default_purchase_price": "1.23456"})),
		http.StatusUnprocessableEntity, "", "Request validation failed", "default_purchase_price", "expected value to match exactly one schema but matched none")
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"default_unit_price": -1})),
		http.StatusUnprocessableEntity, "", "Input should be greater than or equal to 0", "default_unit_price", "Input should be greater than or equal to 0")
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"default_unit_price": "1000000000000000"})),
		http.StatusUnprocessableEntity, "", whole, "default_unit_price", whole)
	it := create(t, a, minimal(map[string]any{"default_unit_price": "999999999999999.9999", "default_purchase_price": json.Number("1.50000")}))
	price(t, it.DefaultUnitPrice, "999999999999999.9999")
	price(t, it.DefaultPurchasePrice, "1.5000") // trailing zeros are not places
	// Both prices refused: the first is reported.
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"default_unit_price": -1, "default_purchase_price": -1})),
		http.StatusUnprocessableEntity, "", "Input should be greater than or equal to 0", "default_unit_price", "Input should be greater than or equal to 0")
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{"default_purchase_price": 0.00001}),
		http.StatusUnprocessableEntity, "", places, "default_purchase_price", places)
	if cleared := patch(t, a, it.ItemID, map[string]any{"default_unit_price": nil}); cleared.DefaultUnitPrice != nil {
		t.Errorf("%+v", cleared.DefaultUnitPrice)
	}
}

func TestItemDuplicateSkuIs409(t *testing.T) {
	a := newApp(t)
	create(t, a, minimal(map[string]any{"sku": "SKU-1"}))
	other := create(t, a, minimal(map[string]any{"sku": "SKU-2"}))
	wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{"sku": "SKU-1"})), http.StatusConflict, "sku_exists", "sku already exists", "", "")
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(other.ItemID), map[string]any{"sku": "SKU-1"}), http.StatusConflict, "sku_exists", "sku already exists", "", "")
	// Many items may have no sku.
	create(t, a, minimal(nil))
	create(t, a, minimal(nil))
}

func TestItemUnknownAccountIs422(t *testing.T) {
	a := newApp(t)
	for _, f := range []string{"revenue_account_id", "expense_account_id"} {
		wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{f: 9999})), http.StatusUnprocessableEntity, "account_not_found", "account not found", "", "")
	}
	it := create(t, a, minimal(map[string]any{"revenue_account_id": 6}))
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{"expense_account_id": 9999}), http.StatusUnprocessableEntity, "account_not_found", "account not found", "", "")
	// A hosting create that fails on its account writes no target either.
	wantProblem(t, a.Do(t, http.MethodPost, path, hosting(map[string]any{"revenue_account_id": 9999})), http.StatusUnprocessableEntity, "account_not_found", "account not found", "", "")
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM cloudflare_targets`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d targets: %v", n, err)
	}
}

func TestItemListFilters(t *testing.T) {
	a := newApp(t)
	create(t, a, map[string]any{"name": "Bravo", "item_type": "USAGE"})
	create(t, a, map[string]any{"name": "Alpha", "item_type": "SERVICE"})
	create(t, a, map[string]any{"name": "Charlie", "item_type": "FIXED_FEE", "active": false})
	create(t, a, hosting(map[string]any{"name": "Delta"}))
	names := func(q string) (out []string) {
		for _, it := range apitest.Decode[[]items.ItemOut](t, a.Do(t, http.MethodGet, path+q, nil), http.StatusOK) {
			out = append(out, it.Name)
		}
		return out
	}
	for q, want := range map[string][]string{
		"":                                {"Alpha", "Bravo", "Charlie", "Delta"},
		"?item_type=":                     {"Alpha", "Bravo", "Charlie", "Delta"},
		"?active=false":                   {"Charlie"},
		"?active=true":                    {"Alpha", "Bravo", "Delta"},
		"?item_type=SERVICE":              {"Alpha", "Delta"},
		"?item_type=NOPE":                 nil,
		"?is_hosting=true":                {"Delta"},
		"?is_hosting=false&active=true":   {"Alpha", "Bravo"},
		"?item_type=SERVICE&is_hosting=0": {"Alpha"},
	} {
		if got := names(q); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", q, got, want)
		}
	}
	if rec := a.Do(t, http.MethodGet, path+"?is_hosting=maybe", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad filter: %d", rec.Code)
	}
}

func TestItemResponseHasNullTarget(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(nil))
	for _, rec := range []*httptest.ResponseRecorder{
		a.Do(t, http.MethodGet, itemPath(it.ItemID), nil),
		a.Do(t, http.MethodGet, path, nil),
		a.Do(t, http.MethodDelete, itemPath(it.ItemID), nil),
	} {
		if !strings.Contains(rec.Body.String(), `"cloudflare_target":null`) {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	}
	if rec := a.Do(t, http.MethodGet, path+"?active=nope", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("%d", rec.Code)
	}
}

func TestItemEmptyListIsArray(t *testing.T) {
	a := newApp(t)
	if rec := a.Do(t, http.MethodGet, path, nil); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

func TestItemDeleteDeactivatesAndKeepsTarget(t *testing.T) {
	a := newApp(t)
	it := create(t, a, hosting(nil))
	got := apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodDelete, itemPath(it.ItemID), nil), http.StatusOK)
	if got.Active || got.CloudflareTarget == nil {
		t.Errorf("%+v", got)
	}
	if back := apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodGet, itemPath(it.ItemID), nil), http.StatusOK); back.Active {
		t.Errorf("still active")
	}
}

func TestItemNullOnNotNullIs422(t *testing.T) {
	a := newApp(t)
	it := create(t, a, minimal(nil))
	for _, f := range []string{"name", "item_type", "default_currency", "is_sold", "is_purchased", "active", "is_hosting"} {
		wantProblem(t, a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{f: nil}),
			http.StatusUnprocessableEntity, "", "Request validation failed", f, "value cannot be null")
	}
	for _, f := range []string{"default_currency", "is_sold", "is_purchased", "active", "is_hosting"} {
		wantProblem(t, a.Do(t, http.MethodPost, path, minimal(map[string]any{f: nil})),
			http.StatusUnprocessableEntity, "", "Request validation failed", f, "value cannot be null")
	}
	tg := cfTarget()
	tg["proxied"] = nil
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{"cloudflare_target": tg}),
		http.StatusUnprocessableEntity, "", "Request validation failed", "cloudflare_target.proxied", "value cannot be null")
	// An incomplete target is reported at its fields, on create and PATCH.
	incomplete := map[string]any{"cloudflare_target": map[string]any{
		"zone_id": "z", "record_name": "www", "record_type": "A",
		"live_content": "1", "maintenance_content": "2",
	}}
	wantProblem(t, a.Do(t, http.MethodPost, path, hosting(incomplete)),
		http.StatusUnprocessableEntity, "", "Request validation failed", "cloudflare_target.record_id", "expected required property record_id to be present")
	tooShort := cfTarget()
	tooShort["record_name"] = ""
	wantProblem(t, a.Do(t, http.MethodPatch, itemPath(it.ItemID), map[string]any{"cloudflare_target": tooShort}),
		http.StatusUnprocessableEntity, "", "Request validation failed", "cloudflare_target.record_name", "expected length >= 1")
	for _, body := range []map[string]any{
		{"name": "x", "item_type": "GOODS"},
		{"name": "", "item_type": "SERVICE"},
		{"name": "x", "item_type": "SERVICE", "hosting_grace_days": 366},
		{"name": "x", "item_type": "SERVICE", "sku": strings.Repeat("s", 51)},
		hosting(map[string]any{"cloudflare_target": map[string]any{"zone_id": "z"}}),
	} {
		if rec := a.Do(t, http.MethodPost, path, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	back := apitest.Decode[items.ItemOut](t, a.Do(t, http.MethodGet, itemPath(it.ItemID), nil), http.StatusOK)
	if back.Name != "Support retainer" || !back.Active {
		t.Errorf("row changed: %+v", back)
	}
}

// TestItemEnumsMatch keeps the enum tags in step with ItemTypes and
// HostingStatuses.
func TestItemEnumsMatch(t *testing.T) {
	check := func(v any, field string, want []string) {
		t.Helper()
		f, ok := reflect.TypeOf(v).FieldByName(field)
		if !ok {
			t.Fatalf("%T has no %s", v, field)
		}
		if got := strings.Split(f.Tag.Get("enum"), ","); !slices.Equal(got, want) {
			t.Errorf("%T.%s enum %v, want %v", v, field, got, want)
		}
	}
	for _, v := range []any{items.ItemOut{}, items.ItemCreate{}, items.ItemUpdate{}} {
		check(v, "ItemType", items.ItemTypes)
	}
	check(items.ItemOut{}, "HostingStatus", items.HostingStatuses)
	check(items.ItemUpdate{}, "HostingStatus", items.HostingStatuses)
}

func TestItemsRequireAdmin(t *testing.T) {
	a := newApp(t)
	id := uuid.New()
	for _, c := range []struct{ method, p string }{
		{http.MethodGet, path},
		{http.MethodPost, path},
		{http.MethodGet, itemPath(id)},
		{http.MethodPatch, itemPath(id)},
		{http.MethodDelete, itemPath(id)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{"name": "x"}), http.StatusUnauthorized, "", "not authenticated", "", "")
	}
}
