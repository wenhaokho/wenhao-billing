package stats_test

import (
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

func dashboard(t *testing.T, setup func(a *appCtx)) stats.DashboardCore {
	t.Helper()
	a := newApp(t)
	setup(&appCtx{t: t, a: a})
	got, err := stats.Dashboard(t.Context(), a.Pool, "IDR", today(a), a.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDashboardEmptyDBReturnsZeroCountsAndNoCurrencyRows(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(*appCtx) {})
	if got.AwaitingReviewCount != 0 || got.DraftCount != 0 || got.SentCount != 0 || got.CustomerCount != 0 ||
		got.AutoClearedLast30d != 0 || got.OpenQuotationCount != 0 {
		t.Fatalf("counts = %+v", got)
	}
	for name, l := range map[string][]string{
		"open_ar_by_currency":     amounts(got.OpenARByCurrency),
		"open_quotation_pipeline": amounts(got.OpenQuotationPipeline),
		"mrr_by_currency":         amounts(got.MRRByCurrency),
		"open_ar_unconverted":     got.OpenARUnconverted,
	} {
		if l == nil || len(l) != 0 {
			t.Errorf("%s = %v, want an empty non-nil list", name, l)
		}
	}
	if got.OpenARBaseByCurrency == nil || len(got.OpenARBaseByCurrency) != 0 {
		t.Errorf("open_ar_base_by_currency = %v, want an empty non-nil list", got.OpenARBaseByCurrency)
	}
	if got.BaseCurrency != "IDR" {
		t.Errorf("base_currency = %q", got.BaseCurrency)
	}
}

func TestDashboardCountsReflectInvoiceAndCustomerState(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		c.invoice(cust, "USD", "1000", "DRAFT")
		c.invoice(cust, "USD", "1000", "SENT")
		c.invoice(cust, "USD", "1000", "SENT")
	})
	if got.DraftCount != 1 || got.SentCount != 2 || got.CustomerCount != 1 {
		t.Fatalf("draft %d sent %d customers %d, want 1 2 1", got.DraftCount, got.SentCount, got.CustomerCount)
	}
}

// sent_count is OPEN and SENT, with no template filter and no PARTIAL; an
// inactive customer still counts.
func TestDashboardSentCountStatusesAndCustomers(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		c.invoice(cust, "USD", "10", "OPEN")
		c.invoice(cust, "USD", "10", "SENT")
		c.invoice(cust, "USD", "10", "PARTIAL")
		c.invoice(cust, "USD", "10", "PAID")
		c.invoiceFull(cust, "USD", "10", "10", "SENT", true) // a template is counted, as in Python
		exec(c.t, c.a, `INSERT INTO customers (customer_id, name, active) VALUES (gen_random_uuid(), 'Gone', false)`)
	})
	if got.SentCount != 3 || got.CustomerCount != 2 {
		t.Fatalf("sent %d customers %d, want 3 2", got.SentCount, got.CustomerCount)
	}
}

func TestDashboardOpenARByCurrencySumsSentAndPartialOnly(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		c.invoice(cust, "USD", "500", "SENT")
		c.invoiceFull(cust, "USD", "1000", "300", "PARTIAL", false)
		c.invoice(cust, "USD", "999", "DRAFT") // excluded
		c.invoice(cust, "SGD", "800", "SENT")
	})
	// Both are 800; equal totals come in currency order.
	eq(t, "open_ar_by_currency", amounts(got.OpenARByCurrency), []string{"SGD 800.0000", "USD 800.0000"})
}

func TestDashboardOpenQuotationPipelineSumsSentAndAcceptedOnlySortedDesc(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		quote(c.t, c.a, cust, "USD", "500", "SENT")
		quote(c.t, c.a, cust, "SGD", "2000", "ACCEPTED")
		quote(c.t, c.a, cust, "GBP", "999999", "DECLINED") // excluded
		quote(c.t, c.a, cust, "EUR", "999999", "DRAFT")    // excluded
	})
	if got.OpenQuotationCount != 2 {
		t.Fatalf("open_quotation_count = %d, want 2", got.OpenQuotationCount)
	}
	// Numeric order (SGD 2000 > USD 500), not string order.
	eq(t, "pipeline", amounts(got.OpenQuotationPipeline), []string{"SGD 2000.0000", "USD 500.0000"})
}

// Equal pipeline totals keep currency order, run after run.
func TestPipelineTieOrderDeterministic(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		for _, cur := range []string{"USD", "SGD", "AUD", "EUR"} {
			quote(c.t, c.a, cust, cur, "700", "SENT")
		}
		quote(c.t, c.a, cust, "GBP", "900", "ACCEPTED")
	})
	eq(t, "pipeline", amounts(got.OpenQuotationPipeline),
		[]string{"GBP 900.0000", "AUD 700.0000", "EUR 700.0000", "SGD 700.0000", "USD 700.0000"})
}

func TestDashboardAutoClearedLast30dCountsOnlyClearedPayments(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		inv := c.invoice(cust, "USD", "0", "SENT")
		payment(c.t, c.a, inv, "CLEARED", time.Time{})
		payment(c.t, c.a, inv, "PENDING_MANUAL_REVIEW", time.Time{}) // excluded
	})
	if got.AutoClearedLast30d != 1 {
		t.Fatalf("auto_cleared_last_30d = %d, want 1", got.AutoClearedLast30d)
	}
}

// The window is created_at >= now - 30 days, a full timestamp: a payment
// exactly 30 days old counts, one a second older does not, and FLAGGED
// (reversed) ones never do.
func TestDashboardAutoCleared30DayWindow(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		inv := c.invoice(cust, "USD", "0", "SENT")
		edge := c.a.Clock.Now().UTC().Add(-30 * 24 * time.Hour)
		payment(c.t, c.a, inv, "CLEARED", edge)                                 // counts
		payment(c.t, c.a, inv, "CLEARED", edge.Add(-time.Second))               // too old
		payment(c.t, c.a, inv, "CLEARED", edge.Add(24*time.Hour))               // counts
		payment(c.t, c.a, inv, "FLAGGED", edge.Add(24*time.Hour))               // not cleared
		payment(c.t, c.a, inv, "CLEARED", edge.Add(-60*24*time.Hour))           // too old
		payment(c.t, c.a, inv, "CLEARED", c.a.Clock.Now().UTC().Add(time.Hour)) // after now still counts: no upper bound
	})
	if got.AutoClearedLast30d != 3 {
		t.Fatalf("auto_cleared_last_30d = %d, want 3", got.AutoClearedLast30d)
	}
}

func TestDashboardAwaitingReviewCountCountsPendingManualReviewPayments(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		inv := c.invoice(cust, "USD", "0", "SENT")
		payment(c.t, c.a, inv, "PENDING_MANUAL_REVIEW", time.Time{})
		payment(c.t, c.a, inv, "CLEARED", time.Time{}) // excluded
	})
	if got.AwaitingReviewCount != 1 {
		t.Fatalf("awaiting_review_count = %d, want 1", got.AwaitingReviewCount)
	}
}

func TestDashboardDraftCountExcludesRecurringTemplates(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		c.invoice(cust, "USD", "1000", "DRAFT")                                              // a real finalizable draft: counts
		template(c.t, c.a, cust, "USD", "1200", with(monthly(), "start_date", "2026-01-05")) // a template: does not
	})
	if got.DraftCount != 1 {
		t.Fatalf("draft_count = %d, want 1", got.DraftCount)
	}
}

func TestDashboardIncludesMRRAndBaseAR(t *testing.T) {
	t.Parallel()
	got := dashboard(t, func(c *appCtx) {
		cust := c.customer("Acme Corp")
		template(c.t, c.a, cust, "USD", "1200", with(monthly(), "start_date", "2026-01-05"))
		c.invoice(cust, "USD", "100", "SENT")
	})
	eq(t, "mrr_by_currency", amounts(got.MRRByCurrency), []string{"USD 1200.0000"})
	if got.BaseCurrency != "IDR" {
		t.Errorf("base_currency = %q", got.BaseCurrency)
	}
	if len(got.OpenARBaseByCurrency) != 1 || got.OpenARBaseByCurrency[0].Currency != "USD" ||
		got.OpenARBaseByCurrency[0].BaseAmount.Decimal().StringFixed(4) != "1600000.0000" {
		t.Fatalf("open_ar_base_by_currency = %+v", got.OpenARBaseByCurrency)
	}
	if got.OpenARUnconverted == nil || len(got.OpenARUnconverted) != 0 {
		t.Fatalf("open_ar_unconverted = %v, want []", got.OpenARUnconverted)
	}
}
