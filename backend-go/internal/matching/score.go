package matching

import (
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Payment struct {
	Amount              decimal.Decimal
	Currency, PayerName string
	Date                time.Time
}
type Candidate struct {
	ID, CustomerName                   string
	Aliases                            []string
	Currency                           string
	BalanceDue, WithholdingOutstanding decimal.Decimal
	IssueDate, DueDate                 time.Time
	Status                             string
}
type Status string

const (
	Cleared             Status = "CLEARED"
	PendingManualReview Status = "PENDING_MANUAL_REVIEW"
)

type Result struct {
	CandidateID                                 string
	Confidence                                  decimal.Decimal
	Status                                      Status
	AmountMismatch, CurrencyMismatch, Ambiguous bool
	Reasons                                     []string
}

// Match validates its inputs and applies the reconciliation safe-stop rules.
// External intake must also limit request body and numeric token lengths before
// parsing decimals: Match receives already-allocated values and cannot prevent
// oversized request or mantissa allocations. A fixed-point intake can limit
// money tokens to 15 integer and 4 fractional digits (plus sign and decimal
// point), reject exponent notation, and enforce an appropriate request body cap.
func Match(p Payment, cs []Candidate) Result {
	r := Result{Status: PendingManualReview}
	if !currencyCode(p.Currency) || !utf8.ValidString(p.PayerName) || pythonTrim(p.PayerName) == "" || p.Date.IsZero() || (!validMoney(p.Amount) || !p.Amount.IsPositive()) {
		r.Reasons = []string{"invalid payment"}
		return r
	}
	var best Candidate
	found := false
	seen := make(map[string]Candidate)
	exactIDs := 0
	for _, c := range cs {
		if c.Status != "OPEN" && c.Status != "SENT" && c.Status != "PARTIAL" {
			continue
		}
		if c.ID == "" || !utf8.ValidString(c.CustomerName) || pythonTrim(c.CustomerName) == "" || !currencyCode(c.Currency) || c.IssueDate.IsZero() || c.DueDate.IsZero() || !validMoney(c.BalanceDue) || !validMoney(c.WithholdingOutstanding) || c.BalanceDue.IsNegative() || c.WithholdingOutstanding.IsNegative() {
			return Result{Status: PendingManualReview, Reasons: []string{"invalid candidate"}}
		}
		for _, alias := range c.Aliases {
			if !utf8.ValidString(alias) {
				return Result{Status: PendingManualReview, Reasons: []string{"invalid candidate"}}
			}
		}
		if previous, ok := seen[c.ID]; ok {
			if !reflect.DeepEqual(previous, c) {
				return Result{Status: PendingManualReview, Reasons: []string{"conflicting duplicate candidate"}}
			}
			continue
		}
		seen[c.ID] = c
		expected := withholding.ExpectedCash(c.BalanceDue, c.WithholdingOutstanding, decimal.Zero)
		if strings.EqualFold(p.Currency, c.Currency) && p.Amount.Sub(expected).Abs().LessThanOrEqual(amountEpsilon) {
			exactIDs++
		}
		s := Score(p, c)
		if !found || s.GreaterThan(r.Confidence) {
			best = c
			r.Confidence = s
			r.Ambiguous = false
			found = true
		} else if s.Equal(r.Confidence) {
			r.Ambiguous = true
		}
	}
	if !found {
		r.Reasons = []string{"no open invoice"}
		return r
	}
	if r.Ambiguous || exactIDs > 1 {
		r.Ambiguous = true
		r.Reasons = []string{"ambiguous candidates"}
		return r
	}
	r.CandidateID = best.ID
	expected := withholding.ExpectedCash(best.BalanceDue, best.WithholdingOutstanding, decimal.Zero)
	r.AmountMismatch = !p.Amount.Equal(expected)
	r.CurrencyMismatch = !strings.EqualFold(p.Currency, best.Currency)
	if r.AmountMismatch {
		r.Reasons = append(r.Reasons, "amount mismatch")
	}
	if r.CurrencyMismatch {
		r.Reasons = append(r.Reasons, "currency mismatch")
	}
	if r.Confidence.LessThan(decimal.NewFromInt(95)) {
		r.Reasons = append(r.Reasons, "confidence below threshold")
	}
	if len(r.Reasons) == 0 {
		r.Status = Cleared
		r.Reasons = []string{"exact amount and currency, high payer and date confidence"}
	}
	return r
}

// Score computes the Python-compatible weighted score. Callers must validate
// names, dates, currencies and bound decimal coefficients/exponents first; use
// Match for untrusted candidates and reconciliation eligibility decisions.
func Score(p Payment, c Candidate) decimal.Decimal {
	s := decimal.Zero
	expected := withholding.ExpectedCash(c.BalanceDue, c.WithholdingOutstanding, decimal.Zero)
	if p.Amount.Sub(expected).Abs().LessThanOrEqual(amountEpsilon) {
		s = s.Add(decimal.NewFromInt(50))
	}
	if currencyCode(p.Currency) && currencyCode(c.Currency) && strings.EqualFold(p.Currency, c.Currency) {
		s = s.Add(decimal.NewFromInt(20))
	}
	best := decimal.Zero
	for _, n := range append([]string{c.CustomerName}, c.Aliases...) {
		v := similarity(p.PayerName, n)
		if v.GreaterThan(best) {
			best = v
		}
	}
	s = s.Add(best.Mul(decimal.NewFromInt(20)))
	a, b := daysApart(p.Date, c.IssueDate), daysApart(p.Date, c.DueDate)
	if b < a {
		a = b
	}
	if a < 14 {
		s = s.Add(decimal.NewFromInt(int64(14-a)).Mul(decimal.NewFromInt(10)).DivRound(decimal.NewFromInt(14), 16))
	}
	return s.RoundBank(4)
}
func daysApart(a, b time.Time) int {
	a = time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	b = time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	v := int(a.Sub(b).Hours() / 24)
	if v < 0 {
		return -v
	}
	return v
}
func similarity(a, b string) decimal.Decimal {
	lower := cases.Lower(language.Und)
	x := []rune(lower.String(pythonTrim(a)))
	y := []rune(lower.String(pythonTrim(b)))
	if len(x)+len(y) == 0 {
		return decimal.NewFromInt(1)
	}
	pos := map[rune][]int{}
	for i, c := range y {
		pos[c] = append(pos[c], i)
	}
	if len(y) >= 200 {
		for c, p := range pos {
			if len(p) > len(y)/100+1 {
				delete(pos, c)
			}
		}
	}
	type bounds struct{ al, ah, bl, bh int }
	stack := []bounds{{0, len(x), 0, len(y)}}
	total := 0
	for len(stack) > 0 {
		z := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ai, bi, size := z.al, z.bl, 0
		prev := map[int]int{}
		for i := z.al; i < z.ah; i++ {
			curr := map[int]int{}
			for _, j := range pos[x[i]] {
				if j < z.bl {
					continue
				}
				if j >= z.bh {
					break
				}
				k := prev[j-1] + 1
				curr[j] = k
				if k > size {
					ai = i - k + 1
					bi = j - k + 1
					size = k
				}
			}
			prev = curr
		}
		for ai > z.al && bi > z.bl && x[ai-1] == y[bi-1] {
			ai--
			bi--
			size++
		}
		for ai+size < z.ah && bi+size < z.bh && x[ai+size] == y[bi+size] {
			size++
		}
		if size > 0 {
			total += size
			if z.al < ai && z.bl < bi {
				stack = append(stack, bounds{z.al, ai, z.bl, bi})
			}
			if ai+size < z.ah && bi+size < z.bh {
				stack = append(stack, bounds{ai + size, z.ah, bi + size, z.bh})
			}
		}
	}
	return decimal.NewFromInt(int64(2*total)).DivRound(decimal.NewFromInt(int64(len(x)+len(y))), 16)
}

// validMoney enforces the ledger DECIMAL(19,4) boundary without rounding.
func validMoney(v decimal.Decimal) bool {
	if v.Exponent() < -19 || v.Exponent() > 15 || v.Coefficient().BitLen() > 128 {
		return false
	}
	return v.Equal(v.Round(4)) && v.Abs().LessThanOrEqual(decimal.RequireFromString("999999999999999.9999"))
}
func pythonTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) })
}
