package compare

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// RuleSelfCheck tags a difference found in Go's answer alone: a ledger report
// that does not agree with itself, or with the aging reports. It is counted
// like any other difference, so it fails the run.
const RuleSelfCheck = "self-check"

// ledgerClassify is the classify prefix of the ledger report cases (PN 166):
// on the production copy their Go answers cannot be compared with Python's, so
// Go's are checked against themselves instead.
const ledgerClassify = "PN 166"

// The ledger and aging report routes the self-checks read.
const (
	trialBalanceRoute  = "/api/v1/accounting/reports/trial-balance"
	balanceSheetRoute  = "/api/v1/accounting/reports/balance-sheet"
	fxRevaluationRoute = "/api/v1/accounting/reports/fx-revaluation"
	arAgingRoute       = "/api/v1/accounting/reports/ar-aging"
	apAgingRoute       = "/api/v1/accounting/reports/ap-aging"
)

// The accounts the aging cross-check reads in the trial balance.
const (
	receivableCode = "1100"
	payableCode    = "2000"
)

// selfCheckDiff is one failed check: Python holds what the check expected and
// Go what Go's answer has.
func selfCheckDiff(path, expected string, got decimal.Decimal) Difference {
	return Difference{Path: path, Python: "self-check: " + expected, Go: got.String(), Rule: RuleSelfCheck}
}

// selfCheck checks one ledger report's Go answer against itself, by route:
// the trial balance's totals balance and are its rows' sums; each balance
// sheet group's total is its lines' sum and assets = liabilities + equity +
// income − expense; the FX revaluation's totals are its lines' gains and
// losses. Any other route has no check. A Go answer that is not 200 JSON
// fails the check.
func selfCheck(routePath string, gov Response) []Difference {
	var check func(map[string]any) []Difference
	switch routePath {
	case trialBalanceRoute:
		check = checkTrialBalance
	case balanceSheetRoute:
		check = checkBalanceSheet
	case fxRevaluationRoute:
		check = checkFxRevaluation
	default:
		return nil
	}
	body, ok := goObject(gov)
	if !ok {
		return []Difference{{Path: "$", Python: "self-check: a 200 JSON object", Go: fmt.Sprintf("status %d", gov.Status), Rule: RuleSelfCheck}}
	}
	return check(body)
}

// goObject decodes a 200 answer whose body is a JSON object.
func goObject(r Response) (map[string]any, bool) {
	if r.Status != 200 {
		return nil, false
	}
	v, err := decodeBody(r.Body)
	if err != nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

// decField reads a decimal field (a JSON string or number); a missing or null
// field is 0, anything else unreadable is reported by ok = false.
func decField(obj map[string]any, key string) (decimal.Decimal, bool) {
	v, present := obj[key]
	if !present || v == nil {
		return decimal.Zero, true
	}
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case json.Number:
		s = x.String()
	default:
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(s)
	return d, err == nil
}

// objects is the JSON objects of an array field.
func objects(obj map[string]any, key string) []map[string]any {
	arr, _ := obj[key].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, v := range arr {
		if o, ok := v.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

// checker gathers failed checks; a field that is not a decimal fails too.
type checker struct{ diffs []Difference }

// get reads a decimal field, recording an unreadable one at path.
func (c *checker) get(obj map[string]any, key, path string) decimal.Decimal {
	d, ok := decField(obj, key)
	if !ok {
		c.diffs = append(c.diffs, Difference{Path: path, Python: "self-check: a decimal", Go: text(obj[key]), Rule: RuleSelfCheck})
	}
	return d
}

// equal records a failed check when got is not want.
func (c *checker) equal(path string, got, want decimal.Decimal, what string) {
	if !got.Equal(want) {
		c.diffs = append(c.diffs, selfCheckDiff(path, fmt.Sprintf("%s = %s", what, want.String()), got))
	}
}

// checkTrialBalance: total debit = total credit, and each total is the sum of
// the rows' base column.
func checkTrialBalance(tb map[string]any) []Difference {
	c := &checker{}
	var debit, credit decimal.Decimal
	for i, r := range objects(tb, "rows") {
		debit = debit.Add(c.get(r, "base_debit", fmt.Sprintf("$.rows[%d].base_debit", i)))
		credit = credit.Add(c.get(r, "base_credit", fmt.Sprintf("$.rows[%d].base_credit", i)))
	}
	totalDebit := c.get(tb, "total_debit_base", "$.total_debit_base")
	totalCredit := c.get(tb, "total_credit_base", "$.total_credit_base")
	c.equal("$.total_debit_base", totalDebit, totalCredit, "total_credit_base (the ledger balances)")
	c.equal("$.total_debit_base", totalDebit, debit, "the sum of rows[*].base_debit")
	c.equal("$.total_credit_base", totalCredit, credit, "the sum of rows[*].base_credit")
	return c.diffs
}

// checkBalanceSheet: each group's total is its lines' sum, each typed total is
// its groups' totals, and assets = liabilities + equity + income − expense
// (expense with COGS), which holds whenever the ledger balances.
func checkBalanceSheet(bs map[string]any) []Difference {
	c := &checker{}
	byType := map[string]decimal.Decimal{}
	for i, g := range objects(bs, "groups") {
		var sum decimal.Decimal
		for j, l := range objects(g, "lines") {
			sum = sum.Add(c.get(l, "base_balance", fmt.Sprintf("$.groups[%d].lines[%d].base_balance", i, j)))
		}
		path := fmt.Sprintf("$.groups[%d].total_base", i)
		total := c.get(g, "total_base", path)
		c.equal(path, total, sum, "the sum of its lines' base_balance")
		t, _ := g["type"].(string)
		byType[t] = byType[t].Add(total)
	}
	assets := c.get(bs, "assets_total_base", "$.assets_total_base")
	liabilities := c.get(bs, "liabilities_total_base", "$.liabilities_total_base")
	equity := c.get(bs, "equity_total_base", "$.equity_total_base")
	income := c.get(bs, "income_total_base", "$.income_total_base")
	expense := c.get(bs, "expense_total_base", "$.expense_total_base")
	c.equal("$.assets_total_base", assets, byType["ASSET"], "the ASSET group total")
	c.equal("$.liabilities_total_base", liabilities, byType["LIABILITY"], "the LIABILITY group total")
	c.equal("$.equity_total_base", equity, byType["EQUITY"], "the EQUITY group total")
	c.equal("$.income_total_base", income, byType["INCOME"], "the INCOME group total")
	c.equal("$.expense_total_base", expense, byType["EXPENSE"].Add(byType["COGS"]), "the EXPENSE and COGS group totals")
	c.equal("$.assets_total_base", assets, liabilities.Add(equity).Add(income).Sub(expense),
		"liabilities + equity + income - expense")
	return c.diffs
}

// checkFxRevaluation: the unrealised gain is the sum of the lines' positive
// unrealised_gain_loss, and the loss the sum of the negative ones, as a
// positive figure. A line with a missing rate has none.
func checkFxRevaluation(fx map[string]any) []Difference {
	c := &checker{}
	var gain, loss decimal.Decimal
	for i, l := range objects(fx, "lines") {
		u := c.get(l, "unrealised_gain_loss", fmt.Sprintf("$.lines[%d].unrealised_gain_loss", i))
		if u.IsPositive() {
			gain = gain.Add(u)
		} else {
			loss = loss.Add(u.Neg())
		}
	}
	c.equal("$.total_unrealised_gain", c.get(fx, "total_unrealised_gain", "$.total_unrealised_gain"), gain,
		"the sum of the positive lines[*].unrealised_gain_loss")
	c.equal("$.total_unrealised_loss", c.get(fx, "total_unrealised_loss", "$.total_unrealised_loss"), loss,
		"the sum of the negative lines[*].unrealised_gain_loss")
	return c.diffs
}

// agingCheck cross-checks the trial balance against the aging reports, which
// read the documents and are compared with Python in full: per currency, the
// receivable 1100's net (debit − credit) is the AR aging total, and the
// payable 2000's credit balance (credit − debit) the AP aging total. Both
// reports must be of the same as_of. A missing answer fails the check, so it
// is never skipped silently. An overpaid invoice (a customer advance, PN 166)
// leaves 1100 below its aging total and fails the check: review it by hand.
func agingCheck(tb, ar, ap Response, haveAR, haveAP bool) []Difference {
	tbObj, ok := goObject(tb)
	if !ok {
		return []Difference{{Path: "$", Python: "self-check: a 200 trial balance", Go: fmt.Sprintf("status %d", tb.Status), Rule: RuleSelfCheck}}
	}
	c := &checker{}
	net := map[string]map[string]decimal.Decimal{receivableCode: {}, payableCode: {}}
	for i, r := range objects(tbObj, "rows") {
		code, _ := r["code"].(string)
		if _, ok := net[code]; !ok {
			continue
		}
		cur, _ := r["currency"].(string)
		net[code][cur] = net[code][cur].Add(c.get(r, "net", fmt.Sprintf("$.rows[%d].net", i)))
	}
	for _, side := range []struct {
		name, code string
		resp       Response
		have       bool
		sign       int64
	}{
		{"ar-aging", receivableCode, ar, haveAR, 1},
		{"ap-aging", payableCode, ap, haveAP, -1},
	} {
		agingObj, ok := goObject(side.resp)
		if !side.have || !ok {
			c.diffs = append(c.diffs, Difference{Path: "$.rows", Python: "self-check: a 200 " + side.name + " answer in the run", Go: missing, Rule: RuleSelfCheck})
			continue
		}
		if text(agingObj["as_of"]) != text(tbObj["as_of"]) {
			c.diffs = append(c.diffs, Difference{Path: "$.as_of", Python: "self-check: " + side.name + " as_of " + text(agingObj["as_of"]), Go: text(tbObj["as_of"]), Rule: RuleSelfCheck})
			continue
		}
		totals, _ := agingObj["totals_by_currency"].(map[string]any)
		currencies := map[string]bool{}
		for cur := range totals {
			currencies[cur] = true
		}
		for cur := range net[side.code] {
			currencies[cur] = true
		}
		for _, cur := range sortedKeys(currencies) {
			var want decimal.Decimal
			if b, ok := totals[cur].(map[string]any); ok {
				want = c.get(b, "total", side.name+" totals_by_currency."+cur+".total")
			}
			got := net[side.code][cur].Mul(decimal.NewFromInt(side.sign))
			c.equal(fmt.Sprintf("$.rows[code=%s,currency=%s]", side.code, cur), got, want,
				side.name+" totals_by_currency."+cur+".total")
		}
	}
	return c.diffs
}
