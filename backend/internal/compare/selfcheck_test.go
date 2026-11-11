package compare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A consistent Go ledger: 1100 IDR 300 open (AR aging IDR 300), 2000 IDR 50
// open (AP aging IDR 50), 1000 SGD revalued at a gain of 7 and a loss of 2 on
// another line.
const (
	goodTB = `{"as_of":"2026-11-11","rows":[` +
		`{"code":"1000","currency":"IDR","net":"700","base_debit":"1000.0000","base_credit":"300.0000"},` +
		`{"code":"1100","currency":"IDR","net":"300","base_debit":"1000.0000","base_credit":"700.0000"},` +
		`{"code":"2000","currency":"IDR","net":"-50","base_debit":"0","base_credit":"50.0000"},` +
		`{"code":"4000","currency":"IDR","net":"-950","base_debit":"0","base_credit":"950.0000"}],` +
		`"total_debit_base":"2000.0000","total_credit_base":"2000.0000"}`
	goodBS = `{"groups":[` +
		`{"type":"ASSET","total_base":"1000.0000","lines":[{"base_balance":"700.0000"},{"base_balance":"300.0000"}]},` +
		`{"type":"LIABILITY","total_base":"50.0000","lines":[{"base_balance":"50.0000"}]},` +
		`{"type":"INCOME","total_base":"950.0000","lines":[{"base_balance":"950.0000"}]}],` +
		`"assets_total_base":"1000.0000","liabilities_total_base":"50.0000","equity_total_base":"0",` +
		`"income_total_base":"950.0000","expense_total_base":"0"}`
	goodFX = `{"lines":[{"unrealised_gain_loss":"7.0000"},{"unrealised_gain_loss":"-2.0000"},{"unrealised_gain_loss":null}],` +
		`"total_unrealised_gain":"7.0000","total_unrealised_loss":"2.0000"}`
	goodAR = `{"as_of":"2026-11-11","kind":"AR","rows":[],"totals_by_currency":{"IDR":{"total":"300.0000"}}}`
	goodAP = `{"as_of":"2026-11-11","kind":"AP","rows":[],"totals_by_currency":{"IDR":{"total":"50.0000"}}}`
)

// paths is the differences' paths, for a readable failure.
func paths(diffs []Difference) []string {
	var out []string
	for _, d := range diffs {
		if d.Rule != RuleSelfCheck {
			out = append(out, "not a self-check: "+d.Path)
			continue
		}
		out = append(out, d.Path)
	}
	return out
}

func TestSelfCheckReports(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, route, body string
		want              []string
	}{
		{"a consistent trial balance", trialBalanceRoute, goodTB, nil},
		{
			"an unbalanced trial balance", trialBalanceRoute,
			strings.Replace(goodTB, `"total_credit_base":"2000.0000"`, `"total_credit_base":"1999.0000"`, 1),
			[]string{"$.total_debit_base", "$.total_credit_base"},
		},
		{
			"trial balance rows that do not sum to the totals", trialBalanceRoute,
			strings.Replace(goodTB, `"base_debit":"1000.0000","base_credit":"300.0000"`, `"base_debit":"999.0000","base_credit":"299.0000"`, 1),
			[]string{"$.total_debit_base", "$.total_credit_base"},
		},
		{
			"a trial balance row that is not a decimal", trialBalanceRoute,
			strings.Replace(goodTB, `"base_debit":"0","base_credit":"50.0000"`, `"base_debit":true,"base_credit":"50.0000"`, 1),
			[]string{"$.rows[2].base_debit"},
		},
		{"a consistent balance sheet", balanceSheetRoute, goodBS, nil},
		{
			"a group total that is not its lines' sum", balanceSheetRoute,
			strings.Replace(goodBS, `{"base_balance":"300.0000"}`, `{"base_balance":"301.0000"}`, 1),
			[]string{"$.groups[0].total_base"},
		},
		{
			"a typed total that is not its group's", balanceSheetRoute,
			strings.Replace(goodBS, `"liabilities_total_base":"50.0000"`, `"liabilities_total_base":"0"`, 1),
			[]string{"$.liabilities_total_base", "$.assets_total_base"},
		},
		{
			"assets that are not liabilities + equity + income - expense", balanceSheetRoute,
			strings.Replace(strings.Replace(goodBS, `"total_base":"950.0000","lines":[{"base_balance":"950.0000"}]`,
				`"total_base":"900.0000","lines":[{"base_balance":"900.0000"}]`, 1), `"income_total_base":"950.0000"`, `"income_total_base":"900.0000"`, 1),
			[]string{"$.assets_total_base"},
		},
		{"a consistent FX revaluation", fxRevaluationRoute, goodFX, nil},
		{
			"an FX gain that is not the lines' gains", fxRevaluationRoute,
			strings.Replace(goodFX, `"total_unrealised_gain":"7.0000"`, `"total_unrealised_gain":"9.0000"`, 1),
			[]string{"$.total_unrealised_gain"},
		},
		{
			"an FX loss that is not the lines' losses", fxRevaluationRoute,
			strings.Replace(goodFX, `"total_unrealised_loss":"2.0000"`, `"total_unrealised_loss":"0"`, 1),
			[]string{"$.total_unrealised_loss"},
		},
		{"another route has no check", arAgingRoute, `{"anything":1}`, nil},
		{
			"an empty trial balance answer fails", trialBalanceRoute, `{}`,
			[]string{"$.rows", "$.total_debit_base", "$.total_credit_base"},
		},
		{
			"an empty balance sheet answer fails", balanceSheetRoute, `{}`,
			[]string{"$.groups", "$.assets_total_base", "$.liabilities_total_base", "$.equity_total_base", "$.income_total_base", "$.expense_total_base"},
		},
		{
			"an empty FX revaluation answer fails", fxRevaluationRoute, `{}`,
			[]string{"$.lines", "$.total_unrealised_gain", "$.total_unrealised_loss"},
		},
		{
			"a null total fails", trialBalanceRoute, strings.Replace(goodTB, `"total_credit_base":"2000.0000"`, `"total_credit_base":null`, 1),
			[]string{"$.total_credit_base", "$.total_debit_base", "$.total_credit_base"},
		},
		{
			"a group without lines fails", balanceSheetRoute,
			strings.Replace(goodBS, `"lines":[{"base_balance":"50.0000"}]`, `"x":1`, 1),
			[]string{"$.groups[1].lines", "$.groups[1].total_base"},
		},
		{
			"a row that is not an object fails", trialBalanceRoute, strings.Replace(goodTB, `"rows":[`, `"rows":[7,`, 1),
			[]string{"$.rows[0]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := paths(selfCheck(tc.route, jsonResponse(200, tc.body)))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("self-check paths %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("a Go error answer fails", func(t *testing.T) {
		t.Parallel()
		if got := selfCheck(trialBalanceRoute, jsonResponse(500, `{"detail":"x"}`)); len(got) != 1 || got[0].Rule != RuleSelfCheck {
			t.Fatalf("self-check %+v, want one failure", got)
		}
	})
}

func TestAgingCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		tb, ar, ap     string
		haveAR, haveAP bool
		want           []string
	}{
		{"1100 and 2000 match the aging", goodTB, goodAR, goodAP, true, true, nil},
		{
			"1100 above the AR aging", goodTB, strings.Replace(goodAR, `"300.0000"`, `"200.0000"`, 1), goodAP, true, true,
			[]string{"$.rows[code=1100,currency=IDR]"},
		},
		{
			"an AR currency with no 1100 row", goodTB, strings.Replace(goodAR, `}}}`, `},"SGD":{"total":"5"}}}`, 1), goodAP, true, true,
			[]string{"$.rows[code=1100,currency=SGD]"},
		},
		{
			"2000 not the AP aging", goodTB, goodAR, strings.Replace(goodAP, `"50.0000"`, `"40.0000"`, 1), true, true,
			[]string{"$.rows[code=2000,currency=IDR]"},
		},
		// haveAP false: the run met no AP aging case (the response is unused).
		{"no AP aging in the run", goodTB, goodAR, goodAP, true, false, []string{"$.rows"}},
		{"an AP aging that is not 200 JSON", goodTB, goodAR, "", true, true, []string{"$.rows"}},
		{
			"an aging without totals", goodTB, `{"as_of":"2026-11-11","rows":[]}`, goodAP, true, true,
			[]string{"$.totals_by_currency"},
		},
		{
			"an aging of another date", goodTB, strings.Replace(goodAR, "2026-11-11", "2026-11-10", 1), goodAP, true, true,
			[]string{"$.as_of"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := paths(agingCheck(jsonResponse(200, tc.tb), jsonResponse(200, tc.ar), jsonResponse(200, tc.ap), tc.haveAR, tc.haveAP))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("aging-check paths %q, want %q", got, tc.want)
			}
		})
	}
}

// ledgerBackend answers the three ledger reports and the two agings, with
// the Go trial balance given.
func ledgerBackend(t *testing.T, tb string) func(w http.ResponseWriter, r *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case trialBalanceRoute:
			jsonBody(w, http.StatusOK, tb)
		case balanceSheetRoute:
			jsonBody(w, http.StatusOK, goodBS)
		case fxRevaluationRoute:
			jsonBody(w, http.StatusOK, goodFX)
		case arAgingRoute:
			jsonBody(w, http.StatusOK, goodAR)
		case apAgingRoute:
			jsonBody(w, http.StatusOK, goodAP)
		default:
			http.NotFound(w, r)
		}
	}
}

// ledgerCorpus is the five report cases, the three ledger ones PN 166.
func ledgerCorpus(classify string) Corpus {
	return Corpus{Cases: []Case{
		{ID: "report-trial-balance", Route: "GET " + trialBalanceRoute, Classify: classify},
		{ID: "report-balance-sheet", Route: "GET " + balanceSheetRoute, Classify: classify},
		{ID: "report-fx-revaluation", Route: "GET " + fxRevaluationRoute, Classify: classify},
		{ID: "report-ar-aging", Route: "GET " + arAgingRoute},
		{ID: "report-ap-aging", Route: "GET " + apAgingRoute},
	}}
}

// TestRunnerSelfChecksLedgerReports pins that a PN 166 case's Go answer is
// checked in the run and a failure is a difference (exit 1), while Python's
// answer plays no part; the aging cross-check runs only when asked.
func TestRunnerSelfChecksLedgerReports(t *testing.T) {
	t.Parallel()
	allowed, _, err := LoadAllowedFiles(shippedAllowed, prodcopyAllowed)
	if err != nil {
		t.Fatalf("LoadAllowedFiles: %v", err)
	}
	const label = "PN 166 (production-copy run only)"
	unbalanced := strings.Replace(goodTB, `"total_credit_base":"2000.0000"`, `"total_credit_base":"1999.0000"`, 1)
	offAging := strings.Replace(strings.Replace(goodTB, `"net":"300"`, `"net":"301"`, 1), `"net":"700","base_debit":"1000.0000"`, `"net":"699","base_debit":"1000.0000"`, 1)
	cases := []struct {
		name     string
		classify string
		tb       string
		aging    bool
		want     int
	}{
		{"a consistent ledger", label, goodTB, true, 0},
		{"an unbalanced trial balance fails", label, unbalanced, false, 2},
		{"an unclassified case is not self-checked", "", unbalanced, false, 0},
		{"1100 off the AR aging fails with the cross-check", label, offAging, true, 1},
		{"without the cross-check 1100 is not read", label, offAging, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Python's ledger is empty; the overlay ignores what differs.
			py := (&recorder{handle: ledgerBackend(t, `{"as_of":"2026-11-11","rows":[],"total_debit_base":"0","total_credit_base":"0"}`)}).start(t)
			gov := (&recorder{handle: ledgerBackend(t, tc.tb)}).start(t)
			report, err := runWith(t, py, gov, ledgerCorpus(tc.classify), allowed, tc.aging)
			if err != nil {
				t.Fatalf("RunReads: %v", err)
			}
			var self []Difference
			for _, c := range report.Cases {
				for _, d := range c.Differences {
					if d.Rule == RuleSelfCheck {
						self = append(self, d)
					} else if !strings.Contains(c.ID, "balance-sheet") && !strings.Contains(c.ID, "fx") {
						t.Errorf("case %s: unexpected difference %+v", c.ID, d)
					}
				}
			}
			if len(self) != tc.want {
				t.Fatalf("self-check differences %+v, want %d", self, tc.want)
			}
			for _, d := range self {
				if d.Classify != label {
					t.Errorf("self-check %+v: want classify %q", d, label)
				}
			}
			if tc.want > 0 && ExitCode(report, nil, false) != 1 {
				t.Errorf("exit code %d, want 1: a failed self-check fails the run", ExitCode(report, nil, false))
			}
		})
	}
}

// TestRunnerAgingCheckNeedsTheTrialBalance pins that asking for the
// cross-check with no PN 166 trial-balance case is a run error, never a pass.
func TestRunnerAgingCheckNeedsTheTrialBalance(t *testing.T) {
	t.Parallel()
	py := (&recorder{handle: ledgerBackend(t, goodTB)}).start(t)
	gov := (&recorder{handle: ledgerBackend(t, goodTB)}).start(t)
	if _, err := runWith(t, py, gov, ledgerCorpus(""), Allowed{}, true); err == nil {
		t.Fatal("RunReads succeeded, want an error: no PN 166 trial balance to check")
	}
}

// TestReportsListUnusedAsEmptyArray pins that unused_allowed is [] (not null)
// when nothing is unused, in both reports.
func TestReportsListUnusedAsEmptyArray(t *testing.T) {
	t.Parallel()
	for name, v := range map[string]any{"reads": Report{UnusedAllowed: []UnusedAllowed{}}, "writes": WritesReport{UnusedAllowed: []UnusedAllowed{}}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"unused_allowed":[]`) {
			t.Errorf("%s report %s, want unused_allowed []", name, b)
		}
	}
	py := (&recorder{handle: ledgerBackend(t, goodTB)}).start(t)
	gov := (&recorder{handle: ledgerBackend(t, goodTB)}).start(t)
	report, err := runWith(t, py, gov, Corpus{Cases: []Case{{ID: "ar", Route: "GET " + arAgingRoute}}}, Allowed{}, false)
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"unused_allowed":[]`) {
		t.Errorf("run report %s, want unused_allowed []", b)
	}
}

// runWith is run with the aging cross-check on or off.
func runWith(t *testing.T, py, gov *httptest.Server, corpus Corpus, allowed Allowed, aging bool) (Report, error) {
	t.Helper()
	return RunReads(context.Background(), ReadOptions{
		PythonURL: py.URL, GoURL: gov.URL,
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Corpus: corpus, Allowed: allowed,
		Client: py.Client(), LedgerMatchesAging: aging,
	})
}

// TestWritesRefusedReportListsUnusedAsEmptyArray pins [] on a write run that
// stops before any step.
func TestWritesRefusedReportListsUnusedAsEmptyArray(t *testing.T) {
	t.Parallel()
	report, err := RunWrites(context.Background(), WritesOptions{Env: func(k string) string {
		if k == "RESEND_API_KEY" {
			return "set"
		}
		return ""
	}})
	if err == nil {
		t.Fatal("RunWrites succeeded, want the RESEND_API_KEY refusal")
	}
	if report.UnusedAllowed == nil {
		t.Fatal("unused_allowed is nil, want []")
	}
}

// TestReadsFailedReportListsUnusedAsEmptyArray pins [] on a read run that
// stops at the sign-in check.
func TestReadsFailedReportListsUnusedAsEmptyArray(t *testing.T) {
	t.Parallel()
	refuse := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginHandler(w, r)
			return
		}
		jsonBody(w, http.StatusUnauthorized, `{}`)
	}
	py := (&recorder{handle: refuse}).start(t)
	gov := (&recorder{handle: refuse}).start(t)
	report, err := runWith(t, py, gov, Corpus{}, Allowed{}, false)
	if err == nil {
		t.Fatal("RunReads succeeded, want the sign-in check to fail")
	}
	if report.UnusedAllowed == nil {
		t.Fatal("unused_allowed is nil, want []")
	}
}
