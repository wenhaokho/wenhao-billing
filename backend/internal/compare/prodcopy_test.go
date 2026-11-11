package compare

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The allowances and tooling fixes found by the first production-copy run
// (plan 11 Task 6, 2026-11-11).

const (
	shippedAllowed  = "../../port/allowed-diffs.yaml"
	prodcopyAllowed = "../../port/allowed-diffs-prodcopy.yaml"
)

// TestShippedListAllowances pins the PN 40, 44, 100 and 91 entries the run
// needed, each by route and path: a permutation passes, a changed value is
// still reported.
func TestShippedListAllowances(t *testing.T) {
	t.Parallel()
	a, err := LoadAllowedFile(shippedAllowed)
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}
	if hasEntry(a, "GET /api/v1/customers/{customer_id}/billing-summary", "$.open_invoices", RuleUnordered) {
		t.Error("PN 40: the $.open_invoices entry names no field of the answer and should be gone")
	}
	if hasEntry(a, "GET /api/v1/invoices/recurring-templates/{template_id}", "$.children", RuleUnordered) {
		t.Error("PN 47: the $.children entry names no field of the answer and should be gone")
	}
	const twoLines = `[{"line_item_id":"a","position":0},{"line_item_id":"b","position":0}]`
	const swapped = `[{"line_item_id":"b","position":0},{"line_item_id":"a","position":0}]`
	cases := []struct {
		name       string
		route      string
		py, gov    string
		wantDiffer int
	}{
		{
			name:  "PN 44 + 100: the invoice list, created_at ties and position ties swapped",
			route: "GET /api/v1/invoices?status=OPEN&status=SENT",
			py: `[{"invoice_id":"1","created_at":"2026-11-01T02:00:00.009901","line_items":` + twoLines + `},` +
				`{"invoice_id":"2","created_at":"2026-11-01T02:00:00.009901","line_items":[]},` +
				`{"invoice_id":"3","created_at":"2026-10-01T00:00:00","line_items":[]}]`,
			gov: `[{"invoice_id":"2","created_at":"2026-11-01T02:00:00.009901Z","line_items":[]},` +
				`{"invoice_id":"1","created_at":"2026-11-01T02:00:00.009901Z","line_items":` + swapped + `},` +
				`{"invoice_id":"3","created_at":"2026-10-01T00:00:00Z","line_items":[]}]`,
		},
		{
			name:  "PN 100: the invoice list in another created_at order is reported",
			route: "GET /api/v1/invoices",
			py:    `[{"invoice_id":"2","created_at":"2026-01-02T00:00:00"},{"invoice_id":"1","created_at":"2026-01-01T00:00:00"}]`,
			gov:   `[{"invoice_id":"1","created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"2","created_at":"2026-01-02T00:00:00Z"}]`,
			// Python DESC, Go ASC: both elements differ in both fields.
			wantDiffer: 4,
		},
		{
			name:  "PN 44: lines in another position order are reported",
			route: "GET /api/v1/invoices",
			py:    `[{"invoice_id":"1","line_items":[{"line_item_id":"a","position":0},{"line_item_id":"b","position":1}]}]`,
			gov:   `[{"invoice_id":"1","line_items":[{"line_item_id":"b","position":1},{"line_item_id":"a","position":0}]}]`,
			// Positions 0,1 against 1,0: both lines differ in both fields.
			wantDiffer: 4,
		},
		{
			name:       "PN 44: lines in another position order are reported on the detail too",
			route:      "GET /api/v1/invoices/i1",
			py:         `{"line_items":[{"line_item_id":"a","position":0},{"line_item_id":"b","position":1}]}`,
			gov:        `{"line_items":[{"line_item_id":"b","position":1},{"line_item_id":"a","position":0}]}`,
			wantDiffer: 4,
		},
		{
			name:       "PN 40: the billing summary in another due_date order is reported",
			route:      "GET /api/v1/customers/c1/billing-summary",
			py:         `{"unpaid_invoices":[{"invoice_id":"1","due_date":"2026-01-01"},{"invoice_id":"2","due_date":null}]}`,
			gov:        `{"unpaid_invoices":[{"invoice_id":"2","due_date":null},{"invoice_id":"1","due_date":"2026-01-01"}]}`,
			wantDiffer: 4,
		},
		{
			name:  "PN 40: a customer's invoices, issue_date and created_at ties swapped",
			route: "GET /api/v1/customers/c1/invoices",
			py: `[{"invoice_id":"t1","issue_date":null,"created_at":"2026-01-01T00:00:00"},{"invoice_id":"t2","issue_date":null,"created_at":"2026-01-01T00:00:00"},` +
				`{"invoice_id":"i1","issue_date":"2026-02-01","created_at":"2026-01-01T00:00:00"}]`,
			gov: `[{"invoice_id":"t2","issue_date":null,"created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"t1","issue_date":null,"created_at":"2026-01-01T00:00:00Z"},` +
				`{"invoice_id":"i1","issue_date":"2026-02-01","created_at":"2026-01-01T00:00:00Z"}]`,
		},
		{
			name:       "PN 40: a customer's invoices with equal issue_date in another created_at order are reported",
			route:      "GET /api/v1/customers/c1/invoices",
			py:         `[{"invoice_id":"a","issue_date":"2026-02-01","created_at":"2026-01-02T00:00:00"},{"invoice_id":"b","issue_date":"2026-02-01","created_at":"2026-01-01T00:00:00"}]`,
			gov:        `[{"invoice_id":"b","issue_date":"2026-02-01","created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"a","issue_date":"2026-02-01","created_at":"2026-01-02T00:00:00Z"}]`,
			wantDiffer: 4,
		},
		{
			name:  "PN 53: awaiting finalization, created_at ties swapped",
			route: "GET /api/v1/invoices/awaiting-finalization",
			py:    `[{"invoice_id":"a","created_at":"2026-01-01T00:00:00"},{"invoice_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:   `[{"invoice_id":"b","created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"a","created_at":"2026-01-01T00:00:00Z"}]`,
		},
		{
			name:  "PN 78: projects, bills and project invoices, created_at ties swapped",
			route: "GET /api/v1/projects",
			py:    `[{"project_id":"a","created_at":"2026-01-01T00:00:00"},{"project_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:   `[{"project_id":"b","created_at":"2026-01-01T00:00:00Z"},{"project_id":"a","created_at":"2026-01-01T00:00:00Z"}]`,
		},
		{
			name:       "PN 78: projects in another created_at order are reported",
			route:      "GET /api/v1/projects",
			py:         `[{"project_id":"a","created_at":"2026-01-02T00:00:00"},{"project_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:        `[{"project_id":"b","created_at":"2026-01-01T00:00:00Z"},{"project_id":"a","created_at":"2026-01-02T00:00:00Z"}]`,
			wantDiffer: 4,
		},
		{
			name:       "PN 78: bills in another created_at order are reported",
			route:      "GET /api/v1/bills",
			py:         `[{"bill_id":"a","created_at":"2026-01-02T00:00:00"},{"bill_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:        `[{"bill_id":"b","created_at":"2026-01-01T00:00:00Z"},{"bill_id":"a","created_at":"2026-01-02T00:00:00Z"}]`,
			wantDiffer: 4,
		},
		{
			name:       "PN 78: a project's invoices in another created_at order are reported",
			route:      "GET /api/v1/projects/p1/invoices",
			py:         `[{"invoice_id":"a","created_at":"2026-01-02T00:00:00"},{"invoice_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:        `[{"invoice_id":"b","created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"a","created_at":"2026-01-02T00:00:00Z"}]`,
			wantDiffer: 4,
		},
		{
			name:       "PN 53: awaiting finalization in another created_at order is reported",
			route:      "GET /api/v1/invoices/awaiting-finalization",
			py:         `[{"invoice_id":"a","created_at":"2026-01-02T00:00:00"},{"invoice_id":"b","created_at":"2026-01-01T00:00:00"}]`,
			gov:        `[{"invoice_id":"b","created_at":"2026-01-01T00:00:00Z"},{"invoice_id":"a","created_at":"2026-01-02T00:00:00Z"}]`,
			wantDiffer: 4,
		},
		{
			name:       "the invoice list still compares a line's value",
			route:      "GET /api/v1/invoices",
			py:         `[{"invoice_id":"1","line_items":` + twoLines + `}]`,
			gov:        `[{"invoice_id":"1","line_items":[{"line_item_id":"a","position":0},{"line_item_id":"c","position":0}]}]`,
			wantDiffer: 1,
		},
		{
			name:  "PN 44: a customer's invoices",
			route: "GET /api/v1/customers/c1/invoices",
			py:    `[{"invoice_id":"1","line_items":` + twoLines + `}]`,
			gov:   `[{"invoice_id":"1","line_items":` + swapped + `}]`,
		},
		{
			name:  "PN 44: the recurring templates",
			route: "GET /api/v1/invoices/recurring-templates",
			py:    `[{"invoice_id":"1","line_items":` + twoLines + `}]`,
			gov:   `[{"invoice_id":"1","line_items":` + swapped + `}]`,
		},
		{
			name:  "PN 40 + 44: the billing summary's four lists and their lines",
			route: "GET /api/v1/customers/c1/billing-summary",
			py: `{"milestone":[{"invoice_id":"1","line_items":` + twoLines + `},{"invoice_id":"2","line_items":[]}],` +
				`"recurring":[{"invoice_id":"3","line_items":` + twoLines + `},{"invoice_id":"4","line_items":[]}],` +
				`"usage":[{"invoice_id":"5","line_items":` + twoLines + `},{"invoice_id":"6","line_items":[]}],` +
				`"unpaid_invoices":[{"invoice_id":"1","line_items":` + twoLines + `},{"invoice_id":"3","line_items":[]}],"total_unpaid":"10"}`,
			gov: `{"milestone":[{"invoice_id":"2","line_items":[]},{"invoice_id":"1","line_items":` + swapped + `}],` +
				`"recurring":[{"invoice_id":"4","line_items":[]},{"invoice_id":"3","line_items":` + swapped + `}],` +
				`"usage":[{"invoice_id":"6","line_items":[]},{"invoice_id":"5","line_items":` + swapped + `}],` +
				`"unpaid_invoices":[{"invoice_id":"3","line_items":[]},{"invoice_id":"1","line_items":` + swapped + `}],"total_unpaid":"10"}`,
		},
		{
			name:       "the billing summary still compares its total",
			route:      "GET /api/v1/customers/c1/billing-summary",
			py:         `{"milestone":[],"total_unpaid":"10"}`,
			gov:        `{"milestone":[],"total_unpaid":"11"}`,
			wantDiffer: 1,
		},
		{
			name:       "PN 44 is on line_items only: an invoice detail's other list is ordered",
			route:      "GET /api/v1/customers/c1/billing-summary",
			py:         `{"milestone":[{"invoice_id":"1","payments":[1,2]}]}`,
			gov:        `{"milestone":[{"invoice_id":"1","payments":[2,1]}]}`,
			wantDiffer: 2,
		},
		{
			name:  "PN 91: the webhook answer's confidence_score scale",
			route: "POST /api/v1/payments/intake/webhook",
			py:    `{"payment_id":"p","confidence_score":"92.1889","amount":"1000.0000"}`,
			gov:   `{"payment_id":"p","confidence_score":"92.19","amount":"1000.0000"}`,
		},
		{
			name:  "PN 91: the email answer's confidence_score scale",
			route: "POST /api/v1/payments/intake/email",
			py:    `{"payment_id":"p","confidence_score":"97.5000"}`,
			gov:   `{"payment_id":"p","confidence_score":"97.50"}`,
		},
		{
			name:       "PN 91 ignores confidence_score only: the amount is compared",
			route:      "POST /api/v1/payments/intake/webhook",
			py:         `{"payment_id":"p","confidence_score":"92.1889","amount":"1000.0000"}`,
			gov:        `{"payment_id":"p","confidence_score":"92.19","amount":"999.0000"}`,
			wantDiffer: 1,
		},
		{
			name:       "PN 91 is on the intake routes only",
			route:      "GET /api/v1/payments/p",
			py:         `{"payment_id":"p","confidence_score":"92.1889"}`,
			gov:        `{"payment_id":"p","confidence_score":"92.19"}`,
			wantDiffer: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			one(t, Diff(tc.route, jsonResponse(200, tc.py), jsonResponse(200, tc.gov), a), tc.wantDiffer)
		})
	}
}

// TestProdcopyOverlay pins allowed-diffs-prodcopy.yaml: only ignore entries,
// each on one of the three ledger reports with note PN 166, none a whole body;
// merged with the shipped file it hides exactly those paths, and the shipped
// file alone hides none of them.
func TestProdcopyOverlay(t *testing.T) {
	t.Parallel()
	overlay, err := LoadAllowedFile(prodcopyAllowed)
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}
	reports := map[string]bool{
		"GET /api/v1/accounting/reports/trial-balance":  true,
		"GET /api/v1/accounting/reports/balance-sheet":  true,
		"GET /api/v1/accounting/reports/fx-revaluation": true,
	}
	if len(overlay.Entries) != 8 {
		t.Fatalf("overlay has %d entries, want the 8 report paths", len(overlay.Entries))
	}
	for _, e := range overlay.Entries {
		if !reports[e.Route] || e.Rule != RuleIgnore || e.Note != "PN 166" || strings.Contains(e.Path, "[*]") {
			t.Errorf("overlay entry %+v: want an ignore of one report field, note PN 166", e)
		}
	}

	merged, overlaps, err := LoadAllowedFiles(shippedAllowed, prodcopyAllowed)
	if err != nil {
		t.Fatalf("LoadAllowedFiles: %v", err)
	}
	// The overlay's ignores knowingly override the shipped entries under the
	// same report fields (trial balance $.rows: unordered and two
	// decimal-equal; balance sheet $.groups and FX $.lines: one decimal-equal
	// each). The loader names each one; nothing else overlaps.
	if len(overlaps) != 5 {
		t.Fatalf("overlaps %q, want the 5 known report-field conflicts", overlaps)
	}
	for _, o := range overlaps {
		if !strings.Contains(o, "conflicts with") || !strings.Contains(o, "PN 166") {
			t.Errorf("overlap %q: want a PN 166 conflict with a shipped entry", o)
		}
	}
	shipped, err := LoadAllowedFile(shippedAllowed)
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}
	cases := []struct {
		name, route string
		py, gov     string
		// hidden is the differences the overlay drops; kept is what it still
		// reports.
		hidden, kept int
	}{
		{
			name:  "trial balance: an empty Python ledger against the back-post",
			route: "GET /api/v1/accounting/reports/trial-balance?as_of=2026-11-11",
			py:    `{"as_of":"2026-11-11","base_currency":"IDR","rows":[],"total_debit_base":"0","total_credit_base":"0"}`,
			gov: `{"as_of":"2026-11-11","base_currency":"IDR","rows":[{"code":"1000","debit":"5.0000"},{"code":"4000","credit":"5.0000"}],` +
				`"total_debit_base":"5.0000","total_credit_base":"5.0000"}`,
			hidden: 4,
		},
		{
			name:   "trial balance: the base currency is still compared",
			route:  "GET /api/v1/accounting/reports/trial-balance",
			py:     `{"base_currency":"IDR","rows":[],"total_debit_base":"0"}`,
			gov:    `{"base_currency":"SGD","rows":[],"total_debit_base":"5"}`,
			hidden: 1, kept: 1,
		},
		{
			name:   "balance sheet: groups, assets and income",
			route:  "GET /api/v1/accounting/reports/balance-sheet",
			py:     `{"groups":[],"assets_total_base":"0","income_total_base":"0","liabilities_total_base":"0"}`,
			gov:    `{"groups":[{"type":"ASSET"},{"type":"INCOME"}],"assets_total_base":"5","income_total_base":"5","liabilities_total_base":"0"}`,
			hidden: 4,
		},
		{
			name:   "balance sheet: the liabilities total is still compared",
			route:  "GET /api/v1/accounting/reports/balance-sheet",
			py:     `{"groups":[],"liabilities_total_base":"0"}`,
			gov:    `{"groups":[{"type":"ASSET"}],"liabilities_total_base":"7"}`,
			hidden: 1, kept: 1,
		},
		{
			name:   "fx revaluation: its lines and the unrealised gain",
			route:  "GET /api/v1/accounting/reports/fx-revaluation",
			py:     `{"lines":[],"total_unrealised_gain":"0","total_unrealised_loss":"0"}`,
			gov:    `{"lines":[{"code":"1000","currency":"SGD"}],"total_unrealised_gain":"142040.0000","total_unrealised_loss":"0"}`,
			hidden: 2,
		},
		{
			name:   "fx revaluation: the unrealised loss is still compared",
			route:  "GET /api/v1/accounting/reports/fx-revaluation",
			py:     `{"lines":[],"total_unrealised_gain":"0","total_unrealised_loss":"0"}`,
			gov:    `{"lines":[],"total_unrealised_gain":"1","total_unrealised_loss":"3"}`,
			hidden: 1, kept: 1,
		},
		{
			name:   "the overlay reaches no other report",
			route:  "GET /api/v1/accounting/reports/ar-aging",
			py:     `{"rows":[],"total":"0"}`,
			gov:    `{"rows":[{"customer_id":"c"}],"total":"0"}`,
			hidden: 0, kept: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			py, gov := jsonResponse(200, tc.py), jsonResponse(200, tc.gov)
			one(t, Diff(tc.route, py, gov, merged), tc.kept)
			// Without the overlay every one of those differences is reported,
			// so the shipped file alone never hides a ledger report.
			one(t, Diff(tc.route, py, gov, shipped), tc.kept+tc.hidden)
		})
	}
}

// TestCorpusClassifiesLedgerReports pins `classify: PN 166 (production-copy
// run only)` on the three ledger reports, and on no other case: it is also
// what turns their Go self-checks on.
func TestCorpusClassifiesLedgerReports(t *testing.T) {
	t.Parallel()
	corpus, err := LoadCorpusFile("../../port/compare-corpus.yaml")
	if err != nil {
		t.Fatalf("LoadCorpusFile: %v", err)
	}
	want := map[string]bool{"report-trial-balance": true, "report-balance-sheet": true, "report-fx-revaluation": true}
	got := map[string]bool{}
	for _, cs := range corpus.Cases {
		if strings.HasPrefix(cs.Classify, ledgerClassify) {
			if cs.Classify != "PN 166 (production-copy run only)" {
				t.Errorf("case %s classify %q: the label says the run it is for", cs.ID, cs.Classify)
			}
			got[cs.ID] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("PN 166 cases %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("case %s should carry classify: PN 166", id)
		}
	}
}

// TestManualExternalRefIsGenerated pins the external_ref mask in a step
// answer: two manual refs (manual- and 12 hex digits, random on each backend)
// compare equal, while a request's ref, or a manual ref against a request's
// ref, is still compared.
func TestManualExternalRefIsGenerated(t *testing.T) {
	t.Parallel()
	c := newRowComparer(Schema{}, IDMaps{})
	cases := []struct {
		name       string
		py, gov    string
		wantDiffer int
	}{
		{"two manual refs", `{"external_ref":"manual-e5ba37f3d7d0"}`, `{"external_ref":"manual-73d637a497cf"}`, 0},
		{"in a list", `[{"external_ref":"manual-e5ba37f3d7d0"}]`, `[{"external_ref":"manual-73d637a497cf"}]`, 0},
		{"a webhook ref", `{"external_ref":"compare-webhook"}`, `{"external_ref":"compare-webhook-2"}`, 1},
		{"a manual ref against a request's", `{"external_ref":"manual-e5ba37f3d7d0"}`, `{"external_ref":"compare-webhook"}`, 1},
		{"upper-case hex is not the shape", `{"external_ref":"manual-E5BA37F3D7D0"}`, `{"external_ref":"manual-73d637a497cf"}`, 1},
		{"13 digits is not the shape", `{"external_ref":"manual-e5ba37f3d7d01"}`, `{"external_ref":"manual-73d637a497cf"}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			py := Response{Status: 200, Body: c.normalizeBody([]byte(tc.py), true)}
			gov := Response{Status: 200, Body: c.normalizeBody([]byte(tc.gov), false)}
			one(t, Diff("POST /api/v1/invoices/i/payments", py, gov, Allowed{}), tc.wantDiffer)
		})
	}
}

// TestRowsManualExternalRefIsGenerated pins the same mask on the payments row:
// two manual refs compare equal, and a ref of another shape is reported.
func TestRowsManualExternalRefIsGenerated(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "payments")
	f.snapshot(t)
	for _, s := range []struct {
		pool *pgxpool.Pool
		id   string
		ref  string
	}{
		{f.py, pyPayment, "manual-e5ba37f3d7d0"},
		{f.gov, goPayment, "manual-73d637a497cf"},
	} {
		mustExec(t, s.pool, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status)
			VALUES ($1, 1000, 'IDR', 'P', '2026-01-02', 'MANUAL', $2, 'CLEARED')`, s.id, s.ref)
	}
	maps := mapsOf(t, "payments", pyPayment, goPayment)
	if diffs := f.compare(t, maps); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: a manual external_ref is generated", diffs)
	}

	mustExec(t, f.gov, `UPDATE payments SET external_ref = 'compare-webhook'`)
	diffs := f.compare(t, maps)
	if len(diffs) != 1 || diffs[0].Table != "payments" || !strings.Contains(diffs[0].Go, "external_ref=compare-webhook") {
		t.Fatalf("diffs %+v, want the external_ref difference", diffs)
	}
}

// TestProductionCopyMarker pins the marker `compare reads` keys the
// --ledger-matches-aging requirement on: the overlay has it, the shipped file
// does not.
func TestProductionCopyMarker(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{prodcopyAllowed: true, shippedAllowed: false} {
		got, err := IsProductionCopyFile(path)
		if err != nil {
			t.Fatalf("IsProductionCopyFile(%s): %v", path, err)
		}
		if got != want {
			t.Errorf("IsProductionCopyFile(%s) = %v, want %v", path, got, want)
		}
	}
}
