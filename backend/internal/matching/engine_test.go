package matching

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func incoming(mod func(*Incoming)) Incoming {
	p := Incoming{
		Amount:      dec("1000.0000"),
		Currency:    "USD",
		PayerName:   "Acme Corp",
		PaymentDate: *day(2026, 4, 15),
	}
	if mod != nil {
		mod(&p)
	}
	return p
}

func candidate(mod func(*Candidate)) Candidate {
	c := Candidate{
		InvoiceID:    uuid.New(),
		CustomerID:   uuid.New(),
		CustomerName: "Acme Corp",
		Currency:     "USD",
		BalanceDue:   dec("1000.0000"),
		IssueDate:    day(2026, 4, 10),
		DueDate:      day(2026, 4, 24),
		Status:       "SENT",
	}
	if mod != nil {
		mod(&c)
	}
	return c
}

func match(p Incoming, cs ...Candidate) Result { return Match(p, cs) }

func wantConfidenceAtLeast95(t *testing.T, r Result) {
	t.Helper()
	if r.Confidence.Cmp(dec("95")) < 0 {
		t.Fatalf("confidence %s, want >= 95", r.Confidence)
	}
}

// The twelve tests of tests/unit/test_matching_engine.py, with Python's values.

func TestExactMatchClears(t *testing.T) {
	t.Parallel()
	r := match(incoming(nil), candidate(nil))
	if r.Status != StatusCleared || r.AdjustmentType != AdjNone {
		t.Fatalf("got %s/%s", r.Status, r.AdjustmentType)
	}
	wantConfidenceAtLeast95(t, r)
}

func TestAmountMismatchHoldsAndNoLedgerHint(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Amount = dec("999.9000") }), candidate(nil))
	if r.Status != StatusPending || !r.AmountMismatch || r.AdjustmentType != AdjPartialPay {
		t.Fatalf("got %+v", r)
	}
}

func TestOverpaymentFlaggedAsCreditOnAccount(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Amount = dec("1500.0000") }), candidate(nil))
	if r.Status != StatusPending || r.AdjustmentType != AdjCreditOnAccount {
		t.Fatalf("got %s/%s", r.Status, r.AdjustmentType)
	}
}

func TestCurrencyMismatchAlwaysHoldsEvenWithExactAmount(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Currency = "GBP" }), candidate(nil))
	if r.Status != StatusPending || !r.CurrencyMismatch || r.AdjustmentType != AdjNone {
		t.Fatalf("got %+v", r)
	}
}

func TestAmountEpsilonBoundaryAcceptsWithinTolerance(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Amount = dec("1000.0001") }), candidate(nil))
	if r.Status != StatusCleared {
		t.Fatalf("got %s: %v", r.Status, r.Reasons)
	}
}

func TestPayerAliasHitRaisesConfidence(t *testing.T) {
	t.Parallel()
	c := candidate(func(c *Candidate) {
		c.CustomerName = "Acme Corporation Limited"
		c.Aliases = []string{"Acme Corp"}
	})
	if r := match(incoming(nil), c); r.Status != StatusCleared {
		t.Fatalf("got %s: %v", r.Status, r.Reasons)
	}
}

func TestPayerAliasMissNeverFailsOpen(t *testing.T) {
	t.Parallel()
	c := candidate(func(c *Candidate) { c.CustomerName = "Totally Different Name Ltd" })
	r := match(incoming(nil), c)
	if r.Status == StatusPending && r.Confidence.Cmp(dec("95")) >= 0 {
		t.Fatalf("held at confidence %s", r.Confidence)
	}
	if r.Status == StatusCleared && r.Confidence.Cmp(dec("95")) < 0 {
		t.Fatalf("cleared at confidence %s", r.Confidence)
	}
}

func TestEmptyCandidatesHoldsAsCreditOnAccount(t *testing.T) {
	t.Parallel()
	r := Match(incoming(nil), nil)
	if r.Status != StatusPending || r.Invoice != nil || r.AdjustmentType != AdjCreditOnAccount {
		t.Fatalf("got %+v", r)
	}
}

func TestDateDecayStillClearsWithinWindow(t *testing.T) {
	t.Parallel()
	c := candidate(func(c *Candidate) {
		c.IssueDate, c.DueDate = day(2026, 4, 1), day(2026, 4, 10)
	})
	r := match(incoming(func(p *Incoming) { p.PaymentDate = *day(2026, 4, 13) }), c)
	if r.Status != StatusCleared {
		t.Fatalf("got %s: %v", r.Status, r.Reasons)
	}
}

func TestDateOutsideWindowStillHoldsIfPayerWeak(t *testing.T) {
	t.Parallel()
	c := candidate(func(c *Candidate) {
		c.IssueDate, c.DueDate = day(2026, 1, 1), day(2026, 1, 15)
		c.CustomerName = "Different Ltd"
	})
	if r := match(incoming(nil), c); r.Status != StatusPending {
		t.Fatalf("got %s", r.Status)
	}
}

func whtCandidate() Candidate {
	return candidate(func(c *Candidate) {
		c.Currency = "IDR"
		c.BalanceDue = dec("10204082")
		c.WhtOutstanding = dec("204082")
	})
}

func TestWithholdingNetPaymentIsExactMatch(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Amount, p.Currency = dec("10000000"), "IDR" }), whtCandidate())
	if r.Status != StatusCleared || r.AmountMismatch {
		t.Fatalf("got %+v", r)
	}
}

func TestGrossPaymentOnWithholdingInvoiceGoesToReview(t *testing.T) {
	t.Parallel()
	r := match(incoming(func(p *Incoming) { p.Amount, p.Currency = dec("10204082"), "IDR" }), whtCandidate())
	if r.Status != StatusPending || !r.AmountMismatch || r.AdjustmentType != AdjCreditOnAccount {
		t.Fatalf("got %+v", r)
	}
}

// Sentences and edge rules.

func TestReasonsSentences(t *testing.T) {
	t.Parallel()
	t.Run("currency amount and confidence", func(t *testing.T) {
		t.Parallel()
		p := incoming(func(p *Incoming) { p.Currency, p.Amount = "gbp", dec("900.0000") })
		r := match(p, candidate(nil))
		want := []string{
			"currency mismatch: payment gbp vs invoice USD",
			"amount mismatch: delta=-100.0000",
			"confidence 26.4286 below threshold 95.0",
		}
		if strings.Join(r.Reasons, "|") != strings.Join(want, "|") {
			t.Fatalf("reasons %q, want %q", r.Reasons, want)
		}
	})
	t.Run("confidence only", func(t *testing.T) {
		t.Parallel()
		c := candidate(func(c *Candidate) { c.IssueDate, c.DueDate = day(2026, 4, 1), day(2026, 4, 5) })
		r := match(incoming(func(p *Incoming) { p.PayerName = "Zzz" }), c)
		want := "confidence 72.8571 below threshold 95.0"
		if len(r.Reasons) != 1 || r.Reasons[0] != want {
			t.Fatalf("reasons %q, want %q", r.Reasons, want)
		}
	})
	t.Run("cleared", func(t *testing.T) {
		t.Parallel()
		r := match(incoming(nil), candidate(nil))
		if len(r.Reasons) != 1 || r.Reasons[0] != "exact amount+currency, high payer+date confidence" {
			t.Fatalf("reasons %q", r.Reasons)
		}
	})
}

func TestNoCandidates(t *testing.T) {
	t.Parallel()
	r := Match(incoming(nil), nil)
	if r.Status != StatusPending || r.AdjustmentType != AdjCreditOnAccount || !r.Confidence.IsZero() ||
		!r.AmountMismatch || r.CurrencyMismatch {
		t.Fatalf("got %+v", r)
	}
	if len(r.Reasons) != 1 || r.Reasons[0] != "no open invoice matches payer/currency" {
		t.Fatalf("reasons %q", r.Reasons)
	}
}

func TestNonOpenCandidatesAreIgnored(t *testing.T) {
	t.Parallel()
	for _, st := range []string{"PAID", "DRAFT", "VOID", ""} {
		c := candidate(func(c *Candidate) { c.Status = st })
		r := match(incoming(nil), c)
		if r.Status != StatusPending || r.Invoice != nil {
			t.Fatalf("status %q: got %+v", st, r)
		}
	}
	for _, st := range []string{"OPEN", "SENT", "PARTIAL"} {
		c := candidate(func(c *Candidate) { c.Status = st })
		if r := match(incoming(nil), c); r.Status != StatusCleared {
			t.Fatalf("status %q: got %s", st, r.Status)
		}
	}
}

func TestIsExactMatchChecksCurrency(t *testing.T) {
	t.Parallel()
	if IsExactMatch(incoming(func(p *Incoming) { p.Currency = "GBP" }), candidate(nil)) {
		t.Fatal("a different currency is never exact")
	}
	if !IsExactMatch(incoming(func(p *Incoming) { p.Currency = "usd" }), candidate(nil)) {
		t.Fatal("currency compares upper-cased")
	}
	if IsExactMatch(incoming(func(p *Incoming) { p.Amount = dec("1000.0002") }), candidate(nil)) {
		t.Fatal("beyond epsilon is not exact")
	}
}

func TestTwoExactCandidatesHold(t *testing.T) {
	t.Parallel()
	later := candidate(func(c *Candidate) { c.DueDate = day(2026, 4, 24) })
	earlier := candidate(func(c *Candidate) { c.DueDate = day(2026, 4, 20) })
	r := match(incoming(nil), later, earlier)
	if r.Status != StatusPending {
		t.Fatalf("status %s, want PENDING_MANUAL_REVIEW", r.Status)
	}
	if r.Invoice == nil || r.Invoice.InvoiceID != earlier.InvoiceID {
		t.Fatalf("attached %+v, want the earlier due date", r.Invoice)
	}
	if r.AmountMismatch || r.CurrencyMismatch || r.AdjustmentType != AdjNone {
		t.Fatalf("got %+v", r)
	}
	if len(r.Reasons) != 1 || r.Reasons[0] != "ambiguous match: 2 open invoices clear equally" {
		t.Fatalf("reasons %q", r.Reasons)
	}
	three := match(incoming(nil), later, earlier, candidate(nil))
	if last := three.Reasons[len(three.Reasons)-1]; last != "ambiguous match: 3 open invoices clear equally" {
		t.Fatalf("reasons %q", three.Reasons)
	}
}

func TestOneExactAmongInexactStillClears(t *testing.T) {
	t.Parallel()
	other := candidate(func(c *Candidate) { c.BalanceDue = dec("1200.0000") })
	r := match(incoming(nil), other, candidate(nil))
	if r.Status != StatusCleared {
		t.Fatalf("got %s: %v", r.Status, r.Reasons)
	}
}

func TestTieOrderDeterministic(t *testing.T) {
	t.Parallel()
	a := candidate(func(c *Candidate) { c.InvoiceID = uuid.MustParse("00000000-0000-0000-0000-00000000000a") })
	b := candidate(func(c *Candidate) { c.InvoiceID = uuid.MustParse("00000000-0000-0000-0000-00000000000b") })
	noDue := candidate(func(c *Candidate) {
		c.InvoiceID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
		c.DueDate = nil
		c.IssueDate = day(2026, 4, 10)
	})
	cases := [][]Candidate{{a, b, noDue}, {noDue, b, a}, {b, noDue, a}, {a, noDue, b}}
	for _, cs := range cases {
		r := Match(incoming(nil), cs)
		if r.Invoice == nil || r.Invoice.InvoiceID != a.InvoiceID {
			t.Fatalf("attached %v, want %v (same due date: lowest id; nil due last)", r.Invoice.InvoiceID, a.InvoiceID)
		}
	}
	// A nil due date sorts after a dated one even with the lowest id.
	r := Match(incoming(nil), []Candidate{noDue, b})
	if r.Invoice.InvoiceID != b.InvoiceID {
		t.Fatalf("attached %v, want the dated one", r.Invoice.InvoiceID)
	}
}

func TestNullDatesScoreZeroProximity(t *testing.T) {
	t.Parallel()
	for name, mod := range map[string]func(*Candidate){
		"no issue": func(c *Candidate) { c.IssueDate = nil },
		"no due":   func(c *Candidate) { c.DueDate = nil },
		"neither":  func(c *Candidate) { c.IssueDate, c.DueDate = nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := match(incoming(nil), candidate(mod))
			if !r.Confidence.Equal(dec("90")) || r.Status != StatusPending {
				t.Fatalf("got %s %s", r.Confidence, r.Status)
			}
		})
	}
}

func TestDateProximityBoundaries(t *testing.T) {
	t.Parallel()
	// Exact amount, currency and payer score 90; the date adds 10*(14-a)/14.
	for _, tc := range []struct {
		name   string
		issue  *time.Time
		due    *time.Time
		score  string
		status string
	}{
		{"anchor 0 is 100", day(2026, 4, 15), day(2026, 5, 20), "100.0000", StatusCleared},
		{"anchor 7 is exactly 95", day(2026, 4, 8), day(2026, 5, 20), "95.0000", StatusCleared},
		{"anchor 8 is just below", day(2026, 4, 7), day(2026, 5, 20), "94.2857", StatusPending},
		{"anchor 13 keeps a sliver", day(2026, 4, 2), day(2026, 5, 20), "90.7143", StatusPending},
		{"anchor 14 is zero", day(2026, 4, 1), day(2026, 5, 20), "90.0000", StatusPending},
		{"due date can anchor", day(2026, 1, 1), day(2026, 4, 15), "100.0000", StatusCleared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := candidate(func(c *Candidate) { c.IssueDate, c.DueDate = tc.issue, tc.due })
			r := match(incoming(nil), c)
			if platform.PyDecimalStr(r.Confidence) != tc.score || r.Status != tc.status {
				t.Fatalf("got %s %s, want %s %s", r.Confidence, r.Status, tc.score, tc.status)
			}
		})
	}
}

func TestScoreHalfEven(t *testing.T) {
	t.Parallel()
	// a is 128 "a"; b shares exactly one rune with it, so ratio = 2/256 =
	// 0.0078125 exactly and 20*ratio = 0.15625: a tie at the fifth place.
	// Half-even gives 0.1562, half-up would give 0.1563.
	a := strings.Repeat("a", 128)
	b := "a" + strings.Repeat("b", 127)
	p := incoming(func(p *Incoming) {
		p.Currency, p.Amount, p.PayerName = "GBP", dec("1"), b
	})
	c := candidate(func(c *Candidate) { c.CustomerName, c.IssueDate, c.DueDate = a, nil, nil })
	if got := Score(p, c); platform.PyDecimalStr(got) != "0.1562" {
		t.Fatalf("score %s, want 0.1562", got)
	}
}

func TestPayerNormalizationLowerThenStrip(t *testing.T) {
	t.Parallel()
	p := incoming(func(p *Incoming) { p.PayerName = "  ACME CORP \t" })
	if r := match(p, candidate(nil)); r.Status != StatusCleared {
		t.Fatalf("got %s: %v", r.Status, r.Reasons)
	}
}

func TestWithinEpsilonLowConfidenceAdjustment(t *testing.T) {
	t.Parallel()
	// Within epsilon is exact, but a low-confidence hold still compares with
	// < and >: Python tags it CREDIT_ON_ACCOUNT (and PARTIAL_PAY below).
	weak := candidate(func(c *Candidate) { c.CustomerName = "Totally Different Name Ltd" })
	over := match(incoming(func(p *Incoming) { p.Amount = dec("1000.0001") }), weak)
	if over.Status != StatusPending || over.AmountMismatch || over.AdjustmentType != AdjCreditOnAccount {
		t.Fatalf("over: %+v", over)
	}
	under := match(incoming(func(p *Incoming) { p.Amount = dec("999.9999") }), weak)
	if under.Status != StatusPending || under.AmountMismatch || under.AdjustmentType != AdjPartialPay {
		t.Fatalf("under: %+v", under)
	}
}

func TestWithholdingClampsExpectedCashAtZero(t *testing.T) {
	t.Parallel()
	c := candidate(func(c *Candidate) { c.BalanceDue, c.WhtOutstanding = dec("100"), dec("150") })
	if !IsExactMatch(incoming(func(p *Incoming) { p.Amount = dec("0") }), c) {
		t.Fatal("expected cash is clamped at zero")
	}
}
