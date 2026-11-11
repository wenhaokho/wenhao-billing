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
// route never requested are not. A documentary rule (decimal-equal) is used
// when an answer has its path, so one naming a wrong field is listed.
func TestUnusedListsEntriesThatNeverMatched(t *testing.T) {
	t.Parallel()
	const route = "GET /api/v1/customers/{customer_id}/billing-summary"
	entries := []AllowedEntry{
		{Route: route, Path: "$.recurring", Rule: RuleUnordered, Note: "PN 40"},
		{Route: route, Path: "$.open_invoices", Rule: RuleUnordered, Note: "PN 40"},
		{Route: route, Path: "$.total_unpaid", Rule: RuleDecimalEqual, Note: "PN 99"},
		{Route: route, Path: "$.rows[*].difference", Rule: RuleDecimalEqual, Note: "PN 99"},
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
		route + " $.rows[*].difference",
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

	a, _, err := LoadAllowedFiles(first, second)
	if err != nil {
		t.Fatalf("LoadAllowedFiles: %v", err)
	}
	if len(a.Entries) != 2 || a.Entries[0].Route != "GET /api/v1/bills" || a.Entries[1].Route != "GET /api/v1/projects" {
		t.Fatalf("entries %+v, want bills then projects", a.Entries)
	}
	_, _, err = LoadAllowedFiles(first, bad)
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

// TestLoadAllowedFilesWarnsOnOverlap pins the loader's overlap warnings: a
// later file's entry on an earlier entry's route and path (or a path inside or
// above it) repeats it (same rule) or conflicts with it (another rule), and a
// literal route an earlier pattern route matches shadows that pattern. Entries
// within one file, and disjoint entries, are not warned of.
func TestLoadAllowedFilesWarnsOnOverlap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	shipped := write("shipped.yaml", `
- route: GET /api/v1/accounting/reports/trial-balance
  path: $.rows
  rule: unordered
  note: PN 100
- route: GET /api/v1/accounting/reports/trial-balance
  path: $.rows[*].debit
  rule: decimal-equal
  note: PN 99
- route: GET /api/v1/invoices/{invoice_id}
  path: $.line_items
  rule: unordered
  note: PN 44
`)
	overlay := write("overlay.yaml", `
- route: GET /api/v1/accounting/reports/trial-balance
  path: $.rows
  rule: ignore
  note: PN 166
- route: GET /api/v1/accounting/reports/trial-balance
  path: $.rows[*].debit
  rule: decimal-equal
  note: PN 99
- route: GET /api/v1/invoices/awaiting-finalization
  path: $[*]
  rule: unordered
  note: PN 53
- route: GET /api/v1/accounting/reports/trial-balance
  path: $.total_debit_base
  rule: ignore
  note: PN 166
`)
	_, warnings, err := LoadAllowedFiles(shipped, overlay)
	if err != nil {
		t.Fatalf("LoadAllowedFiles: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"$.rows (ignore, PN 166) conflicts with an earlier entry GET /api/v1/accounting/reports/trial-balance $.rows (unordered, PN 100)",
		"$.rows (ignore, PN 166) conflicts with an earlier entry GET /api/v1/accounting/reports/trial-balance $.rows[*].debit (decimal-equal, PN 99)",
		"$.rows[*].debit (decimal-equal, PN 99) repeats an earlier entry",
		"the literal route GET /api/v1/invoices/awaiting-finalization shadows the earlier pattern GET /api/v1/invoices/{invoice_id}",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "total_debit_base") {
		t.Errorf("a disjoint entry was warned of:\n%s", joined)
	}
	// Two for the overlay's $.rows, two for its $.rows[*].debit (a conflict
	// with $.rows and a repeat), one shadowed pattern: 5 in all.
	if len(warnings) != 5 {
		t.Errorf("%d warnings, want 5:\n%s", len(warnings), joined)
	}
	if _, single, err := LoadAllowedFiles(shipped); err != nil || len(single) != 0 {
		t.Errorf("one file: warnings %q, err %v; want none", single, err)
	}
}

// TestTieKeyValidation pins that tie_key belongs to an unordered entry and
// names plain fields.
func TestTieKeyValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, yaml, wantErr string
	}{
		{"on an ignore", "- route: GET /x\n  path: $.a\n  rule: ignore\n  tie_key: position\n  note: PN 1\n", "tie_key is for an unordered entry"},
		{"not a field", "- route: GET /x\n  path: $.a\n  rule: unordered\n  tie_key: a.b\n  note: PN 1\n", "is not a field name"},
		{"an empty part", "- route: GET /x\n  path: $.a\n  rule: unordered\n  tie_key: a,\n  note: PN 1\n", "is not a field name"},
		{"two fields", "- route: GET /x\n  path: $.a\n  rule: unordered\n  tie_key: issue_date, created_at\n  note: PN 1\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAllowed([]byte(tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseAllowed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestTieKeyRuns pins the tie_key comparison: equal keys in the same order
// with the elements of each run permuted pass; an element moved to another
// run, a different key order or a different length is reported; and a nested
// tied array inside an unordered one is matched by the same rule.
func TestTieKeyRuns(t *testing.T) {
	t.Parallel()
	const route = "GET /api/v1/things"
	a := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$[*]", Rule: RuleUnordered, TieKey: "k", Note: "PN 100"},
		{Route: route, Path: "$[*].sub", Rule: RuleUnordered, TieKey: "p,q", Note: "PN 44"},
	}}
	cases := []struct {
		name, py, gov string
		want          int
	}{
		{
			"runs permuted", `[{"k":1,"v":"a"},{"k":1,"v":"b"},{"k":2,"v":"c"},{"k":2,"v":"d"}]`,
			`[{"k":1,"v":"b"},{"k":1,"v":"a"},{"k":2,"v":"d"},{"k":2,"v":"c"}]`, 0,
		},
		{
			"an element in another run", `[{"k":1,"v":"a"},{"k":2,"v":"b"},{"k":2,"v":"c"}]`,
			`[{"k":1,"v":"b"},{"k":2,"v":"a"},{"k":2,"v":"c"}]`, 2,
		},
		{"another key order", `[{"k":1,"v":"a"},{"k":2,"v":"b"}]`, `[{"k":2,"v":"b"},{"k":1,"v":"a"}]`, 4},
		{"another length", `[{"k":1,"v":"a"},{"k":1,"v":"b"}]`, `[{"k":1,"v":"a"}]`, 1},
		{"a value differs inside a run", `[{"k":1,"v":"a"},{"k":1,"v":"b"}]`, `[{"k":1,"v":"b"},{"k":1,"v":"x"}]`, 1},
		{"decimal keys compare by value", `[{"k":"1.0","v":"a"},{"k":"1","v":"b"}]`, `[{"k":"1.0000","v":"b"},{"k":"1.00","v":"a"}]`, 0},
		{
			"two-field keys", `[{"k":1,"sub":[{"p":0,"q":1,"v":"a"},{"p":0,"q":1,"v":"b"},{"p":0,"q":2,"v":"c"}]}]`,
			`[{"k":1,"sub":[{"p":0,"q":1,"v":"b"},{"p":0,"q":1,"v":"a"},{"p":0,"q":2,"v":"c"}]}]`, 0,
		},
		{
			"two-field keys: the second differs in order", `[{"k":1,"sub":[{"p":0,"q":1,"v":"a"},{"p":0,"q":2,"v":"c"}]}]`,
			`[{"k":1,"sub":[{"p":0,"q":2,"v":"c"},{"p":0,"q":1,"v":"a"}]}]`, 4,
		},
		{
			"nested tied arrays inside a permuted run", `[{"k":1,"sub":[{"p":0,"q":0,"v":"a"},{"p":0,"q":0,"v":"b"}]},{"k":1,"sub":[]}]`,
			`[{"k":1,"sub":[]},{"k":1,"sub":[{"p":0,"q":0,"v":"b"},{"p":0,"q":0,"v":"a"}]}]`, 0,
		},
		{
			// Go's keys are not Python's: compared in order (k and v at [1]
			// differ, v at [0]), never paired inside Python's run.
			"another key inside a run", `[{"k":1,"v":"a"},{"k":1,"v":"b"}]`,
			`[{"k":1,"v":"b"},{"k":2,"v":"a"}]`, 3,
		},
		{
			// Only consecutive equal keys form a run: an element never pairs
			// with one of an equal key elsewhere in the list.
			"equal keys in separate runs do not pair", `[{"k":1,"v":"a"},{"k":2,"v":"b"},{"k":1,"v":"c"}]`,
			`[{"k":1,"v":"c"},{"k":2,"v":"b"},{"k":1,"v":"a"}]`, 2,
		},
		{
			"nested tied arrays in another key order are reported", `[{"k":1,"sub":[{"p":0,"q":0,"v":"a"},{"p":1,"q":0,"v":"b"}]}]`,
			`[{"k":1,"sub":[{"p":1,"q":0,"v":"b"},{"p":0,"q":0,"v":"a"}]}]`, 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			one(t, Diff(route, jsonResponse(200, tc.py), jsonResponse(200, tc.gov), a), tc.want)
		})
	}
}
