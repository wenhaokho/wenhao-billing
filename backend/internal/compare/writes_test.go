package compare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// openTestDB is a fresh pgtestdb database migrated with the real migrations,
// so every test below runs on the real schema.
func openTestDB(t *testing.T) string {
	t.Helper()
	return testkit.PostgresURL(t, migrations.FS)
}

// openTestPool opens a pool on a pgtestdb database and closes it at the end.
func openTestPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: url})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// mustExec runs one statement or fails the test.
func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// rowsFixture is two migrated databases, the real schema read from one, and
// the snapshots of the tables a test compares.
type rowsFixture struct {
	py, gov        *pgxpool.Pool
	schema         Schema
	tables         []string
	pySnap, goSnap Snapshot
}

// newRowsFixture opens two migrated databases. Rows a test inserts before
// snapshot are pre-existing (the shared dump); rows inserted after are the
// run's.
func newRowsFixture(t *testing.T, tables ...string) *rowsFixture {
	t.Helper()
	f := &rowsFixture{py: openTestPool(t, openTestDB(t)), gov: openTestPool(t, openTestDB(t)), tables: tables}
	schema, err := LoadSchema(context.Background(), f.py)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	f.schema = schema
	return f
}

func (f *rowsFixture) snapshot(t *testing.T) {
	t.Helper()
	var err error
	if f.pySnap, err = TakeSnapshot(context.Background(), f.py, f.schema, f.tables); err != nil {
		t.Fatalf("python snapshot: %v", err)
	}
	if f.goSnap, err = TakeSnapshot(context.Background(), f.gov, f.schema, f.tables); err != nil {
		t.Fatalf("go snapshot: %v", err)
	}
}

func (f *rowsFixture) compare(t *testing.T, maps IDMaps) []RowDiff {
	t.Helper()
	diffs, err := Rows(context.Background(), RowsInput{
		Python: f.py, Go: f.gov, Schema: f.schema, Tables: f.tables, Maps: maps,
		PythonSnapshot: f.pySnap, GoSnapshot: f.goSnap,
	})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	return diffs
}

// mapsOf builds an IDMaps from table, python, go triples.
func mapsOf(t *testing.T, triples ...string) IDMaps {
	t.Helper()
	m := IDMaps{}
	for i := 0; i+2 < len(triples); i += 3 {
		if err := m.Set(triples[i], triples[i+1], triples[i+2]); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	return m
}

const (
	pyCustomer = "11111111-1111-4111-8111-111111111111"
	goCustomer = "22222222-2222-4222-8222-222222222222"
	pyInvoice  = "33333333-3333-4333-8333-333333333333"
	goInvoice  = "44444444-4444-4444-8444-444444444444"
	pyPayment  = "55555555-5555-4555-8555-555555555555"
	goPayment  = "66666666-6666-4666-8666-666666666666"
)

// TestRowsMapsUUIDs is C1: a uuid column comes back from pgx as [16]byte and
// must render as the canonical lowercase uuid, so the py→go pair in the map
// hits and the same customer compares equal on the real customers table.
func TestRowsMapsUUIDs(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "customers")
	f.snapshot(t)
	mustExec(t, f.py, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Same')`, pyCustomer)
	mustExec(t, f.gov, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Same')`, goCustomer)

	if diffs := f.compare(t, mapsOf(t, "customers", pyCustomer, goCustomer)); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: the customer's uuid is mapped", diffs)
	}
	if got := valueString([16]byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x41, 0x11, 0x81, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11}); got != pyCustomer {
		t.Fatalf("valueString([16]byte) = %q, want %q", got, pyCustomer)
	}
	diffs := f.compare(t, IDMaps{})
	if len(diffs) != 2 || diffs[0].Python == "" || diffs[1].Go == "" {
		t.Fatalf("diffs %+v, want the unmapped Python row and the unmapped Go row", diffs)
	}
}

// TestRowsDerivesPairs is C2: rows whose ids no response named are paired
// after the run on the real tables, in the fixed order (payments, journal
// entries, reconciliation log, line items), and a row left unpaired is
// reported, never dropped.
func TestRowsDerivesPairs(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "customers", "invoices", "invoice_line_items", "payments",
		"reconciliation_log", "journal_entries", "journal_lines")
	f.snapshot(t)
	type side struct {
		pool                                 *pgxpool.Pool
		customer, invoice, payment, entry    string
		line, logID, jl1, jl2, extraLineItem string
	}
	sides := []side{
		{
			f.py, pyCustomer, pyInvoice, pyPayment, "a0000000-0000-4000-8000-000000000001",
			"a0000000-0000-4000-8000-000000000002", "a0000000-0000-4000-8000-000000000003",
			"a0000000-0000-4000-8000-000000000004", "a0000000-0000-4000-8000-000000000005", "",
		},
		{
			f.gov, goCustomer, goInvoice, goPayment, "b0000000-0000-4000-8000-000000000001",
			"b0000000-0000-4000-8000-000000000002", "b0000000-0000-4000-8000-000000000003",
			"b0000000-0000-4000-8000-000000000005", "b0000000-0000-4000-8000-000000000004",
			"b0000000-0000-4000-8000-000000000009",
		},
	}
	for _, s := range sides {
		mustExec(t, s.pool, `INSERT INTO customers (customer_id, name) VALUES ($1, 'C')`, s.customer)
		mustExec(t, s.pool, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status)
			VALUES ($1, $2, 'MILESTONE', 'IDR', 1000, 1000, 'OPEN')`, s.invoice, s.customer)
		mustExec(t, s.pool, `INSERT INTO invoice_line_items (line_item_id, invoice_id, position, description, quantity, unit_price, amount)
			VALUES ($1, $2, 1, 'Service', 1, 1000, 1000)`, s.line, s.invoice)
		mustExec(t, s.pool, `INSERT INTO payments (payment_id, invoice_id, customer_id, amount, currency, payer_name, payer_reference, payment_date, intake_source, status)
			VALUES ($1, $2, $3, 1000, 'IDR', 'P', 'ref', '2026-01-02', 'MANUAL', 'CLEARED')`, s.payment, s.invoice, s.customer)
		mustExec(t, s.pool, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons)
			VALUES ($1, $2, 'CLEARED', ARRAY['manual against invoice ' || $3::text])`, s.logID, s.payment, s.invoice)
		mustExec(t, s.pool, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo)
			VALUES ($1, 'PAYMENT', $2::uuid, 'Payment ' || $2::uuid::text || ' cleared against invoice ' || $3::text)`, s.entry, s.payment, s.invoice)
		mustExec(t, s.pool, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency) VALUES
			($1, $3, 1, 1000, 0, 'IDR'), ($2, $3, 2, 0, 1000, 'IDR')`, s.jl1, s.jl2, s.entry)
		if s.extraLineItem != "" {
			mustExec(t, s.pool, `INSERT INTO invoice_line_items (line_item_id, invoice_id, position, description, quantity, unit_price, amount)
				VALUES ($1, $2, 2, 'Extra', 1, 5, 5)`, s.extraLineItem, s.invoice)
		}
	}

	// Only the customer and the invoice came back in a response.
	diffs := f.compare(t, mapsOf(t, "customers", pyCustomer, goCustomer, "invoices", pyInvoice, goInvoice))
	if len(diffs) != 1 {
		t.Fatalf("diffs %+v, want only the Go-only line item", diffs)
	}
	d := diffs[0]
	if d.Table != "invoice_line_items" || d.Python != "" || !strings.Contains(d.Go, "description=Extra") {
		t.Fatalf("diff %+v, want the unpaired Go line item at position 2", d)
	}
}

// TestJournalLinesAsMultiset is C2's journal-line rule on the real
// journal_entries and journal_lines tables: lines compare as a multiset per
// mapped entry_id with line_id excluded, so the same lines in another order
// with other line ids are equal, a line moved to another entry with the same
// totals is reported, and an entry with no partner is reported.
func TestJournalLinesAsMultiset(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "journal_entries", "journal_lines")
	f.snapshot(t)
	const (
		pySrc1, goSrc1 = "c0000000-0000-4000-8000-000000000001", "d0000000-0000-4000-8000-000000000001"
		pySrc2, goSrc2 = "c0000000-0000-4000-8000-000000000002", "d0000000-0000-4000-8000-000000000002"
		pyE1, pyE2     = "c1000000-0000-4000-8000-000000000001", "c1000000-0000-4000-8000-000000000002"
		goE1, goE2     = "d1000000-0000-4000-8000-000000000001", "d1000000-0000-4000-8000-000000000002"
	)
	mustExec(t, f.py, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES
		($1, 'PAYMENT', $2, 'one'), ($3, 'PAYMENT', $4, 'two')`, pyE1, pySrc1, pyE2, pySrc2)
	mustExec(t, f.gov, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES
		($1, 'PAYMENT', $2, 'two'), ($3, 'PAYMENT', $4, 'one')`, goE2, goSrc2, goE1, goSrc1)
	mustExec(t, f.py, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency) VALUES
		('c2000000-0000-4000-8000-000000000001', $1, 1, 100, 0, 'IDR'),
		('c2000000-0000-4000-8000-000000000002', $1, 2, 0, 100, 'IDR'),
		('c2000000-0000-4000-8000-000000000003', $2, 1, 100, 0, 'IDR'),
		('c2000000-0000-4000-8000-000000000004', $2, 2, 0, 100, 'IDR')`, pyE1, pyE2)
	mustExec(t, f.gov, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency) VALUES
		('d2000000-0000-4000-8000-000000000004', $2, 2, 0, 100, 'IDR'),
		('d2000000-0000-4000-8000-000000000003', $2, 1, 100, 0, 'IDR'),
		('d2000000-0000-4000-8000-000000000002', $1, 2, 0, 100, 'IDR'),
		('d2000000-0000-4000-8000-000000000001', $1, 1, 100, 0, 'IDR')`, goE1, goE2)
	// The entries' sources are payments a response named; the entries
	// themselves never appear in a response and are derived.
	maps := func() IDMaps { return mapsOf(t, "payments", pySrc1, goSrc1, "payments", pySrc2, goSrc2) }

	if diffs := f.compare(t, maps()); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: the lines are a multiset per entry", diffs)
	}

	// Move Go's debit line from entry one to entry two: the table's totals
	// are unchanged, but each entry's lines are not.
	mustExec(t, f.gov, `UPDATE journal_lines SET entry_id = $1 WHERE line_id = 'd2000000-0000-4000-8000-000000000001'`, goE2)
	diffs := f.compare(t, maps())
	if len(diffs) != 2 {
		t.Fatalf("diffs %+v, want the line missing from entry one and the extra line on entry two", diffs)
	}
	got := map[string]string{}
	for _, d := range diffs {
		if d.Table != "journal_lines" {
			t.Fatalf("diff %+v, want a journal_lines difference", d)
		}
		if d.Python != "" {
			got["python"] = d.Key
		} else {
			got["go"] = d.Key
		}
	}
	if got["python"] != goE1 || got["go"] != goE2 {
		t.Fatalf("diff keys %v, want the Python-only line on entry %s and the Go-only line on %s", got, goE1, goE2)
	}

	// An entry with no Go partner is reported, not dropped.
	mustExec(t, f.py, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES
		('c1000000-0000-4000-8000-000000000003', 'PAYMENT', $1, 'three')`, pySrc1)
	diffs = f.compare(t, maps())
	var lonely bool
	for _, d := range diffs {
		if d.Table == "journal_entries" && d.Go == "" && strings.Contains(d.Python, "memo=three") {
			lonely = true
		}
	}
	if !lonely {
		t.Fatalf("diffs %+v, want the unpaired Python entry reported", diffs)
	}
}

// TestRowsGeneratedNumbers is I2: only the numbers the server generates are
// normalised (invoices.invoice_number in the WH<year><n> shape); a bill_number
// difference is reported.
func TestRowsGeneratedNumbers(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "vendors", "invoices", "bills")
	f.snapshot(t)
	const pyVendor, goVendor = "e0000000-0000-4000-8000-000000000001", "f0000000-0000-4000-8000-000000000001"
	const pyBill, goBill = "e0000000-0000-4000-8000-000000000002", "f0000000-0000-4000-8000-000000000002"
	for _, s := range []struct {
		pool                      *pgxpool.Pool
		vendor, bill, invoice     string
		invoiceNumber, billNumber string
	}{
		{f.py, pyVendor, pyBill, pyInvoice, "WH20260001", "B-1"},
		{f.gov, goVendor, goBill, goInvoice, "WH20260002", "B-1"},
	} {
		mustExec(t, s.pool, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'V')`, s.vendor)
		mustExec(t, s.pool, `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status, invoice_number)
			VALUES ($1, 'MILESTONE', 'IDR', 1, 1, 'DRAFT', $2)`, s.invoice, s.invoiceNumber)
		mustExec(t, s.pool, `INSERT INTO bills (bill_id, vendor_id, bill_number, currency, amount, balance_due)
			VALUES ($1, $2, $3, 'IDR', 1, 1)`, s.bill, s.vendor, s.billNumber)
	}
	maps := mapsOf(t, "vendors", pyVendor, goVendor, "invoices", pyInvoice, goInvoice, "bills", pyBill, goBill)
	if diffs := f.compare(t, maps); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: invoice_number is generated", diffs)
	}

	mustExec(t, f.gov, `UPDATE bills SET bill_number = 'B-2'`)
	diffs := f.compare(t, maps)
	if len(diffs) != 1 || diffs[0].Table != "bills" || !strings.Contains(diffs[0].Go, "bill_number=B-2") {
		t.Fatalf("diffs %+v, want the bill_number difference", diffs)
	}
}

// TestIDMapsAreKeyedByEntity is I3: an integer chart_of_accounts mapping 1→2
// must not rewrite payment_account.business_profile_id, an unrelated integer
// *_id column whose value is 1.
func TestIDMapsAreKeyedByEntity(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "payment_account")
	f.snapshot(t)
	for _, pool := range []*pgxpool.Pool{f.py, f.gov} {
		mustExec(t, pool, `INSERT INTO payment_account (business_profile_id, currency, instructions) VALUES (1, 'IDR', 'Bank')`)
	}
	if diffs := f.compare(t, mapsOf(t, "chart_of_accounts", "1", "2")); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: the account map must not touch business_profile_id", diffs)
	}
}

// TestRowsComparesOnlyTouchedRows is I5: a seeded row that differs between the
// two databases but that the run never touched is not reported; a seeded row
// the run changed, on either side, is compared.
func TestRowsComparesOnlyTouchedRows(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "customers")
	const seed, changed, oneSide = "70000000-0000-4000-8000-000000000001", "70000000-0000-4000-8000-000000000002", "70000000-0000-4000-8000-000000000003"
	mustExec(t, f.py, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Seed py'), ($2, 'X'), ($3, 'Y')`, seed, changed, oneSide)
	mustExec(t, f.gov, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Seed go'), ($2, 'X'), ($3, 'Y')`, seed, changed, oneSide)
	f.snapshot(t)
	if diffs := f.compare(t, IDMaps{}); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: nothing was touched", diffs)
	}

	mustExec(t, f.py, `UPDATE customers SET name = 'X2' WHERE customer_id = $1`, changed)
	mustExec(t, f.gov, `UPDATE customers SET name = 'X3' WHERE customer_id = $1`, changed)
	mustExec(t, f.py, `UPDATE customers SET name = 'Y2' WHERE customer_id = $1`, oneSide)
	diffs := f.compare(t, IDMaps{})
	keys := map[string]bool{}
	for _, d := range diffs {
		keys[d.Key] = true
	}
	if len(diffs) != 2 || !keys[changed] || !keys[oneSide] {
		t.Fatalf("diffs %+v, want the two changed rows and not the untouched seed", diffs)
	}
}

// TestRowsDeletedOnBothSidesAgree is review round 2: a seeded row deleted on
// both sides (PUT /business-profile/payment-accounts replaces the seeded
// rows) is agreement, not a difference; a seeded row deleted on one side only
// is reported.
func TestRowsDeletedOnBothSidesAgree(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "payment_account")
	for _, pool := range []*pgxpool.Pool{f.py, f.gov} {
		mustExec(t, pool, `INSERT INTO payment_account (id, business_profile_id, currency, instructions) VALUES (1, 1, 'IDR', 'Bank'), (2, 1, 'USD', 'Bank')`)
	}
	f.snapshot(t)
	for _, pool := range []*pgxpool.Pool{f.py, f.gov} {
		mustExec(t, pool, `DELETE FROM payment_account WHERE id = 1`)
	}
	if diffs := f.compare(t, IDMaps{}); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: both sides deleted the row", diffs)
	}

	mustExec(t, f.py, `DELETE FROM payment_account WHERE id = 2`)
	diffs := f.compare(t, IDMaps{})
	if len(diffs) != 1 || diffs[0].Key != "2" || diffs[0].Python != "" || diffs[0].Go == "" {
		t.Fatalf("diffs %+v, want the row Python deleted and Go kept", diffs)
	}
}

// accountServer is a fake backend that writes the real chart_of_accounts
// table on its own database, the way the real backends do.
func accountServer(t *testing.T, pool *pgxpool.Pool, login bool) *recorder {
	t.Helper()
	return &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		switch {
		case login && r.URL.Path == "/api/v1/auth/login":
			loginHandler(w, r)
		case r.URL.Path == "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounting/chart-of-accounts":
			var id int
			if err := pool.QueryRow(ctx, `INSERT INTO chart_of_accounts (code, name, type) VALUES ('9900', 'Account', 'EXPENSE') RETURNING account_id`).Scan(&id); err != nil {
				jsonBody(w, http.StatusInternalServerError, `{"detail":"insert"}`)
				return
			}
			jsonBody(w, http.StatusCreated, `{"account_id":`+itoa(id)+`,"code":"9900","name":"Account","type":"EXPENSE","active":true}`)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v1/accounting/chart-of-accounts/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/accounting/chart-of-accounts/")
			if _, err := pool.Exec(ctx, `UPDATE chart_of_accounts SET name = 'Updated' WHERE account_id = $1::int`, id); err != nil {
				jsonBody(w, http.StatusInternalServerError, `{"detail":"update"}`)
				return
			}
			jsonBody(w, http.StatusOK, `{"account_id":`+id+`,"code":"9900","name":"Updated","type":"EXPENSE","active":true}`)
		default:
			http.NotFound(w, r)
		}
	}}
}

func itoa(n int) string { return valueString(n) }

// TestIdMappingAcrossWrites is the Review Focus 3 case on the real
// chart_of_accounts table: the Python account's id and the Go account's id
// differ, so the later update must use each backend's own id, and the row
// comparison and the step answers map the Python id to the Go id.
func TestIdMappingAcrossWrites(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	pyPool, goPool := openTestPool(t, pyDB), openTestPool(t, goDB)
	mustExec(t, goPool, `SELECT setval('chart_of_accounts_account_id_seq', 500)`)
	py, gov := accountServer(t, pyPool, true), accountServer(t, goPool, false)
	pySrv, goSrv := py.start(t), gov.start(t)

	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID:     "account",
		Tables: []string{"chart_of_accounts"},
		Steps: []WriteStep{
			{
				ID: "create", Method: http.MethodPost, Path: "/api/v1/accounting/chart-of-accounts",
				Body: map[string]any{"code": "9900"}, Extract: map[string]string{"account_id": "$.account_id"},
			},
			{
				ID: "update", Method: http.MethodPatch, Path: "/api/v1/accounting/chart-of-accounts/{account_id}",
				Body: map[string]any{"name": "Updated"},
			},
		},
	}}}

	report, err := RunWrites(context.Background(), writeOptions(t, pySrv, goSrv, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	if report.Differences != 0 {
		t.Fatalf("report has %d differences, want 0: rows %+v, steps %+v", report.Differences, report.RowDiffs, report.Scenarios)
	}
	var pyID, goID string
	for _, c := range py.seen() {
		if c.method == http.MethodPatch {
			pyID = c.target
		}
	}
	for _, c := range gov.seen() {
		if c.method == http.MethodPatch {
			goID = c.target
		}
	}
	if pyID == goID || !strings.HasSuffix(goID, "/501") {
		t.Fatalf("Python patched %q and Go patched %q, want each backend's own id (Go 501)", pyID, goID)
	}
}

// TestWritesComparesStepAnswers is Minor 2: each step's status and body are
// compared with Diff after the ids are mapped. Python 201 vs Go 200 is
// reported; the ids and the timestamps are not.
func TestWritesComparesStepAnswers(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	answer := func(status int, id, ts, name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/auth/login":
				loginHandler(w, r)
			case r.URL.Path == "/api/v1/auth/me":
				jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
			case r.Method == http.MethodPost:
				jsonBody(w, status, `{"customer_id":"`+id+`","name":"A","created_at":"`+ts+`"}`)
			case r.URL.Path == "/api/v1/customers/"+id:
				jsonBody(w, http.StatusOK, `{"customer_id":"`+id+`","name":"`+name+`"}`)
			default:
				http.NotFound(w, r)
			}
		}
	}
	py := (&recorder{handle: answer(http.StatusCreated, pyCustomer, "2026-01-01T00:00:00", "A")}).start(t)
	gov := (&recorder{handle: answer(http.StatusOK, goCustomer, "2026-01-02T03:04:05Z", "B")}).start(t)
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "customer",
		Steps: []WriteStep{
			{
				ID: "create", Method: http.MethodPost, Path: "/api/v1/customers", Body: map[string]any{"name": "A"},
				Extract: map[string]string{"customer_id": "$.customer_id"},
			},
			{ID: "read", Method: http.MethodGet, Path: "/api/v1/customers/{customer_id}"},
		},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	steps := report.Scenarios[0].Steps
	if len(steps[0].Differences) != 1 || steps[0].Differences[0].Path != statusPath ||
		steps[0].Differences[0].Python != "201" || steps[0].Differences[0].Go != "200" {
		t.Fatalf("create differences %+v, want only the 201/200 status", steps[0].Differences)
	}
	if len(steps[1].Differences) != 1 || steps[1].Differences[0].Path != "$.name" {
		t.Fatalf("read differences %+v, want only $.name (the id is mapped)", steps[1].Differences)
	}
	if report.Differences != 2 {
		t.Fatalf("report differences %d, want 2", report.Differences)
	}
}

// writeBackend is a fake backend that answers the login and the sign-in check
// and the write routes the scenario names.
func writeBackend(t *testing.T, login bool, handle func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case login && r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			loginHandler(w, r)
		case r.URL.Path == "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		default:
			handle(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeOptions is a run against the two fake servers and the two databases.
func writeOptions(t *testing.T, py, goSrv *httptest.Server, pyDB, goDB string, scenarios WritesScenarios) WritesOptions {
	t.Helper()
	return WritesOptions{
		PythonURL: py.URL, GoURL: goSrv.URL,
		PythonDB: pyDB, GoDB: goDB,
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Scenarios: scenarios, Client: py.Client(),
		Env: func(string) string { return "" },
	}
}

// TestWritesExtractNeedsEntity pins that an extract whose key names no single
// table is a hard error: its id could not be mapped.
func TestWritesExtractNeedsEntity(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusCreated, `{"id":1}`) })
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusCreated, `{"id":2}`) })
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{ID: "x", Steps: []WriteStep{{
		ID: "create", Method: http.MethodPost, Path: "/api/v1/things", Extract: map[string]string{"thing": "$.id"},
	}}}}}
	_, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err == nil || !strings.Contains(err.Error(), "names no single table") {
		t.Fatalf("error %v, want the no-table refusal", err)
	}
}

// TestScenarioStopsAtFirstHardFailure pins the hard-failure rule: a 400 on the
// first step stops the run and the second step is never sent.
func TestScenarioStopsAtFirstHardFailure(t *testing.T) {
	t.Parallel()
	pyDB := openTestDB(t)
	goDB := openTestDB(t)
	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/things":
			jsonBody(w, http.StatusBadRequest, `{"detail":"bad"}`)
		default:
			http.NotFound(w, r)
		}
	}}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/things":
			jsonBody(w, http.StatusOK, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}}
	pySrv, goSrv := py.start(t), gov.start(t)
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "stop",
		Steps: []WriteStep{
			{ID: "first", Method: http.MethodPost, Path: "/api/v1/things"},
			{ID: "second", Method: http.MethodPost, Path: "/api/v1/things"},
		},
	}}}

	report, err := RunWrites(context.Background(), writeOptions(t, pySrv, goSrv, pyDB, goDB, scenarios))
	if err == nil {
		t.Fatal("RunWrites succeeded, want a hard failure")
	}
	if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "python status 400") {
		t.Fatalf("error %q, want the first step and the 400", err)
	}
	if got := WritesExitCode(report, err); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
	if len(report.Scenarios) != 1 || len(report.Scenarios[0].Steps) != 1 {
		t.Fatalf("report scenarios %+v, want only the first step", report.Scenarios)
	}
	pySteps, goSteps := 0, 0
	for _, c := range py.seen() {
		if c.target == "/api/v1/things" {
			pySteps++
		}
	}
	for _, c := range gov.seen() {
		if c.target == "/api/v1/things" {
			goSteps++
		}
	}
	if pySteps != 1 || goSteps != 1 {
		t.Fatalf("the second step was sent: Python %d, Go %d", pySteps, goSteps)
	}
}

// runRefused runs a one-step scenario with the given options changed and
// returns the error; the run must be refused before any database is opened.
func runRefused(t *testing.T, change func(*WritesOptions)) (WritesReport, error) {
	t.Helper()
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusOK, `{"ok":true}`) })
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusOK, `{"ok":true}`) })
	o := WritesOptions{
		PythonURL: py.URL, GoURL: gov.URL,
		PythonDB:    "postgresql://user:password@localhost:5432/billing",
		GoDB:        "postgresql://user:password@localhost:5432/billing",
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Client: py.Client(),
		Scenarios: WritesScenarios{Scenarios: []WritesScenario{{
			ID: "x", Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/x"}},
		}}},
		Env: func(string) string { return "" },
	}
	change(&o)
	return RunWrites(context.Background(), o)
}

// TestWritesRefusesRemoteDB pins the local-only guard end to end: a remote DSN
// is refused before the database is opened, and the error never names the
// DSN's password.
func TestWritesRefusesRemoteDB(t *testing.T) {
	t.Parallel()
	report, err := runRefused(t, func(o *WritesOptions) {
		o.PythonDB = "postgresql://user:password@db.example.com:5432/billing"
	})
	if err == nil || !strings.Contains(err.Error(), "not local") {
		t.Fatalf("error %v, want a local-only refusal", err)
	}
	if strings.Contains(err.Error(), "password") {
		t.Fatalf("the error leaks the DSN: %v", err)
	}
	if got := WritesExitCode(report, err); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}

// TestWritesLocalDB is I1: the DSN is parsed the way the driver parses it, and
// the final host, every fallback host and hostaddr are all checked.
func TestWritesLocalDB(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, dsn string
		ok        bool
	}{
		{"localhost url", "postgres://u:p@localhost:5432/x", true},
		{"loopback v4", "postgres://u:p@127.0.0.1/x", true},
		{"loopback v6", "postgres://u:p@[::1]/x", true},
		{"compose service", "postgres://u:p@db-test:5432/x?sslmode=disable", true},
		{"keyword local", "host=localhost user=u password=p dbname=x", true},
		{"host override", "postgres://u:p@localhost/x?host=db.example.com", false},
		{"hostaddr override", "postgres://u:p@localhost/x?hostaddr=203.0.113.5", false},
		{"ipv6 literal", "postgres://u:p@[2001:db8::1]/x", false},
		{"remote name", "postgres://u:p@db.example.com/x", false},
		{"local-looking name", "postgres://u:p@localhost.evil.example/x", false},
		{"multi host", "postgres://u:p@localhost,db.example.com/x", false},
		{"keyword remote", "host=db.example.com user=u password=p dbname=x", false},
		{"private ip", "postgres://u:p@10.0.0.1/x", false},
		{"numeric shorthand", "postgres://u:p@2130706433/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkLocalDB(tc.dsn, "python-db")
			if (err == nil) != tc.ok {
				t.Fatalf("checkLocalDB(%q) = %v, want ok=%v", tc.dsn, err, tc.ok)
			}
			if err != nil && strings.Contains(err.Error(), "u:p@") {
				t.Fatalf("the error leaks the DSN: %v", err)
			}
		})
	}
}

// TestWritesLocalURL is I4: --python and --go must be local backend URLs.
func TestWritesLocalURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url string
		ok  bool
	}{
		{"http://localhost:8000", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8000", true},
		{"http://backend:8000", true},
		{"https://billing.example.com", false},
		{"http://203.0.113.5:8000", false},
		{"ftp://localhost", false},
		{"localhost:8000", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()
			if err := checkLocalURL(tc.url, "python"); (err == nil) != tc.ok {
				t.Fatalf("checkLocalURL(%q) = %v, want ok=%v", tc.url, err, tc.ok)
			}
		})
	}
}

// TestWritesRefusesRemoteBackend is I4 end to end: a deployed backend URL is
// refused with exit code 2 before any request.
func TestWritesRefusesRemoteBackend(t *testing.T) {
	t.Parallel()
	for _, side := range []string{"python", "go"} {
		t.Run(side, func(t *testing.T) {
			t.Parallel()
			report, err := runRefused(t, func(o *WritesOptions) {
				if side == "python" {
					o.PythonURL = "https://billing.example.com"
				} else {
					o.GoURL = "https://billing.example.com"
				}
			})
			if err == nil || !strings.Contains(err.Error(), "--"+side+" host") {
				t.Fatalf("error %v, want the --%s refusal", err, side)
			}
			if got := WritesExitCode(report, err); got != 2 {
				t.Fatalf("exit code %d, want 2", got)
			}
		})
	}
}

// TestWritesRefusesResendKey pins the mail guard: a run whose own environment
// has RESEND_API_KEY is refused before anything is sent, and the error says
// only the runner's environment was read.
func TestWritesRefusesResendKey(t *testing.T) {
	t.Parallel()
	report, err := runRefused(t, func(o *WritesOptions) {
		o.Env = func(key string) string {
			if key == "RESEND_API_KEY" {
				return "re-secret"
			}
			return ""
		}
	})
	if err == nil || !strings.Contains(err.Error(), "RESEND_API_KEY is set in this runner's own environment") {
		t.Fatalf("error %v, want the runner-environment RESEND_API_KEY refusal", err)
	}
	if strings.Contains(err.Error(), "re-secret") {
		t.Fatalf("the error leaks the key: %v", err)
	}
	if got := WritesExitCode(report, err); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}

// TestWritesClassifiesByCase pins the PN 6 decision: a scenario's classification
// names the note in the report; it never hides a difference.
func TestWritesClassifiesByCase(t *testing.T) {
	t.Parallel()
	pyDB := openTestDB(t)
	goDB := openTestDB(t)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	goSrv := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":false}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "pn6-tie", Classify: "PN 6",
		Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/api/v1/things"}},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, goSrv, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	if len(report.Scenarios) != 1 || report.Scenarios[0].Classify != "PN 6" {
		t.Fatalf("scenario %+v, want the PN 6 classification", report.Scenarios)
	}
	if report.Differences != 1 {
		t.Fatalf("differences %d, want the $.ok difference still reported", report.Differences)
	}
}

// TestWritesRejectsGoOnlyTable pins the Go-only table exclusion.
func TestWritesRejectsGoOnlyTable(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte("scenarios:\n  - id: x\n    tables: [goose_db_version]\n    steps:\n      - id: s\n        method: POST\n        path: /x\n"))
	if err == nil {
		t.Fatal("ParseWrites accepted a Go-only table, want a refusal")
	}
	if !strings.Contains(err.Error(), "Go-only") {
		t.Fatalf("error %q, want a Go-only refusal", err)
	}
}

// TestWritesFileLoads pins the shipped scenarios file: every scenario, step
// and table validates (no placeholder is extracted twice or used after its
// DELETE), and approve-intake applies the intake payment to its own
// invoice-lifecycle invoice.
func TestWritesFileLoads(t *testing.T) {
	t.Parallel()
	scenarios, err := LoadWritesFile("../../port/compare-writes.yaml")
	if err != nil {
		t.Fatalf("LoadWritesFile: %v", err)
	}
	if got := scenarios.Scenarios[0].ID; got != "master-data" {
		t.Fatalf("first scenario %q, want master-data", got)
	}
	var extractedBy string
	for _, sc := range scenarios.Scenarios {
		for _, step := range sc.Steps {
			if _, ok := step.Extract["invoice_id"]; ok {
				extractedBy = step.ID
			}
			if step.ID == "approve-intake" {
				if !strings.Contains(step.BodyString(), "{intake_invoice_id}") || !strings.Contains(step.Path, "{intake_payment_id}") {
					t.Fatalf("approve-intake %+v, want {intake_payment_id} applied to {intake_invoice_id}", step)
				}
			}
		}
	}
	if extractedBy != "create-invoice" {
		t.Fatalf("{invoice_id} is extracted by %q, want create-invoice", extractedBy)
	}
}

// TestWritesRejectsUnknownPlaceholder pins the placeholder validation: a path
// placeholder with no earlier extract cannot run.
func TestWritesRejectsUnknownPlaceholder(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte("scenarios:\n  - id: x\n    steps:\n      - id: s\n        method: POST\n        path: /x/{id}\n"))
	if err == nil || !strings.Contains(err.Error(), "no earlier extract") {
		t.Fatalf("error %v, want an earlier-extract refusal", err)
	}
}

// TestWritesRejectsDoubleExtract is Minor 1: a second extract under one name
// would point every later step at another row.
func TestWritesRejectsDoubleExtract(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte(`scenarios:
  - id: x
    steps:
      - {id: a, method: POST, path: /a, extract: {invoice_id: "$.invoice_id"}}
      - {id: b, method: POST, path: /b, extract: {invoice_id: "$.invoice_id"}}
`))
	if err == nil || !strings.Contains(err.Error(), "extracted twice") {
		t.Fatalf("error %v, want the extracted-twice refusal", err)
	}
}

// TestWritesRejectsUseAfterDelete is Minor 1: a row a DELETE deactivated is
// never used by a later step, in a path or a body.
func TestWritesRejectsUseAfterDelete(t *testing.T) {
	t.Parallel()
	for name, later := range map[string]string{
		"path": `{id: c, method: PATCH, path: "/a/{customer_id}"}`,
		"body": `{id: c, method: POST, path: /b, body: {customer_id: "{customer_id}"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWrites([]byte(`scenarios:
  - id: x
    steps:
      - {id: a, method: POST, path: /a, extract: {customer_id: "$.customer_id"}}
      - {id: b, method: DELETE, path: "/a/{customer_id}"}
      - ` + later + `
`))
			if err == nil || !strings.Contains(err.Error(), "was deleted by step b") {
				t.Fatalf("error %v, want the use-after-delete refusal", err)
			}
		})
	}
}
