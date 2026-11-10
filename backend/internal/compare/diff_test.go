package compare

import (
	"net/http"
	"testing"
	"time"
)

// jsonResponse is body with a JSON content type.
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diffs := Diff("GET /api/v1/invoices/1", jsonResponse(200, tc.py), jsonResponse(200, tc.gov), Allowed{})
			if tc.wantDiffer {
				one(t, diffs, 1)
				if diffs[0].Path != "$.amount" || diffs[0].Rule != RuleDecimalEqual {
					t.Fatalf("difference %+v, want path $.amount rule %s", diffs[0], RuleDecimalEqual)
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

func TestErrorStatusDifference(t *testing.T) {
	t.Parallel()
	py := jsonResponse(400, `{"detail":"bad input"}`)
	gov := jsonResponse(422, `{"detail":"bad input"}`)
	diffs := Diff("POST /api/v1/customers", py, gov, Allowed{})
	one(t, diffs, 1)
	if diffs[0].Path != "$.status" || diffs[0].Rule != RuleStatus {
		t.Fatalf("difference %+v, want path $.status rule %s", diffs[0], RuleStatus)
	}

	allowed := Allowed{Entries: []AllowedEntry{
		{Route: "POST /api/v1/customers", Path: "$.status", Rule: RuleStatus, Note: "PN 16"},
	}}
	if diffs := Diff("POST /api/v1/customers", py, gov, allowed); len(diffs) != 0 {
		t.Fatalf("allowed status difference reported %+v", diffs)
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

	// null on both sides is equal.
	one(t, Diff(route, jsonResponse(200, `{"notes":null}`), jsonResponse(200, `{"notes":null}`), Allowed{}), 0)
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
