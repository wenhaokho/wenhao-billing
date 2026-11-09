package reports_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports"
)

const (
	tbPath = "/api/v1/accounting/reports/trial-balance"
	bsPath = "/api/v1/accounting/reports/balance-sheet"
)

// Seeded accounts (the baseline's chart) and one test-only EQUITY account.
const (
	bank, receivable, payable, income, cogs, expense, equity = 1, 2, 3, 6, 9, 13, 100
)

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Errors []struct {
		Location string `json:"location"`
	} `json:"errors"`
}

type tbRow struct {
	AccountID  int32  `json:"account_id"`
	Code       string `json:"code"`
	Type       string `json:"type"`
	Currency   string `json:"currency"`
	Debit      string `json:"debit"`
	Credit     string `json:"credit"`
	Net        string `json:"net"`
	BaseDebit  string `json:"base_debit"`
	BaseCredit string `json:"base_credit"`
	BaseNet    string `json:"base_net"`
}

type tbReport struct {
	AsOf            string  `json:"as_of"`
	BaseCurrency    string  `json:"base_currency"`
	Rows            []tbRow `json:"rows"`
	TotalDebitBase  string  `json:"total_debit_base"`
	TotalCreditBase string  `json:"total_credit_base"`
}

type bsLine struct {
	AccountID   int32  `json:"account_id"`
	Code        string `json:"code"`
	BaseBalance string `json:"base_balance"`
}

type bsGroup struct {
	Type      string   `json:"type"`
	TotalBase string   `json:"total_base"`
	Lines     []bsLine `json:"lines"`
}

type bsReport struct {
	AsOf                 string    `json:"as_of"`
	BaseCurrency         string    `json:"base_currency"`
	Groups               []bsGroup `json:"groups"`
	AssetsTotalBase      string    `json:"assets_total_base"`
	LiabilitiesTotalBase string    `json:"liabilities_total_base"`
	EquityTotalBase      string    `json:"equity_total_base"`
	IncomeTotalBase      string    `json:"income_total_base"`
	ExpenseTotalBase     string    `json:"expense_total_base"`
}

// line is one journal line: debit or credit in cur, and the base amount.
type line struct {
	account       int
	debit, credit string
	cur           string
	base          string // the base amount on the debit or credit side
}

func dr(account int, amount, cur, base string) line {
	return line{account: account, debit: amount, credit: "0", cur: cur, base: base}
}

func cr(account int, amount, cur, base string) line {
	return line{account: account, debit: "0", credit: amount, cur: cur, base: base}
}

func newApp(t *testing.T) *apitest.App {
	t.Helper()
	cfg := platform.Config{BaseCurrency: "IDR"}
	a := apitest.New(t, func(api huma.API, d apitest.Deps) { reports.Register(api, d.Pool, cfg, d.Clock) })
	exec(t, a, `INSERT INTO chart_of_accounts (account_id, code, name, type) VALUES (100, '3000', 'Owner Equity', 'EQUITY')`)
	return a
}

// post writes one journal entry posted at at, with lines.
func post(t *testing.T, a *apitest.App, at time.Time, lines ...line) {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO journal_entries (entry_id, posted_at, source_type, source_id) VALUES ($1, $2, 'TEST', $3)`, id, at, uuid.New())
	for _, l := range lines {
		baseDebit, baseCredit := "0", "0"
		if l.debit != "0" {
			baseDebit = l.base
		} else {
			baseCredit = l.base
		}
		exec(t, a, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency, base_amount_debit, base_amount_credit)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, uuid.New(), id, l.account, l.debit, l.credit, l.cur, baseDebit, baseCredit)
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }

func tb(t *testing.T, a *apitest.App, query string) tbReport {
	t.Helper()
	return apitest.Decode[tbReport](t, a.Do(t, http.MethodGet, tbPath+query, nil), http.StatusOK)
}

func bs(t *testing.T, a *apitest.App, query string) bsReport {
	t.Helper()
	return apitest.Decode[bsReport](t, a.Do(t, http.MethodGet, bsPath+query, nil), http.StatusOK)
}

func TestTrialBalanceEmpty(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	rec := a.Do(t, http.MethodGet, tbPath, nil)
	if !strings.Contains(rec.Body.String(), `"rows":[]`) {
		t.Errorf("rows is not []: %s", rec.Body)
	}
	got := apitest.Decode[tbReport](t, rec, http.StatusOK)
	want := tbReport{AsOf: "2026-01-15", BaseCurrency: "IDR", Rows: []tbRow{}, TotalDebitBase: "0.0000", TotalCreditBase: "0.0000"}
	if got.AsOf != want.AsOf || got.BaseCurrency != "IDR" || len(got.Rows) != 0 || got.TotalDebitBase != "0.0000" || got.TotalCreditBase != "0.0000" {
		t.Errorf("got %+v, want %+v", got, want)
	}
	bsEmpty := a.Do(t, http.MethodGet, bsPath, nil)
	if !strings.Contains(bsEmpty.Body.String(), `"groups":[]`) {
		t.Errorf("groups is not []: %s", bsEmpty.Body)
	}
	b := apitest.Decode[bsReport](t, bsEmpty, http.StatusOK)
	for _, v := range []string{b.AssetsTotalBase, b.LiabilitiesTotalBase, b.EquityTotalBase, b.IncomeTotalBase, b.ExpenseTotalBase} {
		if v != "0.0000" {
			t.Errorf("empty total %q, want 0.0000", v)
		}
	}
}

func TestTrialBalanceGroupsByAccountAndCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1600000"), cr(income, "100", "USD", "1600000"))
	post(t, a, day(2026, 1, 6), dr(bank, "500", "IDR", "500"), cr(income, "500", "IDR", "500"))
	post(t, a, day(2026, 1, 7), dr(bank, "50", "USD", "800000"), cr(income, "50", "USD", "800000"))

	got := tb(t, a, "")
	type want struct{ code, cur, debit, credit, net, baseNet string }
	wants := []want{
		{"1000", "IDR", "500.0000", "0.0000", "500.0000", "500.0000"},
		{"1000", "USD", "150.0000", "0.0000", "150.0000", "2400000.0000"},
		{"4000", "IDR", "0.0000", "500.0000", "-500.0000", "-500.0000"},
		{"4000", "USD", "0.0000", "150.0000", "-150.0000", "-2400000.0000"},
	}
	if len(got.Rows) != len(wants) {
		t.Fatalf("%d rows, want %d: %+v", len(got.Rows), len(wants), got.Rows)
	}
	for i, w := range wants {
		r := got.Rows[i]
		if r.Code != w.code || r.Currency != w.cur || r.Debit != w.debit || r.Credit != w.credit || r.Net != w.net || r.BaseNet != w.baseNet {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
	if got.Rows[1].Type != "ASSET" || got.Rows[3].Type != "INCOME" {
		t.Errorf("types %s %s", got.Rows[1].Type, got.Rows[3].Type)
	}
	if got.TotalDebitBase != "2400500.0000" || got.TotalCreditBase != "2400500.0000" {
		t.Errorf("totals %s / %s", got.TotalDebitBase, got.TotalCreditBase)
	}
}

func TestTrialBalanceCutoffInclusiveOfDay(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, time.Date(2026, 1, 10, 23, 59, 59, 999999000, time.UTC), dr(bank, "10", "IDR", "10"), cr(income, "10", "IDR", "10"))
	post(t, a, time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC), dr(bank, "1000", "IDR", "1000"), cr(income, "1000", "IDR", "1000"))
	got := tb(t, a, "?as_of=2026-01-10")
	if got.AsOf != "2026-01-10" {
		t.Errorf("as_of %s", got.AsOf)
	}
	if len(got.Rows) != 2 || got.Rows[0].Debit != "10.0000" || got.TotalDebitBase != "10.0000" {
		t.Errorf("as_of day: %+v total %s", got.Rows, got.TotalDebitBase)
	}
	next := tb(t, a, "?as_of=2026-01-11")
	if next.TotalDebitBase != "1010.0000" {
		t.Errorf("next day total %s, want 1010.0000", next.TotalDebitBase)
	}
	if before := tb(t, a, "?as_of=2026-01-09"); len(before.Rows) != 0 {
		t.Errorf("before: %+v", before.Rows)
	}
}

func TestTrialBalanceIncludesReversalPairs(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "IDR", "100"), cr(income, "100", "IDR", "100"))
	post(t, a, day(2026, 1, 6), cr(bank, "100", "IDR", "100"), dr(income, "100", "IDR", "100"))
	got := tb(t, a, "")
	if len(got.Rows) != 2 {
		t.Fatalf("rows %+v", got.Rows)
	}
	for _, r := range got.Rows {
		if r.Debit != "100.0000" || r.Credit != "100.0000" || r.Net != "0.0000" || r.BaseNet != "0.0000" {
			t.Errorf("row %+v does not net to 0", r)
		}
	}
	if got.TotalDebitBase != "200.0000" || got.TotalCreditBase != "200.0000" {
		t.Errorf("totals %s / %s", got.TotalDebitBase, got.TotalCreditBase)
	}
}

func TestTrialBalanceInactiveAccountIncluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "IDR", "100"), cr(income, "100", "IDR", "100"))
	exec(t, a, `UPDATE chart_of_accounts SET active = false WHERE account_id = $1`, bank)
	got := tb(t, a, "")
	if len(got.Rows) != 2 || got.Rows[0].Code != "1000" {
		t.Errorf("rows %+v", got.Rows)
	}
}

func TestBalanceSheetSignsPerType(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5),
		dr(bank, "1000", "IDR", "1000"), cr(payable, "300", "IDR", "300"), cr(income, "700", "IDR", "700"))
	post(t, a, day(2026, 1, 6),
		dr(cogs, "200", "IDR", "200"), dr(expense, "50", "IDR", "50"), cr(equity, "400", "IDR", "400"),
		cr(receivable, "250", "IDR", "250"))
	got := bs(t, a, "")
	byType := map[string]bsGroup{}
	for _, g := range got.Groups {
		byType[g.Type] = g
	}
	for typ, want := range map[string]string{
		"ASSET": "750.0000", "LIABILITY": "300.0000", "EQUITY": "400.0000",
		"INCOME": "700.0000", "EXPENSE": "50.0000", "COGS": "200.0000",
	} {
		if byType[typ].TotalBase != want {
			t.Errorf("%s total %q, want %s", typ, byType[typ].TotalBase, want)
		}
	}
	// A credit-heavy asset is negative: the asset sign is debit minus credit.
	asset := byType["ASSET"].Lines
	if len(asset) != 2 || asset[0].Code != "1000" || asset[0].BaseBalance != "1000.0000" || asset[1].Code != "1100" || asset[1].BaseBalance != "-250.0000" {
		t.Errorf("asset lines %+v", asset)
	}
	if got.AssetsTotalBase != "750.0000" || got.LiabilitiesTotalBase != "300.0000" || got.EquityTotalBase != "400.0000" ||
		got.IncomeTotalBase != "700.0000" || got.ExpenseTotalBase != "250.0000" {
		t.Errorf("totals %+v", got)
	}
}

func TestBalanceSheetKeepsOffsettingAccount(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(receivable, "100", "IDR", "100"), cr(income, "100", "IDR", "100"))
	post(t, a, day(2026, 1, 6), cr(receivable, "100", "IDR", "100"), dr(cogs, "100", "IDR", "100"))
	got := bs(t, a, "")
	if len(got.Groups) == 0 || got.Groups[0].Type != "ASSET" {
		t.Fatalf("groups %+v", got.Groups)
	}
	lines := got.Groups[0].Lines
	if len(lines) != 1 || lines[0].Code != "1100" || lines[0].BaseBalance != "0.0000" || got.Groups[0].TotalBase != "0.0000" {
		t.Errorf("offsetting account dropped or wrong: %+v", got.Groups[0])
	}
}

func TestBalanceSheetGroupOrder(t *testing.T) {
	t.Parallel()
	t.Run("all six", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		post(t, a, day(2026, 1, 5),
			dr(cogs, "1", "IDR", "1"), dr(expense, "1", "IDR", "1"), cr(income, "1", "IDR", "1"),
			cr(equity, "1", "IDR", "1"), cr(payable, "1", "IDR", "1"), dr(bank, "1", "IDR", "1"))
		var order []string
		for _, g := range bs(t, a, "").Groups {
			order = append(order, g.Type)
		}
		if got, want := strings.Join(order, ","), "ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"; got != want {
			t.Errorf("order %s, want %s", got, want)
		}
	})
	t.Run("only those present", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		post(t, a, day(2026, 1, 5), dr(bank, "5", "IDR", "5"), cr(income, "5", "IDR", "5"))
		var order []string
		for _, g := range bs(t, a, "").Groups {
			order = append(order, g.Type)
		}
		if got := strings.Join(order, ","); got != "ASSET,INCOME" {
			t.Errorf("order %s, want ASSET,INCOME", got)
		}
	})
}

func TestBalanceSheetHonoursAsOf(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "IDR", "100"), cr(income, "100", "IDR", "100"))
	post(t, a, day(2026, 1, 20), dr(bank, "50", "IDR", "50"), cr(income, "50", "IDR", "50"))
	for q, want := range map[string]string{
		"?as_of=2026-01-10": "100.0000",
		"":                  "100.0000", // today is 2026-01-15
		"?as_of=2026-01-31": "150.0000",
	} {
		if got := bs(t, a, q).AssetsTotalBase; got != want {
			t.Errorf("as_of %q: assets %s, want %s", q, got, want)
		}
	}
	if got := bs(t, a, "?as_of=2026-01-04"); len(got.Groups) != 0 {
		t.Errorf("before any posting: %+v", got.Groups)
	}
}

func TestReportsRequireAdmin(t *testing.T) {
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
	for _, path := range []string{tbPath, bsPath} {
		if p := apitest.Decode[problem](t, a.Anon(t, http.MethodGet, path, nil), http.StatusUnauthorized); p.Detail != "not authenticated" {
			t.Errorf("%s anon: %q", path, p.Detail)
		}
		if p := apitest.Decode[problem](t, staff.Do(t, http.MethodGet, path, nil), http.StatusForbidden); p.Detail != "admin required" {
			t.Errorf("%s staff: %q", path, p.Detail)
		}
	}
}

func TestAsOfDefaultsToUTCToday(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// 03:00 on the 10th in Jakarta is 20:00 on the 9th in UTC.
	jakarta := time.FixedZone("WIB", 7*3600)
	a.Clock.Advance(time.Date(2026, 3, 10, 3, 0, 0, 0, jakarta).Sub(a.Clock.Now()))
	token, _, err := a.Sessions.Issue(a.AdminID, 0, false) // the first session expired meanwhile
	if err != nil {
		t.Fatal(err)
	}
	a.Cookie = platform.SessionCookie + "=" + token
	post(t, a, time.Date(2026, 3, 9, 22, 0, 0, 0, time.UTC), dr(bank, "7", "IDR", "7"), cr(income, "7", "IDR", "7"))
	post(t, a, time.Date(2026, 3, 10, 0, 30, 0, 0, time.UTC), dr(bank, "900", "IDR", "900"), cr(income, "900", "IDR", "900"))
	got := tb(t, a, "")
	if got.AsOf != "2026-03-09" || got.TotalDebitBase != "7.0000" {
		t.Errorf("trial balance as_of %s total %s", got.AsOf, got.TotalDebitBase)
	}
	if b := bs(t, a, ""); b.AsOf != "2026-03-09" || b.AssetsTotalBase != "7.0000" {
		t.Errorf("balance sheet as_of %s assets %s", b.AsOf, b.AssetsTotalBase)
	}
}

func TestBadAsOfIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, path := range []string{tbPath, bsPath} {
		for _, q := range []string{"?as_of=2026-13-45", "?as_of=abc", "?as_of=2026-1-5"} {
			t.Run(path+q, func(t *testing.T) {
				t.Parallel()
				rec := a.Do(t, http.MethodGet, path+q, nil)
				p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
				found := false
				for _, e := range p.Errors {
					found = found || e.Location == "as_of"
				}
				if !found {
					t.Errorf("no error at as_of: %s", rec.Body)
				}
			})
		}
	}
}

func exec(t *testing.T, a *apitest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
