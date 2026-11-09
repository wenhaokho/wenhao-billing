package matching

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// Payment statuses and adjustment types the engine returns.
const (
	StatusCleared = "CLEARED"
	StatusPending = "PENDING_MANUAL_REVIEW"
	StatusFlagged = "FLAGGED"

	AdjNone            = "NONE"
	AdjPartialPay      = "PARTIAL_PAY"
	AdjCreditOnAccount = "CREDIT_ON_ACCOUNT" //nolint:gosec // an adjustment type, not a credential
	AdjBankFee         = "BANK_FEE"
)

// DateWindowDays is the span over which date proximity decays to zero.
const DateWindowDays = 14

var (
	// AutoClearThreshold is the lowest confidence that may auto-clear. It keeps
	// Python's "95.0" scale because the reason sentence prints it.
	AutoClearThreshold = decimal.RequireFromString("95.0")
	// AmountEpsilon is the DECIMAL(19,4) floor: amounts closer than this agree.
	AmountEpsilon = decimal.RequireFromString("0.0001")

	weightAmount   = decimal.NewFromInt(50)
	weightCurrency = decimal.NewFromInt(20)
	weightPayer    = decimal.NewFromInt(20)
	weightDate     = decimal.NewFromInt(10)

	lowerCase = cases.Lower(language.Und)
)

// Incoming is a payment to match. PayerReference is carried but not scored.
type Incoming struct {
	Amount         decimal.Decimal
	Currency       string
	PayerName      string
	PayerReference *string
	PaymentDate    time.Time
}

// Candidate is an invoice a payment may settle. WhtOutstanding is the
// withholding the customer will deduct and no payment has booked yet. The
// caller filters by currency; Match keeps only OPEN, SENT and PARTIAL.
type Candidate struct {
	InvoiceID, CustomerID uuid.UUID
	Currency              string
	Status                string
	BalanceDue            decimal.Decimal
	WhtOutstanding        decimal.Decimal
	IssueDate, DueDate    *time.Time
	CustomerName          string
	Aliases               []string
}

// Result is the engine's verdict. Status is the value to store; only the
// caller posts to the ledger, and only for StatusCleared.
type Result struct {
	Status, AdjustmentType           string
	Invoice                          *Candidate
	Confidence                       decimal.Decimal
	CurrencyMismatch, AmountMismatch bool
	Reasons                          []string
}

func (c Candidate) expectedCash() decimal.Decimal {
	return withholding.ExpectedCash(c.BalanceDue, c.WhtOutstanding, decimal.Zero)
}

func sameCurrency(a, b string) bool { return platform.PyUpper(a) == platform.PyUpper(b) }

func amountAgrees(p Incoming, c Candidate) bool {
	return p.Amount.Sub(c.expectedCash()).Abs().LessThanOrEqual(AmountEpsilon)
}

// IsExactMatch reports whether the payment agrees with the invoice on
// currency and, within AmountEpsilon, on the cash expected net of unbooked
// withholding. It is the one condition under which a payment may auto-clear.
func IsExactMatch(p Incoming, c Candidate) bool {
	return sameCurrency(p.Currency, c.Currency) && amountAgrees(p, c)
}

// pyStrip is Python's str.strip(): whitespace is str.isspace, which is Go's
// unicode.IsSpace plus the four information separators U+001C to U+001F.
func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) })
}

// normalize is Python's s.lower().strip(): lower first, then strip. The
// x/text lower-casing follows Unicode's full mapping, as str.lower does
// (final sigma, U+0130).
func normalize(s string) string { return pyStrip(lowerCase.String(s)) }

// payerSimilarity is the best difflib ratio over the customer name and its
// aliases, as the shortest-repr decimal Python's Decimal(str(float)) makes.
func payerSimilarity(payer string, c Candidate) decimal.Decimal {
	p := normalize(payer)
	best := Ratio(p, normalize(c.CustomerName))
	for _, alias := range c.Aliases {
		if r := Ratio(p, normalize(alias)); r > best {
			best = r
		}
	}
	return decimal.RequireFromString(platform.PyFloatRepr(best))
}

// dayDiff is the absolute difference in calendar days.
func dayDiff(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	d := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC).Sub(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC))
	if d < 0 {
		d = -d
	}
	return int(d.Hours() / 24)
}

// dateProximity is 1 at the issue or due date, falling linearly to 0 at
// DateWindowDays, with Python's 28-significant-digit division. A candidate
// with no issue or due date scores 0 (Python crashed).
func dateProximity(pay time.Time, c Candidate) decimal.Decimal {
	if c.IssueDate == nil || c.DueDate == nil {
		return decimal.Zero
	}
	anchor := min(dayDiff(pay, *c.IssueDate), dayDiff(pay, *c.DueDate))
	if anchor >= DateWindowDays {
		return decimal.Zero
	}
	q := decimal.NewFromInt(int64(DateWindowDays-anchor)).DivRound(decimal.NewFromInt(DateWindowDays), 60)
	places := int32(28)
	if q.Sign() > 0 && q.LessThan(decimal.NewFromInt(1)) {
		// 28 significant digits: the leading zeros after the point do not count.
		places += int32(-(q.Exponent() + int32(len(q.Coefficient().String())))) //nolint:gosec // small
	}
	return q.RoundBank(places)
}

// Score is the confidence, 0 to 100 at four places (half-even): amount 50,
// currency 20, payer 20 times the name ratio, date 10 times proximity.
func Score(p Incoming, c Candidate) decimal.Decimal {
	total := decimal.Zero
	if amountAgrees(p, c) {
		total = total.Add(weightAmount)
	}
	if sameCurrency(p.Currency, c.Currency) {
		total = total.Add(weightCurrency)
	}
	total = total.Add(payerSimilarity(p.PayerName, c).Mul(weightPayer))
	total = total.Add(dateProximity(p.PaymentDate, c).Mul(weightDate))
	return platform.RoundHalfEven4(total)
}

type scored struct {
	score decimal.Decimal
	c     Candidate
}

// before orders by score descending, then due date ascending (none last),
// then invoice id, so the attached candidate never depends on query order.
func (a scored) before(b scored) bool {
	if cmp := a.score.Cmp(b.score); cmp != 0 {
		return cmp > 0
	}
	switch ad, bd := a.c.DueDate, b.c.DueDate; {
	case ad == nil && bd != nil:
		return false
	case ad != nil && bd == nil:
		return true
	case ad != nil && bd != nil && !ad.Equal(*bd):
		return ad.Before(*bd)
	}
	return bytes.Compare(a.c.InvoiceID[:], b.c.InvoiceID[:]) < 0
}

// Match scores the open candidates and applies the safe-stop gate: anything
// but one exact, high-confidence candidate is held for manual review.
func Match(p Incoming, cands []Candidate) Result {
	var open []scored
	for _, c := range cands {
		switch c.Status {
		case "OPEN", "SENT", "PARTIAL":
			open = append(open, scored{Score(p, c), c})
		}
	}
	if len(open) == 0 {
		return Result{
			Status:         StatusPending,
			AdjustmentType: AdjCreditOnAccount,
			Confidence:     decimal.Zero,
			AmountMismatch: true,
			Reasons:        []string{"no open invoice matches payer/currency"},
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].before(open[j]) })
	best := open[0].c
	confidence := open[0].score

	expected := best.expectedCash()
	amountMismatch := p.Amount.Sub(expected).Abs().GreaterThan(AmountEpsilon)
	currencyMismatch := !sameCurrency(p.Currency, best.Currency)

	var reasons []string
	if currencyMismatch {
		reasons = append(reasons, fmt.Sprintf("currency mismatch: payment %s vs invoice %s", p.Currency, best.Currency))
	}
	if amountMismatch {
		reasons = append(reasons, "amount mismatch: delta="+platform.PyDecimalStr(p.Amount.Sub(expected)))
	}
	belowThreshold := confidence.LessThan(AutoClearThreshold)
	if belowThreshold {
		reasons = append(reasons, fmt.Sprintf("confidence %s below threshold %s",
			platform.PyDecimalStr(confidence), platform.PyDecimalStr(AutoClearThreshold)))
	}

	// More than one candidate that would clear is ambiguous: a human picks.
	clearing := 0
	for _, s := range open {
		if IsExactMatch(p, s.c) && !s.score.LessThan(AutoClearThreshold) {
			clearing++
		}
	}
	ambiguous := clearing > 1
	if ambiguous {
		reasons = append(reasons, fmt.Sprintf("ambiguous match: %d open invoices clear equally", clearing))
	}

	if !IsExactMatch(p, best) || belowThreshold || ambiguous {
		adjustment := AdjNone
		switch {
		case currencyMismatch:
		case p.Amount.LessThan(expected):
			adjustment = AdjPartialPay
		case p.Amount.GreaterThan(expected):
			adjustment = AdjCreditOnAccount
		}
		return Result{
			Status:           StatusPending,
			AdjustmentType:   adjustment,
			Invoice:          &best,
			Confidence:       confidence,
			CurrencyMismatch: currencyMismatch,
			AmountMismatch:   amountMismatch,
			Reasons:          reasons,
		}
	}
	return Result{
		Status:         StatusCleared,
		AdjustmentType: AdjNone,
		Invoice:        &best,
		Confidence:     confidence,
		Reasons:        []string{"exact amount+currency, high payer+date confidence"},
	}
}
