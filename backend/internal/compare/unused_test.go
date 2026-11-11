package compare

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unusedKeys renders Unused as "route path" strings, for a readable failure.
func unusedKeys(a Allowed) []string {
	var out []string
	for _, u := range a.Unused() {
		out = append(out, u.Route+" "+u.Path)
	}
	return out
}

// TestUnusedListsEntriesThatNeverMatched pins the dead-entry warning: an entry
// whose route a diff reached but whose path no answer had (the PN 40
// `$.open_invoices` case) is listed; an entry that applied, an entry for a
// route never requested and a documentary rule are not.
func TestUnusedListsEntriesThatNeverMatched(t *testing.T) {
	t.Parallel()
	const route = "GET /api/v1/customers/{customer_id}/billing-summary"
	entries := []AllowedEntry{
		{Route: route, Path: "$.recurring", Rule: RuleUnordered, Note: "PN 40"},
		{Route: route, Path: "$.open_invoices", Rule: RuleUnordered, Note: "PN 40"},
		{Route: route, Path: "$.total_unpaid", Rule: RuleDecimalEqual, Note: "PN 99"},
		{Route: route, Path: "$.confidence", Rule: RuleIgnore, Note: "PN 1"},
		{Route: route, Path: "$.count", Rule: RuleNumberEqual, Note: "PN 104"},
		{Route: route, Path: statusPath, Rule: RuleStatus, Python: 500, Go: 422, Note: "PN 36"},
		{Route: "GET /api/v1/bills", Path: "$[*]", Rule: RuleUnordered, Note: "PN 78"},
	}
	a := Allowed{Entries: entries}.TrackUsage()
	py := jsonResponse(200, `{"recurring":[{"id":1},{"id":2}],"total_unpaid":"0","confidence":"97.5000","count":3}`)
	gov := jsonResponse(200, `{"recurring":[{"id":2},{"id":1}],"total_unpaid":"0.0000","confidence":"97.50","count":3}`)
	one(t, Diff("GET /api/v1/customers/c1/billing-summary", py, gov, a), 0)

	got := unusedKeys(a)
	want := []string{
		route + " $.open_invoices",
		route + " " + statusPath,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Unused = %q, want %q", got, want)
	}
}

// TestUnusedStatusAndDetailCountWhenTheyApply pins that a status pair and a
// detail-shape entry are used once the difference they allow occurs.
func TestUnusedStatusAndDetailCountWhenTheyApply(t *testing.T) {
	t.Parallel()
	const route = "POST /api/v1/payments/intake/email"
	a := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: statusPath, Rule: RuleStatus, Python: 422, Go: 401, Note: "PN 87"},
		{Route: route, Path: "$.detail", Rule: RuleDetailShape, GoCode: 401, DetailPattern: "^bad token$", Note: "PN 87"},
	}}.TrackUsage()
	one(t, Diff(route, jsonResponse(422, `{"detail":"x"}`), jsonResponse(401, `{"detail":"bad token"}`), a), 0)
	if got := unusedKeys(a); len(got) != 0 {
		t.Fatalf("Unused = %q, want none", got)
	}
}

// TestUnusedNeedsTrackUsage pins that an Allowed no run tracks lists nothing,
// and that each TrackUsage starts an empty record.
func TestUnusedNeedsTrackUsage(t *testing.T) {
	t.Parallel()
	const route = "GET /api/v1/invoices"
	base := Allowed{Entries: []AllowedEntry{{Route: route, Path: "$[*]", Rule: RuleUnordered, Note: "PN 100"}}}
	one(t, Diff(route, jsonResponse(200, `[]`), jsonResponse(200, `[]`), base), 0)
	if got := base.Unused(); len(got) != 0 {
		t.Fatalf("untracked Unused = %+v, want none", got)
	}

	first := base.TrackUsage()
	one(t, Diff(route, jsonResponse(200, `[1]`), jsonResponse(200, `[1]`), first), 0)
	if got := unusedKeys(first); len(got) != 0 {
		t.Fatalf("first run Unused = %q, want none", got)
	}
	// A second run re-tracks the first run's Allowed: it must not inherit the
	// first run's record.
	second := first.TrackUsage()
	one(t, Diff(route, jsonResponse(200, `{}`), jsonResponse(200, `{}`), second), 0)
	if got := unusedKeys(second); len(got) != 1 {
		t.Fatalf("second run Unused = %q, want the entry: its record starts empty", got)
	}
}

// TestRunnerReportsUnusedAllowed pins that a read run lists the entries it
// never needed under unused_allowed, as a warning: the exit code is unchanged.
func TestRunnerReportsUnusedAllowed(t *testing.T) {
	t.Parallel()
	answer := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/auth/login":
				loginHandler(w, r)
			case "/api/v1/auth/me":
				jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
			case "/api/v1/stats/dashboard":
				jsonBody(w, http.StatusOK, body)
			default:
				http.NotFound(w, r)
			}
		}
	}
	pySrv := (&recorder{handle: answer(`{"recent_activity":[1,2]}`)}).start(t)
	goSrv := (&recorder{handle: answer(`{"recent_activity":[2,1]}`)}).start(t)
	allowed, err := ParseAllowed([]byte(`
- route: GET /api/v1/stats/dashboard
  path: $.recent_activity
  rule: unordered
  note: PN 100
- route: GET /api/v1/stats/dashboard
  path: $.open_invoices
  rule: unordered
  note: PN 40
`))
	if err != nil {
		t.Fatalf("ParseAllowed: %v", err)
	}
	report, err := run(t, pySrv, goSrv, Corpus{Cases: []Case{{ID: "dashboard", Route: "GET /api/v1/stats/dashboard"}}}, allowed)
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	if len(report.UnusedAllowed) != 1 || report.UnusedAllowed[0].Path != "$.open_invoices" || report.UnusedAllowed[0].Note != "PN 40" {
		t.Fatalf("unused_allowed %+v, want only the $.open_invoices entry", report.UnusedAllowed)
	}
	if got := ExitCode(report, nil, false); got != 0 {
		t.Fatalf("exit code %d, want 0: an unused entry is a warning only", got)
	}
}

// TestLoadAllowedFilesMerges pins the repeatable --allowed: the files'
// entries are merged in order, and an error names the file.
func TestLoadAllowedFilesMerges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	first := write("a.yaml", "- route: GET /api/v1/bills\n  path: $[*]\n  rule: unordered\n  note: PN 78\n")
	second := write("b.yaml", "- route: GET /api/v1/projects\n  path: $[*]\n  rule: unordered\n  note: PN 78\n")
	bad := write("bad.yaml", "- route: GET /api/v1/items\n  path: $.x\n  rule: ignore\n")

	a, err := LoadAllowedFiles(first, second)
	if err != nil {
		t.Fatalf("LoadAllowedFiles: %v", err)
	}
	if len(a.Entries) != 2 || a.Entries[0].Route != "GET /api/v1/bills" || a.Entries[1].Route != "GET /api/v1/projects" {
		t.Fatalf("entries %+v, want bills then projects", a.Entries)
	}
	_, err = LoadAllowedFiles(first, bad)
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") || !strings.Contains(err.Error(), "has no note") {
		t.Fatalf("error %v, want it to name bad.yaml and the missing note", err)
	}
}

// TestWritesReportsUnusedAllowed pins the same warning on a write run.
func TestWritesReportsUnusedAllowed(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	handle := func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"name":"A"}`)
	}
	py := writeBackend(t, true, handle)
	gov := writeBackend(t, false, handle)
	allowed, err := ParseAllowed([]byte(`
- route: GET /api/v1/customers
  path: $.name
  rule: ignore
  note: PN 1
- route: GET /api/v1/customers
  path: $.missing
  rule: ignore
  note: PN 2
`))
	if err != nil {
		t.Fatalf("ParseAllowed: %v", err)
	}
	o := writeOptions(t, py, gov, pyDB, goDB, WritesScenarios{Scenarios: []WritesScenario{{
		ID: "read", Steps: []WriteStep{{ID: "list", Method: http.MethodGet, Path: "/api/v1/customers"}},
	}}})
	o.Allowed = allowed
	report, err := RunWrites(context.Background(), o)
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	if len(report.UnusedAllowed) != 1 || report.UnusedAllowed[0].Path != "$.missing" {
		t.Fatalf("unused_allowed %+v, want only $.missing", report.UnusedAllowed)
	}
}
