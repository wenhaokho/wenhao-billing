package compare

import (
	"net/http"
	"testing"
	"time"
)

// jsonResponse is a JSON Response.
func jsonResponse(status int, b string) Response {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return Response{Status: status, Header: h, Body: []byte(b)}
}

// one fails t unless diffs has exactly n differences.
func one(t *testing.T, diffs []Difference, n int) {
	t.Helper()
	if len(diffs) != n {
		t.Fatalf("got %d differences, want %d: %+v", len(diffs), n, diffs)
	}
}

func TestDecimalStringsEqualByValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		py, gov    string
		wantDiffer bool
	}{
		{"four places against none", `{"amount":"1500.0000"}`, `{"amount":"1500"}`, false},
		{"negative zero against zero", `{"amount":"-0.0000"}`, `{"amount":"0"}`, false},
		{"zero against four places", `{"amount":"0"}`, `{"amount":"0.0000"}`, false},
		{"two places against one", `{"amount":"10.00"}`, `{"amount":"10.0"}`, false},
		{"a real difference", `{"amount":"1500.0001"}`, `{"amount":"1500"}`, true},
		{"a whole value difference", `{"amount":"1500.0000"}`, `{"amount":"1501"}`, true},
		{"a leading-zero id is not a decimal", `{"code":"0012"}`, `{"code":"12"}`, true},
		{"a phone number is not a decimal", `{"phone":"08123"}`, `{"phone":"8123"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diffs := Diff("GET /api/v1/invoices/1", jsonResponse(200, tc.py), jsonResponse(200, tc.gov), Allowed{})
			if tc.wantDiffer {
				one(t, diffs, 1)
				if diffs[0].Path != "$.amount" && diffs[0].Path != "$.code" && diffs[0].Path != "$.phone" {
					t.Fatalf("difference %+v, want a value path", diffs[0])
				}
				return
			}
			one(t, diffs, 0)
		})
	}
}

func TestInstantsEqualAcrossFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		py, gov    string
		tz         *time.Location
		wantDiffer bool
	}{
		{"naive against Z", `{"at":"2026-01-01T00:00:00"}`, `{"at":"2026-01-01T00:00:00Z"}`, nil, false},
		{"Z against a positive offset", `{"at":"2026-01-01T00:00:00Z"}`, `{"at":"2026-01-01T07:00:00+07:00"}`, nil, false},
		{"naive with microseconds against Z", `{"at":"2026-01-01T09:00:00.000000"}`, `{"at":"2026-01-01T09:00:00Z"}`, nil, false},
		{"different instants", `{"at":"2026-01-01T00:00:00"}`, `{"at":"2026-01-01T00:00:01Z"}`, nil, true},
		{
			"naive in the python zone", `{"at":"2026-01-01T07:00:00"}`, `{"at":"2026-01-01T00:00:00Z"}`,
			time.FixedZone("WIB", 7*3600), false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := Allowed{PythonTZ: tc.tz}
			diffs := Diff("GET /api/v1/invoices/1", jsonResponse(200, tc.py), jsonResponse(200, tc.gov), a)
			if tc.wantDiffer {
				one(t, diffs, 1)
				if diffs[0].Rule != RuleInstantEqual {
					t.Fatalf("difference %+v, want rule %s", diffs[0], RuleInstantEqual)
				}
				return
			}
			one(t, diffs, 0)
		})
	}
}

func TestDateOnlyIsNotAnInstant(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/accounting/reports/trial-balance"
	one(t, Diff(route, jsonResponse(200, `{"as_of":"2026-01-01"}`), jsonResponse(200, `{"as_of":"2026-01-01T00:00:00Z"}`), Allowed{}), 1)

	jakarta := Allowed{PythonTZ: time.FixedZone("WIB", 7*3600)}
	one(t, Diff(route, jsonResponse(200, `{"as_of":"2026-01-02"}`), jsonResponse(200, `{"as_of":"2026-01-01T17:00:00Z"}`), jakarta), 1)

	// The same date-only value is equal.
	one(t, Diff(route, jsonResponse(200, `{"as_of":"2026-01-01"}`), jsonResponse(200, `{"as_of":"2026-01-01"}`), Allowed{}), 0)
}

func TestErrorShapeEqual(t *testing.T) {
	t.Parallel()
	py := jsonResponse(400, `{"detail":"not found"}`)
	gov := jsonResponse(400, `{"title":"Not Found","status":400,"detail":"not found"}`)
	if diffs := Diff("GET /api/v1/invoices/1", py, gov, Allowed{}); len(diffs) != 0 {
		t.Fatalf("equal error shapes reported %+v", diffs)
	}

	different := jsonResponse(400, `{"title":"Not Found","status":400,"detail":"gone"}`)
	diffs := Diff("GET /api/v1/invoices/1", py, different, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.detail" || diffs[0].Rule != RuleDetailShape {
		t.Fatalf("difference %+v, want path $.detail rule %s", diffs[0], RuleDetailShape)
	}
}

func TestOAuthErrorBodiesCompared(t *testing.T) {
	t.Parallel()
	route := "POST /oauth/token"
	py := jsonResponse(400, `{"error":"invalid_grant"}`)
	gov := jsonResponse(400, `{"error":"invalid_client"}`)
	diffs := Diff(route, py, gov, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.error" {
		t.Fatalf("difference %+v, want $.error", diffs[0])
	}

	// The error and error_description fields are compared whenever present.
	pyFull := jsonResponse(400, `{"error":"invalid_grant","error_description":"the code is spent"}`)
	govFull := jsonResponse(400, `{"error":"invalid_client","error_description":"unknown client"}`)
	one(t, Diff(route, pyFull, govFull, Allowed{}), 2)

	// An exact entry allows one of them.
	allowed := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.error", Rule: RuleDetailShape, GoCode: 400, DetailPattern: `^invalid_client$`, Note: "PN 150"},
	}}
	if diffs := Diff(route, py, gov, allowed); len(diffs) != 0 {
		t.Fatalf("allowed error difference reported %+v", diffs)
	}

	// A pattern that does not match does not allow it.
	notMatching := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.error", Rule: RuleDetailShape, GoCode: 400, DetailPattern: `^other$`, Note: "PN 150"},
	}}
	one(t, Diff(route, py, gov, notMatching), 1)

	// A Go status that does not match does not allow it.
	wrongCode := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.error", Rule: RuleDetailShape, GoCode: 422, DetailPattern: `^invalid_client$`, Note: "PN 150"},
	}}
	one(t, Diff(route, py, gov, wrongCode), 1)
}

func TestShippedEntriesDoNotOverreach(t *testing.T) {
	t.Parallel()
	a, err := LoadAllowedFile("../../port/allowed-diffs.yaml")
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}
	cases := []struct {
		name       string
		route      string
		py, gov    Response
		wantDiffer int
	}{
		{
			name:       "the balance sheet compares its balances",
			route:      "GET /api/v1/accounting/reports/balance-sheet",
			py:         jsonResponse(200, `{"groups":[{"lines":[{"base_balance":"100.0000"}]}]}`),
			gov:        jsonResponse(200, `{"groups":[{"lines":[{"base_balance":"999.0000"}]}]}`),
			wantDiffer: 1,
		},
		{
			name:       "the dashboard compares its open AR",
			route:      "GET /api/v1/stats/dashboard",
			py:         jsonResponse(200, `{"open_ar_by_currency":[{"currency":"IDR","amount":"100.0000"}]}`),
			gov:        jsonResponse(200, `{"open_ar_by_currency":[{"currency":"IDR","amount":"999.0000"}]}`),
			wantDiffer: 1,
		},
		{
			name:       "the webhook HTTP status is reported",
			route:      "POST /api/v1/payments/intake/webhook",
			py:         jsonResponse(201, `{}`),
			gov:        jsonResponse(200, `{}`),
			wantDiffer: 1,
		},
		{
			name:       "an OAuth error body is reported",
			route:      "POST /oauth/token",
			py:         jsonResponse(400, `{"error":"invalid_grant"}`),
			gov:        jsonResponse(400, `{"error":"invalid_client"}`),
			wantDiffer: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			one(t, Diff(tc.route, tc.py, tc.gov, a), tc.wantDiffer)
		})
	}
}

// hasEntry reports whether the entries hold one with this route, path and rule.
func hasEntry(a Allowed, route, path, rule string) bool {
	for _, e := range a.Entries {
		if e.Route == route && e.Path == path && e.Rule == rule {
			return true
		}
	}
	return false
}

// hasStatus reports whether a status entry names this exact pair on route.
func hasStatus(a Allowed, route string, py, gov int) bool {
	for _, e := range a.Entries {
		if e.Rule == RuleStatus && e.Route == route && e.Python == py && e.Go == gov {
			return true
		}
	}
	return false
}

func TestShippedYAMLContent(t *testing.T) {
	t.Parallel()
	a, err := LoadAllowedFile("../../port/allowed-diffs.yaml")
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}

	// The status pairs name the Python code the note records.
	if !hasStatus(a, "POST /api/v1/customers", 201, 422) {
		t.Error("PN 25: POST /customers should be python 201, go 422")
	}
	if hasEntry(a, "POST /api/v1/accounting/fx-rates", statusPath, RuleStatus) {
		t.Error("PN 30: the POST /fx-rates status entry should be deleted")
	}
	if !hasStatus(a, "POST /oauth/authorize/consent", 302, 403) {
		t.Error("PN 148: the consent status entry should be python 302, go 403")
	}

	// A detail-shape entry whose difference is never at a JSON field is gone:
	// Python's 500 is plain text (the whole body reports at $), and Python
	// succeeds on the group-2 routes.
	never := map[string]bool{
		"PN 11": true, "PN 25": true, "PN 32": true, "PN 34": true,
		"PN 35": true, "PN 36": true, "PN 38": true, "PN 56": true,
		"PN 57": true, "PN 58": true, "PN 62": true, "PN 86": true, "PN 112": true,
	}
	for _, e := range a.Entries {
		if e.Rule == RuleDetailShape && never[e.Note] {
			t.Errorf("detail-shape entry %+v can never apply; delete it", e)
		}
	}
	if hasEntry(a, "PATCH /api/v1/customers/{customer_id}", "$.detail", RuleDetailShape) {
		t.Error("PN 10 customers: Python 500 is plain text; the detail-shape can never apply")
	}
}

func TestErrorCommonKeysCompared(t *testing.T) {
	t.Parallel()
	py := jsonResponse(400, `{"code":"a","detail":"x"}`)
	gov := jsonResponse(400, `{"code":"b","detail":"x"}`)
	diffs := Diff("POST /api/v1/invoices", py, gov, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.code" {
		t.Fatalf("difference %+v, want $.code", diffs[0])
	}
}

func TestErrorNoNamedFieldsFallsBackToRawText(t *testing.T) {
	t.Parallel()
	py := jsonResponse(400, `{"foo":1}`)
	gov := jsonResponse(400, `{"bar":2}`)
	diffs := Diff("POST /api/v1/invoices", py, gov, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$" {
		t.Fatalf("difference %+v, want $", diffs[0])
	}

	// A key both sides carry is compared, so equal bodies report nothing.
	one(t, Diff("POST /api/v1/invoices", jsonResponse(400, `{"foo":1}`), jsonResponse(400, `{"foo":1}`), Allowed{}), 0)
}

func TestErrorStatusDifference(t *testing.T) {
	t.Parallel()
	py := jsonResponse(400, `{"detail":"bad input"}`)
	gov := jsonResponse(422, `{"detail":"bad input"}`)
	diffs := Diff("POST /api/v1/customers", py, gov, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "@status" || diffs[0].Rule != RuleStatus {
		t.Fatalf("difference %+v, want path @status rule %s", diffs[0], RuleStatus)
	}

	allowed := Allowed{Entries: []AllowedEntry{
		{Route: "POST /api/v1/customers", Path: statusPath, Rule: RuleStatus, Python: 400, Go: 422, Note: "PN 16"},
	}}
	if diffs := Diff("POST /api/v1/customers", py, gov, allowed); len(diffs) != 0 {
		t.Fatalf("allowed status difference reported %+v", diffs)
	}
}

func TestHTTPStatusAtPseudoPath(t *testing.T) {
	t.Parallel()
	route := "POST /api/v1/payments/intake/webhook"
	diffs := Diff(route, jsonResponse(201, `{}`), jsonResponse(200, `{}`), Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "@status" {
		t.Fatalf("difference %+v, want @status", diffs[0])
	}

	// An entry at $.status (a body field) never covers the HTTP status.
	bodyField := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.status", Rule: RuleIgnore, Note: "PN 6"},
	}}
	one(t, Diff(route, jsonResponse(201, `{}`), jsonResponse(200, `{}`), bodyField), 1)
}

func TestClearedAgainstPendingManualReview(t *testing.T) {
	t.Parallel()
	route := "POST /api/v1/payments/intake/webhook"
	py := jsonResponse(201, `{"status":"CLEARED","invoice_id":"A"}`)
	gov := jsonResponse(201, `{"status":"PENDING_MANUAL_REVIEW","invoice_id":"B"}`)
	one(t, Diff(route, py, gov, Allowed{}), 2)
}

func TestStatusEntriesAreExact(t *testing.T) {
	t.Parallel()
	route := "POST /api/v1/invoices"
	py := jsonResponse(404, `{"detail":"customer not found"}`)
	gov := jsonResponse(400, `{"detail":"something else"}`)
	one(t, Diff(route, py, gov, Allowed{}), 2)

	// A status entry for another pair does not cover this one.
	other := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: statusPath, Rule: RuleStatus, Python: 500, Go: 422, Note: "PN 36"},
	}}
	one(t, Diff(route, py, gov, other), 2)

	// The exact pair allows the status but not the detail.
	exact := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: statusPath, Rule: RuleStatus, Python: 404, Go: 400, Note: "PN 36"},
	}}
	diffs := Diff(route, py, gov, exact)
	one(t, diffs, 1)
	if diffs[0].Path != "$.detail" {
		t.Fatalf("difference %+v, want $.detail", diffs[0])
	}
}

func TestUnorderedOnlyWhereAllowed(t *testing.T) {
	t.Parallel()
	py := jsonResponse(200, `{"items":[{"id":1},{"id":2}]}`)
	gov := jsonResponse(200, `{"items":[{"id":2},{"id":1}]}`)
	route := "GET /api/v1/projects"

	// In order everywhere else.
	one(t, Diff(route, py, gov, Allowed{}), 2)

	// Only on the route and path listed for it.
	allowed := Allowed{Entries: []AllowedEntry{
		{Route: "GET /api/v1/projects", Path: "$.items", Rule: RuleUnordered, Note: "PN 78"},
	}}
	one(t, Diff(route, py, gov, allowed), 0)

	otherRoute := Allowed{Entries: []AllowedEntry{
		{Route: "GET /api/v1/bills", Path: "$.items", Rule: RuleUnordered, Note: "PN 78"},
	}}
	one(t, Diff(route, py, gov, otherRoute), 2)

	otherPath := Allowed{Entries: []AllowedEntry{
		{Route: "GET /api/v1/projects", Path: "$.lines", Rule: RuleUnordered, Note: "PN 78"},
	}}
	one(t, Diff(route, py, gov, otherPath), 2)

	// A multiset entry accepts a repeated element but reports the one that has
	// no partner.
	dup := jsonResponse(200, `{"items":[{"id":1},{"id":1}]}`)
	one(t, Diff(route, py, dup, allowed), 1)
}

func TestNumberEqualOnlyWhereAllowed(t *testing.T) {
	t.Parallel()
	py := jsonResponse(200, `{"avg_days_to_pay":10}`)
	gov := jsonResponse(200, `{"avg_days_to_pay":10.0}`)
	route := "GET /api/v1/stats/invoices-summary"
	one(t, Diff(route, py, gov, Allowed{}), 1)

	allowed := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.avg_days_to_pay", Rule: RuleNumberEqual, Note: "PN 104"},
	}}
	one(t, Diff(route, py, gov, allowed), 0)
}

func TestIgnoreDropsSubtree(t *testing.T) {
	t.Parallel()
	py := jsonResponse(200, `{"profile":{"updated_at":"2026-01-01T00:00:00","name":"a"}}`)
	gov := jsonResponse(200, `{"profile":{"updated_at":"2026-02-02T00:00:00","name":"a"}}`)
	allowed := Allowed{Entries: []AllowedEntry{
		{Route: "PUT /api/v1/business-profile", Path: "$.profile.updated_at", Rule: RuleIgnore, Note: "PN 24"},
	}}
	if diffs := Diff("PUT /api/v1/business-profile", py, gov, allowed); len(diffs) != 0 {
		t.Fatalf("ignored field reported %+v", diffs)
	}
	one(t, Diff("PUT /api/v1/business-profile", py, gov, Allowed{}), 1)
}

func TestNullVsMissingIsDifference(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/customers/1"

	diffs := Diff(route, jsonResponse(200, `{"notes":null}`), jsonResponse(200, `{}`), Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.notes" || diffs[0].Python != "null" || diffs[0].Go != missing {
		t.Fatalf("difference %+v, want $.notes null against %s", diffs[0], missing)
	}

	diffs = Diff(route, jsonResponse(200, `{}`), jsonResponse(200, `{"notes":null}`), Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.notes" || diffs[0].Python != missing || diffs[0].Go != "null" {
		t.Fatalf("difference %+v, want $.notes %s against null", diffs[0], missing)
	}

	one(t, Diff(route, jsonResponse(200, `{"notes":null}`), jsonResponse(200, `{"notes":null}`), Allowed{}), 0)
}

func TestEmptyBodyIsNotNull(t *testing.T) {
	t.Parallel()
	route := "POST /api/v1/auth/logout"
	empty := Response{Status: 200, Header: http.Header{}}
	null := jsonResponse(200, `null`)

	// An empty body is not a JSON null.
	diffs := Diff(route, empty, null, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Python != emptyText || diffs[0].Go != "null" {
		t.Fatalf("difference %+v, want %s against null", diffs[0], emptyText)
	}

	one(t, Diff(route, empty, empty, Allowed{}), 0)
	one(t, Diff(route, null, null, Allowed{}), 0)
}

func TestTrailingDataReported(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/invoices"
	one(t, Diff(route, jsonResponse(200, `{"a":1}`), jsonResponse(200, `{"a":1}{"b":2}`), Allowed{}), 1)
	one(t, Diff(route, jsonResponse(200, `{"a":1}`), jsonResponse(200, `{"a":1}`), Allowed{}), 0)
}

func TestIdenticalNonJSONBodiesEqual(t *testing.T) {
	t.Parallel()
	csv := func(status int, ct, b string) Response {
		h := http.Header{}
		h.Set("Content-Type", ct)
		return Response{Status: status, Header: h, Body: []byte(b)}
	}
	route := "GET /api/v1/accounting/reports/trial-balance"
	one(t, Diff(route, csv(200, "text/csv", "a,b\n1,2\n"), csv(200, "text/csv", "a,b\n1,2\n"), Allowed{}), 0)
}

func TestNonJSONMediaTypeCompared(t *testing.T) {
	t.Parallel()
	csv := func(status int, ct, b string) Response {
		h := http.Header{}
		h.Set("Content-Type", ct)
		return Response{Status: status, Header: h, Body: []byte(b)}
	}
	route := "GET /api/v1/accounting/reports/trial-balance"
	diffs := Diff(route, csv(200, "text/csv", "a,b\n"), csv(200, "application/json", "a,b\n"), Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$header.Content-Type" {
		t.Fatalf("difference %+v, want $header.Content-Type", diffs[0])
	}
}

func TestRedirectLocationCompared(t *testing.T) {
	t.Parallel()
	redirect := func(location string) Response {
		h := http.Header{}
		h.Set("Location", location)
		return Response{Status: 302, Header: h}
	}
	route := "GET /oauth/authorize"
	allowed := Allowed{PythonHost: "python.example", GoHost: "go.example"}

	// The backends' own hosts differ and the query order differs: the same
	// redirect.
	py := redirect("https://python.example/oauth/authorize?client_id=x&state=y")
	gov := redirect("https://go.example/oauth/authorize?state=y&client_id=x")
	one(t, Diff(route, py, gov, allowed), 0)

	// A different path is reported.
	other := redirect("https://go.example/oauth/other?state=y&client_id=x")
	diffs := Diff(route, py, other, allowed)
	one(t, diffs, 1)
	if diffs[0].Path != "$header.Location" {
		t.Fatalf("difference %+v, want $header.Location", diffs[0])
	}

	// An outside host that differs is reported.
	outside := redirect("https://evil.example/oauth/authorize?client_id=x&state=y")
	one(t, Diff(route, py, outside, allowed), 1)

	// A relative Location and an absolute own-host one with the same path are
	// equal.
	relative := redirect("/oauth/authorize?client_id=x&state=y")
	one(t, Diff(route, relative, gov, allowed), 0)
}

func TestStrayClosingBraceReported(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/invoices"
	one(t, Diff(route, jsonResponse(200, `{"a":1}}`), jsonResponse(200, `{"a":1}`), Allowed{}), 1)
	one(t, Diff(route, jsonResponse(200, `{"a":1}]`), jsonResponse(200, `{"a":1}`), Allowed{}), 1)
	one(t, Diff(route, jsonResponse(200, `{"a":1}`), jsonResponse(200, `{"a":1}`), Allowed{}), 0)
}

func TestPdfComparesHeadersOnly(t *testing.T) {
	t.Parallel()
	pdf := func(status int, cd, b string) Response {
		h := http.Header{}
		h.Set("Content-Type", "application/pdf")
		h.Set("Content-Disposition", cd)
		return Response{Status: status, Header: h, Body: []byte(b)}
	}
	route := "GET /api/v1/invoices/1/pdf"
	py := pdf(200, `attachment; filename="invoice.pdf"`, "%PDF-1.4 python bytes")
	gov := pdf(200, `attachment; filename="invoice.pdf"`, "%PDF-1.7 go bytes")

	// The bytes never match; status and the two headers do.
	one(t, Diff(route, py, gov, Allowed{}), 0)

	// A header difference is reported.
	changed := pdf(200, `inline; filename="other.pdf"`, "%PDF-1.7 go bytes")
	diffs := Diff(route, py, changed, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$header.Content-Disposition" {
		t.Fatalf("difference %+v, want $header.Content-Disposition", diffs[0])
	}

	// A status difference is reported too.
	one(t, Diff(route, py, pdf(404, `attachment; filename="invoice.pdf"`, ""), Allowed{}), 1)
}

func TestAllowedRoutesMatchPathParams(t *testing.T) {
	t.Parallel()
	py := jsonResponse(200, `{"expected_cash":"0"}`)
	gov := jsonResponse(200, `{"expected_cash":"0.0000"}`)
	// The decimal rule is global, so this is equal with no entry at all.
	one(t, Diff("GET /api/v1/invoices/8f14e45f-ceea-467a", py, gov, Allowed{}), 0)

	// A path entry whose route names a parameter still matches.
	allowed := Allowed{Entries: []AllowedEntry{
		{Route: "GET /api/v1/invoices/{id}", Path: "$.expected_cash", Rule: RuleDecimalEqual, Note: "PN 50"},
	}}
	one(t, Diff("GET /api/v1/invoices/8f14e45f-ceea-467a", py, gov, allowed), 0)
}

func TestLiteralRoutePreferredOverPattern(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/invoices/awaiting-finalization"
	py := jsonResponse(200, `{"foo":1,"bar":2}`)
	gov := jsonResponse(200, `{"foo":9,"bar":9}`)

	// The pattern entry ignores both fields; the literal entry ignores only foo.
	allowed := Allowed{Entries: []AllowedEntry{
		{Route: route, Path: "$.foo", Rule: RuleIgnore, Note: "PN 53"},
		{Route: "GET /api/v1/invoices/{invoice_id}", Path: "$.foo", Rule: RuleIgnore, Note: "PN 53"},
		{Route: "GET /api/v1/invoices/{invoice_id}", Path: "$.bar", Rule: RuleIgnore, Note: "PN 53"},
	}}
	// Only the literal entries apply, so $.bar is reported.
	diffs := Diff(route, py, gov, allowed)
	one(t, diffs, 1)
	if diffs[0].Path != "$.bar" {
		t.Fatalf("difference %+v, want $.bar", diffs[0])
	}

	// Without the literal entry, the pattern applies and both are ignored.
	patternOnly := Allowed{Entries: allowed.Entries[1:]}
	one(t, Diff(route, py, gov, patternOnly), 0)
}

func TestBalanceSheetBalancesCompared(t *testing.T) {
	t.Parallel()
	route := "GET /api/v1/accounting/reports/balance-sheet"
	py := jsonResponse(200, `{"groups":[{"lines":[{"base_balance":"100.0000"}]}]}`)
	gov := jsonResponse(200, `{"groups":[{"lines":[{"base_balance":"999.0000"}]}]}`)
	one(t, Diff(route, py, gov, Allowed{}), 1)
}
