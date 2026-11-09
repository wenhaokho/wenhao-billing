package projects_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects"
)

const path = "/api/v1/projects"

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
	return apitest.New(t, func(api huma.API, d apitest.Deps) { projects.Register(api, d.Pool) })
}

func projectPath(id uuid.UUID) string { return path + "/" + id.String() }

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

func exec(t *testing.T, a *apitest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func insertCustomer(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Acme')`, id)
	return id
}

// body is a create body for customer with the given code, plus extra.
func body(customer uuid.UUID, code string, extra map[string]any) map[string]any {
	b := map[string]any{"customer_id": customer, "code": code, "name": "Atlas", "currency": "IDR"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func create(t *testing.T, a *apitest.App, b map[string]any) projects.ProjectOut {
	t.Helper()
	return apitest.Decode[projects.ProjectOut](t, a.Do(t, http.MethodPost, path, b), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, b map[string]any) projects.ProjectOut {
	t.Helper()
	return apitest.Decode[projects.ProjectOut](t, a.Do(t, http.MethodPatch, projectPath(id), b), http.StatusOK)
}

func countRows(t *testing.T, a *apitest.App) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateProjectNormalizesCodeAndCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	got := create(t, a, map[string]any{"customer_id": c, "code": " atlas-26 ", "name": " Atlas Project ", "currency": "usd"})
	if got.Code != "ATLAS-26" || got.Name != "Atlas Project" || got.Currency != "USD" || got.Status != "ACTIVE" ||
		!got.Active || got.CustomerID != c || got.ProjectID == uuid.Nil || got.ContractValue != nil ||
		got.StartDate != nil || got.EndDate != nil || got.Notes != nil {
		t.Errorf("%+v", got)
	}
	back := apitest.Decode[projects.ProjectOut](t, a.Do(t, http.MethodGet, projectPath(got.ProjectID), nil), http.StatusOK)
	if back.Code != "ATLAS-26" || back.ProjectID != got.ProjectID {
		t.Errorf("GET %+v", back)
	}
	// Every field is present in the body, nulls included.
	rec := a.Do(t, http.MethodPost, path, body(c, "keys-1", map[string]any{
		"contract_value": "1500.5", "status": "ON_HOLD", "start_date": "2026-02-01", "end_date": "2026-03-01", "notes": "n",
	}))
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("%d %v: %s", rec.Code, err, rec.Body)
	}
	for _, k := range []string{"project_id", "customer_id", "code", "name", "currency", "contract_value", "status", "start_date", "end_date", "notes", "active", "created_at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %s: %s", k, rec.Body)
		}
	}
	var out struct {
		ContractValue string `json:"contract_value"`
		Status        string `json:"status"`
		StartDate     string `json:"start_date"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ContractValue != "1500.5000" || out.Status != "ON_HOLD" || out.StartDate != "2026-02-01" {
		t.Errorf("%+v", out)
	}
}

func TestCreateProjectDuplicateCodeRaisesAndLeavesOneRow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	create(t, a, body(c, "DUP-1", nil))
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "DUP-1", nil)), http.StatusBadRequest,
		"project_code_exists", "project code 'DUP-1' already exists", "", "")
	// The code is compared as normalised.
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, " dup-1 ", nil)), http.StatusBadRequest,
		"project_code_exists", "project code 'DUP-1' already exists", "", "")
	if n := countRows(t, a); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
	// Python's repr: a code with a single quote is shown in double quotes.
	create(t, a, body(c, "o'x", nil))
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "O'X", nil)), http.StatusBadRequest,
		"project_code_exists", `project code "O'X" already exists`, "", "")
}

func TestUpdateProjectEditsAndNormalizes(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "atlas-26", map[string]any{"contract_value": "100", "notes": "keep", "start_date": "2026-02-01", "end_date": "2026-03-01"}))
	got := patch(t, a, p.ProjectID, map[string]any{"code": " atlas-27 ", "currency": "sgd", "status": "ON_HOLD", "name": " Renamed ", "active": false})
	if got.Code != "ATLAS-27" || got.Currency != "SGD" || got.Status != "ON_HOLD" || got.Active ||
		got.Name != " Renamed " || // PATCH does not trim the name (Python)
		got.ContractValue == nil || got.Notes == nil || *got.Notes != "keep" || got.StartDate == nil || got.EndDate == nil || got.CustomerID != c {
		t.Errorf("%+v", got)
	}
	// An explicit null clears a nullable column; omitted keeps the rest.
	got = patch(t, a, p.ProjectID, map[string]any{"contract_value": nil, "notes": nil, "end_date": nil})
	if got.ContractValue != nil || got.Notes != nil || got.EndDate != nil || got.StartDate == nil || got.Code != "ATLAS-27" || got.Status != "ON_HOLD" {
		t.Errorf("%+v", got)
	}
	// The same code on the same row is no duplicate.
	patch(t, a, p.ProjectID, map[string]any{"code": "atlas-27"})
}

func TestUpdateProjectNotFound(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(id), map[string]any{"name": "x"}), http.StatusNotFound, "project_not_found", "project not found", "", "")
	wantProblem(t, a.Do(t, http.MethodGet, projectPath(id), nil), http.StatusNotFound, "project_not_found", "project not found", "", "")
	wantProblem(t, a.Do(t, http.MethodDelete, projectPath(id), nil), http.StatusNotFound, "project_not_found", "project not found", "", "")
}

func TestPatchDuplicateCodeIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	create(t, a, body(c, "TAKEN", nil))
	p := create(t, a, body(c, "OTHER", nil))
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"code": "taken"}), http.StatusBadRequest,
		"project_code_exists", "project code already exists", "", "")
	if got := apitest.Decode[projects.ProjectOut](t, a.Do(t, http.MethodGet, projectPath(p.ProjectID), nil), http.StatusOK); got.Code != "OTHER" {
		t.Errorf("code %q after the refused PATCH", got.Code)
	}
}

func TestCreateUnknownCustomerIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPost, path, body(uuid.New(), "NOCUST", nil)), http.StatusUnprocessableEntity,
		"", "customer not found", "customer_id", "customer not found")
	if n := countRows(t, a); n != 0 {
		t.Errorf("%d rows, want 0", n)
	}
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "HASCUST", nil))
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"customer_id": uuid.New()}), http.StatusUnprocessableEntity,
		"", "customer not found", "customer_id", "customer not found")
}

func TestPatchNullForNotNullColumnIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "NULLS", nil))
	for _, f := range []string{"name", "code", "currency", "status", "active", "customer_id"} {
		wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{f: nil}), http.StatusUnprocessableEntity,
			"", "Request validation failed", f, "value cannot be null")
	}
}

func TestPatchNullNameIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "NULLNAME", nil))
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"name": nil}), http.StatusUnprocessableEntity,
		"", "Request validation failed", "name", "value cannot be null")
	if got := apitest.Decode[projects.ProjectOut](t, a.Do(t, http.MethodGet, projectPath(p.ProjectID), nil), http.StatusOK); got.Name != "Atlas" {
		t.Errorf("name %q after the refused PATCH", got.Name)
	}
}

func TestBlankCodeIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	const blank = "String should have at least 1 character"
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "   ", nil)), http.StatusUnprocessableEntity, "", blank, "code", blank)
	if n := countRows(t, a); n != 0 {
		t.Errorf("%d rows, want 0", n)
	}
	p := create(t, a, body(c, "REAL", nil))
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"code": "  "}), http.StatusUnprocessableEntity, "", blank, "code", blank)
	// The column's own bounds still hold for the value as sent.
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "", nil)), http.StatusUnprocessableEntity, "", "Request validation failed", "", "")
}

// An upper-cased value can be longer than the one sent ("ß" is "SS"): the
// check is on the normalised value, where Python stored it or answered 500.
func TestNormalisedCodeAndCurrencyOverflowIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	const code = "String should have at most 40 characters"
	const cur = "String should have at most 3 characters"
	long := strings.Repeat("ß", 21) // 21 characters sent, 42 stored
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, long, nil)), http.StatusUnprocessableEntity, "", code, "code", code)
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "OK", map[string]any{"currency": "ßßß"})), http.StatusUnprocessableEntity, "", cur, "currency", cur)
	p := create(t, a, body(c, "FITS", nil))
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"code": long}), http.StatusUnprocessableEntity, "", code, "code", code)
	wantProblem(t, a.Do(t, http.MethodPatch, projectPath(p.ProjectID), map[string]any{"currency": "ßßß"}), http.StatusUnprocessableEntity, "", cur, "currency", cur)
	if n := countRows(t, a); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestCreateRefusesBadValues(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	const neg = "Input should be greater than or equal to 0"
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "NEG", map[string]any{"contract_value": "-1"})), http.StatusUnprocessableEntity,
		"", neg, "contract_value", neg)
	if rec := a.Do(t, http.MethodPost, path, body(c, "STAT", map[string]any{"status": "DONE"})); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, "NULLSTAT", map[string]any{"status": nil})), http.StatusUnprocessableEntity,
		"", "Request validation failed", "status", "value cannot be null")
}

func TestListFiltersAndOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c1, c2 := insertCustomer(t, a), insertCustomer(t, a)
	old := create(t, a, body(c1, "OLD", nil))
	tieA := create(t, a, body(c1, "TIE-A", map[string]any{"status": "ON_HOLD"}))
	tieB := create(t, a, body(c2, "TIE-B", map[string]any{"status": "COMPLETED"}))
	newest := create(t, a, body(c2, "NEWEST", nil))
	patch(t, a, newest.ProjectID, map[string]any{"active": false})
	set := func(id uuid.UUID, ts string) {
		t.Helper()
		exec(t, a, `UPDATE projects SET created_at = $2::timestamp WHERE project_id = $1`, id, ts)
	}
	set(old.ProjectID, "2026-01-01 00:00:00")
	set(tieA.ProjectID, "2026-01-02 00:00:00")
	set(tieB.ProjectID, "2026-01-02 00:00:00")
	set(newest.ProjectID, "2026-01-03 00:00:00")

	// The id breaks the tie, larger first.
	tieFirst, tieSecond := "TIE-A", "TIE-B"
	if tieB.ProjectID.String() > tieA.ProjectID.String() {
		tieFirst, tieSecond = "TIE-B", "TIE-A"
	}
	want := func(q string, w ...string) {
		t.Helper()
		var got []string
		for _, p := range apitest.Decode[[]projects.ProjectOut](t, a.Do(t, http.MethodGet, path+q, nil), http.StatusOK) {
			got = append(got, p.Code)
		}
		if strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("%q: %v, want %v", q, got, w)
		}
	}
	want("", "NEWEST", tieFirst, tieSecond, "OLD")
	want("?status=ACTIVE", "NEWEST", "OLD")
	want("?status=ON_HOLD&status=COMPLETED", tieFirst, tieSecond)
	want("?customer_id="+c1.String(), "TIE-A", "OLD")
	want("?customer_id="+c2.String()+"&status=ACTIVE", "NEWEST")
	want("?active=false", "NEWEST")
	want("?active=true", tieFirst, tieSecond, "OLD")
	want("?status=NOPE")

	// Bare arrays, never null.
	if rec := a.Do(t, http.MethodGet, path+"?status=NOPE", nil); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty list %q", rec.Body)
	}
}

func TestDeleteUnreferencedProject204(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "GONE", nil))
	if rec := a.Do(t, http.MethodDelete, projectPath(p.ProjectID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	wantProblem(t, a.Do(t, http.MethodGet, projectPath(p.ProjectID), nil), http.StatusNotFound, "", "project not found", "", "")
}

func insertInvoice(t *testing.T, a *apitest.App, customer, project uuid.UUID, status string, template bool) {
	t.Helper()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, project_id, invoice_type, currency, amount, balance_due, status, is_template)
VALUES ($1, $2, $3, 'STANDARD', 'IDR', 0, 0, $4, $5)`, uuid.New(), customer, project, status, template)
}

func insertBill(t *testing.T, a *apitest.App, project uuid.UUID, status string) {
	t.Helper()
	vendor := uuid.New()
	exec(t, a, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'Vend')`, vendor)
	exec(t, a, `INSERT INTO bills (bill_id, vendor_id, project_id, currency, amount, balance_due, status)
VALUES ($1, $2, $3, 'IDR', 0, 0, $4)`, uuid.New(), vendor, project, status)
}

func insertQuotation(t *testing.T, a *apitest.App, customer, project uuid.UUID) {
	t.Helper()
	exec(t, a, `INSERT INTO quotations (quotation_id, customer_id, project_id, currency, amount) VALUES ($1, $2, $3, 'IDR', 0)`,
		uuid.New(), customer, project)
}

func TestDeleteProjectReferencedByInvoicesAndBills(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "BUSY", nil))
	// Any status counts: a void invoice, a template and a void bill too.
	insertInvoice(t, a, c, p.ProjectID, "VOID", false)
	insertInvoice(t, a, c, p.ProjectID, "DRAFT", true)
	insertBill(t, a, p.ProjectID, "VOID")
	wantProblem(t, a.Do(t, http.MethodDelete, projectPath(p.ProjectID), nil), http.StatusBadRequest,
		"project_referenced", "cannot delete: project is referenced by 3 invoice(s)/bill(s)", "", "")
	if n := countRows(t, a); n != 1 {
		t.Errorf("%d rows, want the project kept", n)
	}
}

func TestDeleteProjectReferencedByQuotationIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "QUOTED", nil))
	insertQuotation(t, a, c, p.ProjectID)
	wantProblem(t, a.Do(t, http.MethodDelete, projectPath(p.ProjectID), nil), http.StatusBadRequest,
		"project_referenced", "cannot delete: project is referenced by 1 invoice(s)/bill(s)", "", "")
	if n := countRows(t, a); n != 1 {
		t.Errorf("%d rows, want the project kept", n)
	}
}

func TestProjectRoutesRequireAdmin(t *testing.T) {
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
	id := uuid.New()
	for _, c := range []struct{ method, p string }{
		{http.MethodPost, path},
		{http.MethodGet, path},
		{http.MethodGet, projectPath(id)},
		{http.MethodPatch, projectPath(id)},
		{http.MethodDelete, projectPath(id)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{}), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, c.method, c.p, map[string]any{}), http.StatusForbidden, "", "admin required", "", "")
	}
}
