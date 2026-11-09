package invoices

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// Totals are an invoice's computed amounts. Amount is the total the
// customer is billed (WHT.Total: the after-discount base, grossed up when
// asked); it is also the new balance_due.
type Totals struct {
	Subtotal, Amount decimal.Decimal
	WHT              withholding.Result
}

// LineAmount is a line's stored amount: qty × price, rounded half-up to
// four places (invoicing.py:174-189).
func LineAmount(qty, price decimal.Decimal) decimal.Decimal {
	return platform.RoundHalfUp4(qty.Mul(price))
}

// ComputeTotals is Python's _compute_totals (invoicing.py:47-73). The
// subtotal rounds the raw sum of qty × price once (not the sum of the
// rounded lines). A discount applies only when both its type (PERCENT or
// AMOUNT) and value are given, and the discounted amount never goes below
// zero. Only the withholding path rounds to the currency's precision: a
// total without withholding keeps four places.
func ComputeTotals(lines []LineIn, discountType *string, discountValue *decimal.Decimal, currency, whtType string, whtRate *decimal.Decimal, grossUp bool) Totals {
	sum := decimal.Zero
	for _, ln := range lines {
		sum = sum.Add(ln.Quantity.Mul(ln.UnitPrice))
	}
	subtotal := platform.RoundHalfUp4(sum)

	amount := subtotal
	if discountType != nil && discountValue != nil {
		switch *discountType {
		case "PERCENT":
			// value/100 is exact: Shift moves the decimal point.
			amount = platform.RoundHalfUp4(subtotal.Mul(decimal.NewFromInt(1).Sub(discountValue.Shift(-2))))
		case "AMOUNT":
			amount = platform.RoundHalfUp4(subtotal.Sub(*discountValue))
		}
	}
	if amount.IsNegative() {
		amount = decimal.Zero
	}
	wht := withholding.Compute(amount, currency, whtType, whtRate, grossUp)
	return Totals{Subtotal: subtotal, Amount: wht.Total, WHT: wht}
}

// netTerms is Python's r"^\s*net\s+(\d+)\s*$" (re.I), applied after strip.
// Go's \d and \s are ASCII, Python's Unicode: "Net ٣٠" is 30 days in
// Python and the 14-day default here.
var netTerms = regexp.MustCompile(`(?i)^\s*net\s+(\d+)\s*$`)

// PaymentTermsDays is the days from the issue date to the due date that
// payment terms give (invoicing.py:145-154): 0 for "on receipt" (and its
// two spellings), N for "Net N", 14 for anything else, including none.
// ok is false when N does not fit an int (payment_terms is varchar(30));
// Python's date arithmetic failed there (a 500), the callers refuse it.
func PaymentTermsDays(terms *string) (days int, ok bool) {
	if terms == nil || *terms == "" {
		return 14, true
	}
	t := strings.TrimSpace(*terms)
	switch strings.ToLower(t) {
	case "on receipt", "on-receipt", "due on receipt":
		return 0, true
	}
	if m := netTerms.FindStringSubmatch(t); m != nil {
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	return 14, true
}

// Python's date range: date.min and date.max.
var (
	minDate = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maxDate = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
)

// errDueOutOfRange refuses a due date outside Python's date range, where
// Python's date arithmetic raised (a 500).
func errDueOutOfRange() error { return refuse("due_date_out_of_range", "due date is out of range") }

// addDays is day + days, refused when it leaves 0001-01-01..9999-12-31.
func addDays(day time.Time, days int) (time.Time, error) {
	// Whole days from Unix seconds: time.Duration saturates at about 292
	// years, so Sub cannot measure the distance to either end. Comparing in
	// days also keeps huge values away from time arithmetic.
	const secondsPerDay = 24 * 60 * 60
	up := (maxDate.Unix() - day.Unix()) / secondsPerDay
	down := (day.Unix() - minDate.Unix()) / secondsPerDay
	if int64(days) > up || int64(days) < -down {
		return time.Time{}, errDueOutOfRange()
	}
	return day.AddDate(0, 0, days), nil
}

// dueAfterTerms is the due date payment terms give an issue date
// (invoicing.py:247-249, :335-338), refused when out of range.
func dueAfterTerms(issue time.Time, terms *string) (time.Time, error) {
	days, ok := PaymentTermsDays(terms)
	if !ok {
		return time.Time{}, errDueOutOfRange()
	}
	return addDays(issue, days)
}
