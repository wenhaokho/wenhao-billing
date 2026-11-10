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

// openTestDB is a fresh pgtestdb database with the migrations applied.
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

// TestIdMappingAcrossWrites is the Review Focus 3 case: the Python account's
// id is 1 and the Go account's is 2, so the later update request must use 1 on
// Python and 2 on Go. The row comparison then maps the id before comparing.
func TestIdMappingAcrossWrites(t *testing.T) {
	t.Parallel()
	pyDB := openTestDB(t)
	goDB := openTestDB(t)
	pyPool := openTestPool(t, pyDB)
	goPool := openTestPool(t, goDB)
	for _, pool := range []*pgxpool.Pool{pyPool, goPool} {
		if _, err := pool.Exec(context.Background(), `CREATE TABLE compare_rows (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)`); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	if _, err := pyPool.Exec(context.Background(), `INSERT INTO compare_rows (id, name, created_at) VALUES ('1', 'A', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert python row: %v", err)
	}
	if _, err := goPool.Exec(context.Background(), `INSERT INTO compare_rows (id, name, created_at) VALUES ('2', 'A', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert go row: %v", err)
	}

	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/accounting/chart-of-accounts":
			jsonBody(w, http.StatusCreated, `{"account_id":1}`)
		case "/api/v1/accounting/chart-of-accounts/1":
			jsonBody(w, http.StatusOK, `{"account_id":1,"name":"Updated"}`)
		default:
			http.NotFound(w, r)
		}
	}}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/accounting/chart-of-accounts":
			jsonBody(w, http.StatusCreated, `{"account_id":2}`)
		case "/api/v1/accounting/chart-of-accounts/2":
			jsonBody(w, http.StatusOK, `{"account_id":2,"name":"Updated"}`)
		default:
			http.NotFound(w, r)
		}
	}}
	pySrv, goSrv := py.start(t), gov.start(t)

	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID:     "account",
		Tables: []string{"compare_rows"},
		Steps: []WriteStep{
			{
				ID: "create", Method: http.MethodPost, Path: "/api/v1/accounting/chart-of-accounts",
				Body:    map[string]any{"code": "1000", "name": "Account", "type": "ASSET"},
				Extract: map[string]string{"account_id": "$.account_id"},
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
		t.Fatalf("report has %d differences, want 0: %+v", report.Differences, report.RowDiffs)
	}
	if len(report.Scenarios) != 1 || len(report.Scenarios[0].Steps) != 2 {
		t.Fatalf("scenarios %+v, want one scenario with two steps", report.Scenarios)
	}

	pyPaths := map[string]bool{}
	for _, c := range py.seen() {
		pyPaths[c.target] = true
	}
	if !pyPaths["/api/v1/accounting/chart-of-accounts/1"] {
		t.Fatalf("Python did not receive the Python id 1; paths %v", pyPaths)
	}
	goPaths := map[string]bool{}
	for _, c := range gov.seen() {
		goPaths[c.target] = true
	}
	if !goPaths["/api/v1/accounting/chart-of-accounts/2"] {
		t.Fatalf("Go did not receive the Go id 2; paths %v", goPaths)
	}
	if pyPaths["/api/v1/accounting/chart-of-accounts/2"] || goPaths["/api/v1/accounting/chart-of-accounts/1"] {
		t.Fatalf("the id mapping was not applied: Python %v, Go %v", pyPaths, goPaths)
	}
}

// TestRowsDiffMapsIds is the row comparison's id mapping: the Python row's id
// 1 and the Go row's id 2 are the same row, and a real name difference is
// reported. The two rows' timestamps differ, so they are normalised.
func TestRowsDiffMapsIds(t *testing.T) {
	t.Parallel()
	pyPool := openTestPool(t, openTestDB(t))
	goPool := openTestPool(t, openTestDB(t))
	ctx := context.Background()
	for _, pool := range []*pgxpool.Pool{pyPool, goPool} {
		if _, err := pool.Exec(ctx, `CREATE TABLE compare_rows (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)`); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	if _, err := pyPool.Exec(ctx, `INSERT INTO compare_rows (id, name, created_at) VALUES ('1', 'A', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert python row: %v", err)
	}
	if _, err := goPool.Exec(ctx, `INSERT INTO compare_rows (id, name, created_at) VALUES ('2', 'A', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert go row: %v", err)
	}

	diffs, err := Rows(ctx, pyPool, goPool, []string{"compare_rows"}, map[string]string{"1": "2"})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: the id and the timestamp are normalised", diffs)
	}

	if _, err := goPool.Exec(ctx, `UPDATE compare_rows SET name = 'B' WHERE id = '2'`); err != nil {
		t.Fatalf("update go row: %v", err)
	}
	diffs, err = Rows(ctx, pyPool, goPool, []string{"compare_rows"}, map[string]string{"1": "2"})
	if err != nil {
		t.Fatalf("CompareRows: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("diffs %+v, want the Python and Go rows", diffs)
	}
}

// TestJournalLinesAsMultiset pins the journal-lines rule: the same lines in a
// different order are equal, and a missing line is reported.
func TestJournalLinesAsMultiset(t *testing.T) {
	t.Parallel()
	pyPool := openTestPool(t, openTestDB(t))
	goPool := openTestPool(t, openTestDB(t))
	ctx := context.Background()
	for _, pool := range []*pgxpool.Pool{pyPool, goPool} {
		if _, err := pool.Exec(ctx, `CREATE TABLE compare_journal_lines (id text PRIMARY KEY, journal_entry_id text NOT NULL, account_id text NOT NULL, debit numeric(19,4) NOT NULL)`); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	if _, err := pyPool.Exec(ctx, `INSERT INTO compare_journal_lines VALUES ('1', '10', '100', '1.0000'), ('2', '10', '200', '2.0000')`); err != nil {
		t.Fatalf("insert python rows: %v", err)
	}
	if _, err := goPool.Exec(ctx, `INSERT INTO compare_journal_lines VALUES ('2', '20', '100', '1.0000'), ('1', '20', '200', '2.0000')`); err != nil {
		t.Fatalf("insert go rows: %v", err)
	}
	idMap := map[string]string{"1": "2", "2": "1", "10": "20"}

	diffs, err := Rows(ctx, pyPool, goPool, []string{"compare_journal_lines"}, idMap)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: the lines are a multiset", diffs)
	}

	if _, err := goPool.Exec(ctx, `DELETE FROM compare_journal_lines WHERE id = '1'`); err != nil {
		t.Fatalf("delete go row: %v", err)
	}
	diffs, err = Rows(ctx, pyPool, goPool, []string{"compare_journal_lines"}, idMap)
	if err != nil {
		t.Fatalf("CompareRows: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Go != "" {
		t.Fatalf("diffs %+v, want one Python-only row", diffs)
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
	// The second step must not be sent: the Python server saw login, me and
	// the first step; the Go server saw me and the first step.
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

// TestWritesRefusesRemoteDB pins the local-only guard: a remote DSN is refused
// before the database is opened, and the error never names the DSN.
func TestWritesRefusesRemoteDB(t *testing.T) {
	t.Parallel()
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	goSrv := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "remote", Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/x"}},
	}}}
	dsn := "postgresql://user:password@db.example.com:5432/billing"
	report, err := RunWrites(context.Background(), WritesOptions{
		PythonURL: py.URL, GoURL: goSrv.URL, PythonDB: dsn, GoDB: dsn,
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Scenarios: scenarios, Client: py.Client(),
		Env: func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("RunWrites succeeded, want a remote database refusal")
	}
	if !strings.Contains(err.Error(), "not local") {
		t.Fatalf("error %q, want a local-only refusal", err)
	}
	if strings.Contains(err.Error(), "password") {
		t.Fatalf("the error leaks the DSN: %v", err)
	}
	if got := WritesExitCode(report, err); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}

// TestWritesRefusesResendKey pins the mail guard: a run whose own environment
// has RESEND_API_KEY is refused before anything is sent.
func TestWritesRefusesResendKey(t *testing.T) {
	t.Parallel()
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	goSrv := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "mail", Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/x"}},
	}}}
	report, err := RunWrites(context.Background(), WritesOptions{
		PythonURL: py.URL, GoURL: goSrv.URL,
		PythonDB:    "postgresql://user:password@localhost:5432/billing",
		GoDB:        "postgresql://user:password@localhost:5432/billing",
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Scenarios: scenarios, Client: py.Client(),
		Env: func(key string) string {
			if key == "RESEND_API_KEY" {
				return "re-secret"
			}
			return ""
		},
	})
	if err == nil {
		t.Fatal("RunWrites succeeded with RESEND_API_KEY set, want a refusal")
	}
	if !strings.Contains(err.Error(), "RESEND_API_KEY") {
		t.Fatalf("error %q, want the RESEND_API_KEY refusal", err)
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
		jsonBody(w, http.StatusOK, `{"ok":true}`)
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
}

// TestWritesLocalDBHost pins the local host set.
func TestWritesLocalDBHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"db", true},
		{"db-test", true},
		{"db.example.com", false},
		{"0.0.0.0", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isLocalDBHost(tc.host); got != tc.want {
				t.Fatalf("isLocalDBHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
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
// and table validates, and a later scenario can use an earlier one's id.
func TestWritesFileLoads(t *testing.T) {
	t.Parallel()
	scenarios, err := LoadWritesFile("../../port/compare-writes.yaml")
	if err != nil {
		t.Fatalf("LoadWritesFile: %v", err)
	}
	if len(scenarios.Scenarios) == 0 {
		t.Fatal("the scenarios file has no scenarios")
	}
	if got := scenarios.Scenarios[0].ID; got != "master-data" {
		t.Fatalf("first scenario %q, want master-data", got)
	}
}

// TestWritesRejectsUnknownPlaceholder pins the placeholder validation: a path
// placeholder with no earlier extract cannot run.
func TestWritesRejectsUnknownPlaceholder(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte("scenarios:\n  - id: x\n    steps:\n      - id: s\n        method: POST\n        path: /x/{id}\n"))
	if err == nil {
		t.Fatal("ParseWrites accepted an unknown placeholder, want a refusal")
	}
	if !strings.Contains(err.Error(), "no earlier extract") {
		t.Fatalf("error %q, want an earlier-extract refusal", err)
	}
}
