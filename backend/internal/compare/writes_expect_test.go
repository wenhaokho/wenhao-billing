package compare

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// thingsScenario is two POST steps; the first carries the given expect_status
// and note.
func thingsScenario(expect *ExpectStatus, note string, extract map[string]string, secondPath string) WritesScenarios {
	return WritesScenarios{Scenarios: []WritesScenario{{
		ID: "things",
		Steps: []WriteStep{
			{ID: "first", Method: http.MethodPost, Path: "/api/v1/things", ExpectStatus: expect, Note: note, Extract: extract},
			{ID: "second", Method: http.MethodPost, Path: secondPath, ExpectStatus: expect, Note: note},
		},
	}}}
}

// TestWritesExpectStatus pins the per-step expect_status rule: equal statuses
// continue and compare the bodies, expected-different statuses continue and
// report the note without comparing the bodies, and any other status pair is a
// hard failure naming both.
func TestWritesExpectStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		pyStatus     int
		goStatus     int
		pyBody       string
		goBody       string
		expect       ExpectStatus
		note         string
		wantErr      string
		wantNote     string
		wantBodyDiff bool
	}{
		{
			name: "equal", pyStatus: 400, goStatus: 400,
			pyBody: `{"detail":"a"}`, goBody: `{"detail":"b"}`,
			expect: ExpectStatus{400, 400}, wantBodyDiff: true,
		},
		{
			name: "different", pyStatus: 400, goStatus: 200,
			pyBody: `{"detail":"no_active_entry"}`, goBody: `{"status":"FLAGGED"}`,
			expect: ExpectStatus{400, 200}, note: "PN 161", wantNote: "PN 161",
		},
		{
			name: "mismatch", pyStatus: 200, goStatus: 200,
			pyBody: `{"ok":true}`, goBody: `{"ok":true}`,
			expect: ExpectStatus{400, 200}, note: "PN 161",
			wantErr: "status python 200 go 200, expected python 400 go 200",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pyDB, goDB := openTestDB(t), openTestDB(t)
			var pyHits, goHits atomic.Int32
			py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
				pyHits.Add(1)
				jsonBody(w, tc.pyStatus, tc.pyBody)
			})
			gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
				goHits.Add(1)
				jsonBody(w, tc.goStatus, tc.goBody)
			})
			e := tc.expect
			report, err := RunWrites(context.Background(),
				writeOptions(t, py, gov, pyDB, goDB, thingsScenario(&e, tc.note, nil, "/api/v1/things")))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "first") {
					t.Fatalf("error %v, want %q naming the step", err, tc.wantErr)
				}
				if pyHits.Load() != 1 || goHits.Load() != 1 {
					t.Fatalf("hits python %d go %d, want the run stopped after the first step", pyHits.Load(), goHits.Load())
				}
				return
			}
			if err != nil {
				t.Fatalf("RunWrites: %v", err)
			}
			steps := report.Scenarios[0].Steps
			if len(steps) != 2 || pyHits.Load() != 2 || goHits.Load() != 2 {
				t.Fatalf("steps %d, hits python %d go %d, want the run to continue to the second step", len(steps), pyHits.Load(), goHits.Load())
			}
			first := steps[0]
			if first.ExpectedStatusDiff != tc.wantNote {
				t.Fatalf("ExpectedStatusDiff %q, want %q", first.ExpectedStatusDiff, tc.wantNote)
			}
			if tc.wantBodyDiff != (len(first.Differences) > 0) {
				t.Fatalf("first step differences %+v, want body diff %v", first.Differences, tc.wantBodyDiff)
			}
		})
	}
}

// TestWritesExpectStatusUnsetStillAborts pins that a step without expect_status
// keeps the hard failure on a non-2xx answer.
func TestWritesExpectStatusUnsetStillAborts(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusBadRequest, `{}`) })
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusOK, `{}`) })
	_, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, thingsScenario(nil, "", nil, "/api/v1/things")))
	if err == nil || !strings.Contains(err.Error(), "python status 400") {
		t.Fatalf("error %v, want the python 400 hard failure", err)
	}
}

// TestWritesExtractSkippedOnNon2xx pins that an extract is not taken from a
// side whose answer was not 2xx: a later step that needs the value on that
// side fails clearly, and a run that does not need it carries on.
func TestWritesExtractSkippedOnNon2xx(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		secondPath string
		wantErr    string
	}{
		{"unused", "/api/v1/things", ""},
		{"used", "/api/v1/things/{customer_id}", "not extracted on this backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pyDB, goDB := openTestDB(t), openTestDB(t)
			// Python refuses with an error body (no id); Go answers 200 with one.
			py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
				jsonBody(w, http.StatusBadRequest, `{"detail":"refused"}`)
			})
			gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
				jsonBody(w, http.StatusOK, `{"customer_id":"c-go"}`)
			})
			scenarios := thingsScenario(&ExpectStatus{400, 200}, "PN 1",
				map[string]string{"customer_id": "$.customer_id"}, tc.secondPath)
			_, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("RunWrites: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestWritesParseExpectStatus pins the load-time validation of expect_status:
// an int is both backends, a map needs python and go, a differing pair needs a
// note, and a status must be an HTTP status.
func TestWritesParseExpectStatus(t *testing.T) {
	t.Parallel()
	doc := func(step string) []byte {
		return []byte("scenarios:\n  - id: x\n    steps:\n      - {id: s, method: POST, path: /x" + step + "}\n")
	}
	cases := []struct {
		name    string
		step    string
		wantErr string
		want    *ExpectStatus
	}{
		{"single int", `, expect_status: 400`, "", &ExpectStatus{400, 400}},
		{"map same", `, expect_status: {python: 400, go: 400}`, "", &ExpectStatus{400, 400}},
		{"map differs with note", `, expect_status: {python: 400, go: 200}, note: "PN 161"`, "", &ExpectStatus{400, 200}},
		{"map differs without note", `, expect_status: {python: 400, go: 200}`, "needs a note", nil},
		{"map differs blank note", `, expect_status: {python: 400, go: 200}, note: " "`, "needs a note", nil},
		{"map missing go", `, expect_status: {python: 400}`, "python and go", nil},
		{"out of range", `, expect_status: 99`, "not an HTTP status", nil},
		{"not a status shape", `, expect_status: [400]`, "int or", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, err := ParseWrites(doc(tc.step))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWrites: %v", err)
			}
			if got := w.Scenarios[0].Steps[0].ExpectStatus; got == nil || *got != *tc.want {
				t.Fatalf("ExpectStatus %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestTypedPlaceholders pins typed substitution: a value that is exactly one
// placeholder keeps its JSON type, a placeholder inside a longer string stays
// string interpolation.
func TestTypedPlaceholders(t *testing.T) {
	t.Parallel()
	extracted := map[string]idPair{
		"account_id": {python: "7", goID: "501", pyVal: json.Number("7"), goVal: json.Number("501")},
		"flag":       {python: "true", goID: "true", pyVal: true, goVal: true},
		"code":       {python: "ab", goID: "cd"},
	}
	body := map[string]any{
		"expense_account_id": "{account_id}",
		"active":             "{flag}",
		"code":               "{code}",
		"label":              "acct {account_id}",
		"lines":              []any{map[string]any{"account": "{account_id}"}},
	}
	for _, tc := range []struct {
		goSide bool
		want   string
	}{
		{false, `{"active":true,"code":"ab","expense_account_id":7,"label":"acct 7","lines":[{"account":7}]}`},
		{true, `{"active":true,"code":"cd","expense_account_id":501,"label":"acct 501","lines":[{"account":501}]}`},
	} {
		t.Run(map[bool]string{false: "python", true: "go"}[tc.goSide], func(t *testing.T) {
			t.Parallel()
			got, err := buildBody(body, extracted, tc.goSide)
			if err != nil {
				t.Fatalf("buildBody: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("body %s, want %s", got, tc.want)
			}
		})
	}
}

// TestExtractValueKeepsNumbers pins that an extracted number keeps its type
// and an extracted string stays a string.
func TestExtractValueKeepsNumbers(t *testing.T) {
	t.Parallel()
	id, v, err := extractValue([]byte(`{"account_id":42,"customer_id":"c-1"}`), "$.account_id")
	if err != nil || id != "42" {
		t.Fatalf("extractValue = %q, %v", id, err)
	}
	if n, ok := v.(json.Number); !ok || n.String() != "42" {
		t.Fatalf("value %#v, want json.Number 42", v)
	}
	id, v, err = extractValue([]byte(`{"account_id":42,"customer_id":"c-1"}`), "$.customer_id")
	if err != nil || id != "c-1" || v != "c-1" {
		t.Fatalf("extractValue = %q, %#v, %v", id, v, err)
	}
}

// TestWritesExpectDetail pins expect_detail: every non-2xx side must contain
// the text, a 2xx side is not checked, and a miss is a hard failure.
func TestWritesExpectDetail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		pyBody  string
		wantErr bool
	}{
		{"contained", `{"detail":"no active journal entry to reverse"}`, false},
		{"missing", `{"detail":"something else"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pyDB, goDB := openTestDB(t), openTestDB(t)
			py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusBadRequest, tc.pyBody) })
			gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) { jsonBody(w, http.StatusOK, `{"status":"FLAGGED"}`) })
			sc := thingsScenario(&ExpectStatus{400, 200}, "PN 161", nil, "/api/v1/things")
			sc.Scenarios[0].Steps[0].ExpectDetail = "no active journal entry"
			_, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, sc))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "python answered 400 without expect_detail") {
					t.Fatalf("error %v, want the expect_detail failure", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunWrites: %v", err)
			}
		})
	}
}

// TestWritesGoOnlyStep pins go_only: the step is sent to Go alone (a route
// Python never had, such as a bill payment's reversal, PN 165), its extract is
// taken from Go with no table and no id pair, its answer is not compared, and
// the step result carries the note. A non-2xx Go answer is a hard failure.
func TestWritesGoOnlyStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		goStatus int
		wantErr  string
	}{
		{"ok", http.StatusOK, ""},
		{"go refuses", http.StatusBadRequest, "go status 400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pyDB, goDB := openTestDB(t), openTestDB(t)
			py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					loginHandler(w, r)
				case "/api/v1/auth/me":
					jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
				default:
					jsonBody(w, http.StatusOK, `{"bill_id":"b-py"}`)
				}
			}}
			gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/me":
					jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
				case "/api/v1/bills/b-go/payments":
					jsonBody(w, tc.goStatus, `[{"bill_payment_ref":"bp-go"}]`)
				default:
					jsonBody(w, http.StatusOK, `{"bill_id":"b-go"}`)
				}
			}}
			pySrv, goSrv := py.start(t), gov.start(t)
			scenarios := WritesScenarios{Scenarios: []WritesScenario{{
				ID: "bill",
				Steps: []WriteStep{
					{ID: "create", Method: http.MethodPost, Path: "/api/v1/bills", Extract: map[string]string{"bill_id": "$.bill_id"}},
					{
						ID: "list", Method: http.MethodGet, Path: "/api/v1/bills/{bill_id}/payments", GoOnly: true, Note: "PN 165",
						Extract: map[string]string{"bp": "$[0].bill_payment_ref"},
					},
					{ID: "reverse", Method: http.MethodPost, Path: "/api/v1/bills/{bill_id}/payments/{bp}/reverse", GoOnly: true, Note: "PN 165"},
				},
			}}}
			report, err := RunWrites(context.Background(), writeOptions(t, pySrv, goSrv, pyDB, goDB, scenarios))
			pyBills := 0
			for _, c := range py.seen() {
				if strings.HasPrefix(c.target, "/api/v1/bills") {
					pyBills++
				}
			}
			if pyBills != 1 {
				t.Fatalf("python got %d bill requests, want only the create", pyBills)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "list") {
					t.Fatalf("error %v, want %q naming the step", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunWrites: %v", err)
			}
			var reversed bool
			for _, c := range gov.seen() {
				if c.method == http.MethodPost && c.target == "/api/v1/bills/b-go/payments/bp-go/reverse" {
					reversed = true
				}
			}
			if !reversed {
				t.Fatalf("go requests %+v, want the reversal with Go's own ids", gov.seen())
			}
			steps := report.Scenarios[0].Steps
			if len(steps) != 3 || steps[1].GoOnly != "PN 165" || steps[2].GoOnly != "PN 165" || steps[1].Python != 0 {
				t.Fatalf("steps %+v, want both go_only steps with the note and no Python status", steps)
			}
			if report.Differences != 0 {
				t.Fatalf("differences %d, want 0: a go_only answer is not compared", report.Differences)
			}
		})
	}
}

// TestWritesParseGoOnly pins the go_only validation: a note is required,
// expect_status and expect_detail do not apply, and a step sent to both
// backends never uses a value only Go answered with.
func TestWritesParseGoOnly(t *testing.T) {
	t.Parallel()
	head := "scenarios:\n  - id: x\n    steps:\n      - {id: a, method: POST, path: /a, extract: {bill_id: \"$.bill_id\"}}\n"
	cases := []struct {
		name, steps, wantErr string
	}{
		{"with note", `      - {id: g, method: GET, path: "/a/{bill_id}", go_only: true, note: "PN 165", extract: {bp: "$[0].bp"}}
      - {id: h, method: POST, path: "/a/{bp}", go_only: true, note: "PN 165"}
`, ""},
		{"no note", `      - {id: g, method: GET, path: /g, go_only: true}
`, "needs a note"},
		{"expect_status", `      - {id: g, method: GET, path: /g, go_only: true, note: "PN 165", expect_status: 200}
`, "go_only"},
		{"expect_detail", `      - {id: g, method: GET, path: /g, go_only: true, note: "PN 165", expect_detail: "x"}
`, "go_only"},
		{"used in a path by both", `      - {id: g, method: GET, path: /g, go_only: true, note: "PN 165", extract: {bp: "$[0].bp"}}
      - {id: h, method: POST, path: "/a/{bp}"}
`, "only Go answered"},
		{"used in a body by both", `      - {id: g, method: GET, path: /g, go_only: true, note: "PN 165", extract: {bp: "$[0].bp"}}
      - {id: h, method: POST, path: /h, body: {bp: "{bp}"}}
`, "only Go answered"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWrites([]byte(head + tc.steps))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseWrites: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}
