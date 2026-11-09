package stats_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// upDay is the date test_upcoming_invoices.py runs on.
var upDay = date(2026, time.July, 24)

func upcoming(t *testing.T, a *apitest.App, today time.Time) []stats.UpcomingInvoiceItem {
	t.Helper()
	rows, err := stats.Upcoming(t.Context(), a.Pool, today)
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil {
		t.Fatal("Upcoming returned nil, want a non-nil list")
	}
	return rows
}

// recurring inserts a RECURRING template starting on start.
func recurring(t *testing.T, a *apitest.App, cust uuid.UUID, amount, frequency, start string, extra ...any) {
	t.Helper()
	template(t, a, cust, "USD", amount, with(monthly(), append([]any{"frequency", frequency, "start_date", start}, extra...)...))
}

// usage inserts a USAGE invoice whose billing_cycle_ref is the raw JSON cfg
// (a nil customer leaves it without one).
func usage(t *testing.T, a *apitest.App, cust *uuid.UUID, status, cfg string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template, billing_cycle_ref)
VALUES ($1, $2, 'USAGE', 'USD', 0, 0, $3, false, $4::jsonb)`, id, cust, status, cfg)
	return id
}

// dates is the rows' "MODE next_date" strings, in order.
func dates(rows []stats.UpcomingInvoiceItem) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Mode+" "+r.NextDate.Format("2006-01-02"))
	}
	return out
}

func TestUpcomingEmptyDBReturnsEmpty(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if got := upcoming(t, a, upDay); len(got) != 0 {
		t.Fatalf("rows = %v", got)
	}
}

func TestUpcomingRecurringNextOccurrenceWithinWindow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	recurring(t, a, c, "1000", "MONTHLY", "2026-01-05")
	rows := upcoming(t, a, upDay)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	r := rows[0]
	if r.Mode != "RECURRING" || r.NextDate.Format("2006-01-02") != "2026-08-05" || r.Amount.Decimal().StringFixed(4) != "1000.0000" || r.AmountIsEstimate {
		t.Fatalf("row = %+v", r)
	}
	if r.CustomerID == nil || *r.CustomerID != c || r.CustomerName == nil || *r.CustomerName != "Acme Corp" || r.Currency != "USD" {
		t.Fatalf("customer/currency of %+v", r)
	}
}

func TestUpcomingRecurringReportedOnceEvenIfMultipleCyclesInWindow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	recurring(t, a, newCustomer(t, a, "Acme Corp"), "1000", "WEEKLY", "2026-01-05")
	if rows := upcoming(t, a, upDay); len(rows) != 1 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

func TestUpcomingRecurringBeyondWindowExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	recurring(t, a, newCustomer(t, a, "Acme Corp"), "1000", "YEARLY", "2026-01-05")
	if rows := upcoming(t, a, upDay); len(rows) != 0 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

// The horizon is today + 90 days, inclusive: 2026-10-22 is in, 2026-10-23 out.
func TestUpcomingHorizonIsNinetyDaysInclusive(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	recurring(t, a, c, "1", "YEARLY", "2025-10-22")
	recurring(t, a, c, "2", "YEARLY", "2025-10-23")
	if got := dates(upcoming(t, a, upDay)); len(got) != 1 || got[0] != "RECURRING 2026-10-22" {
		t.Fatalf("rows = %v, want only RECURRING 2026-10-22", got)
	}
}

func TestUpcomingPausedRecurringExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	recurring(t, a, newCustomer(t, a, "Acme Corp"), "1000", "MONTHLY", "2026-01-05", "paused", true)
	if rows := upcoming(t, a, upDay); len(rows) != 0 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

func TestUpcomingEndedRecurringExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	recurring(t, a, newCustomer(t, a, "Acme Corp"), "1000", "MONTHLY", "2026-01-05", "end_mode", "ON_DATE", "end_date", "2026-03-01")
	if rows := upcoming(t, a, upDay); len(rows) != 0 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

// A cycle that starts today is not "after today": the row is the next one.
func TestRecurringCycleStartingTodayNotListed(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	recurring(t, a, newCustomer(t, a, "Acme Corp"), "1000", "MONTHLY", "2026-01-24")
	if got := dates(upcoming(t, a, upDay)); len(got) != 1 || got[0] != "RECURRING 2026-08-24" {
		t.Fatalf("rows = %v, want RECURRING 2026-08-24", got)
	}
}

func TestUpcomingUsageNextCutoffIsEstimate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	usage(t, a, &c, "DRAFT", `{"cut_off_day": 20}`)
	rows := upcoming(t, a, upDay)
	if len(rows) != 1 || rows[0].Mode != "USAGE" || rows[0].NextDate.Format("2006-01-02") != "2026-08-20" || !rows[0].AmountIsEstimate {
		t.Fatalf("rows = %+v", rows)
	}
}

// A cut-off falling on today is listed (usage is on or after today, unlike
// recurring).
func TestUpcomingUsageCutoffTodayIsListed(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	usage(t, a, &c, "DRAFT", `{"cut_off_day": 24}`)
	if got := dates(upcoming(t, a, upDay)); len(got) != 1 || got[0] != "USAGE 2026-07-24" {
		t.Fatalf("rows = %v, want USAGE 2026-07-24", got)
	}
}

func TestUpcomingUsageLockedOrNoCutoffSkipped(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	usage(t, a, &c, "DRAFT", `{"cut_off_day": 20, "locked": true}`)
	usage(t, a, &c, "DRAFT", `{}`)
	if rows := upcoming(t, a, upDay); len(rows) != 0 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

// Python truthiness of "locked": 0, "", null and false do not lock.
func TestUpcomingUsageLockedTruthiness(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		locked string
		listed bool
	}{
		{`true`, false},
		{`1`, false},
		{`"yes"`, false},
		{`[1]`, false},
		{`{"a": 1}`, false},
		{`false`, true},
		{`0`, true},
		{`0.0`, true},
		{`""`, true},
		{`null`, true},
		{`[]`, true},
		{`{}`, true},
	} {
		t.Run(c.locked, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			cu := newCustomer(t, a, "Acme Corp")
			usage(t, a, &cu, "DRAFT", fmt.Sprintf(`{"cut_off_day": 20, "locked": %s}`, c.locked))
			if got := len(upcoming(t, a, upDay)) == 1; got != c.listed {
				t.Fatalf("locked %s: listed = %v, want %v", c.locked, got, c.listed)
			}
		})
	}
}

func TestUpcomingUsageNonDraftSkipped(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	usage(t, a, &c, "SENT", `{"cut_off_day": 25}`)
	if rows := upcoming(t, a, upDay); len(rows) != 0 {
		t.Fatalf("rows = %v", dates(rows))
	}
}

func TestUpcomingMixedSortedByDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	usage(t, a, &c, "DRAFT", `{"cut_off_day": 28}`)
	recurring(t, a, c, "1000", "MONTHLY", "2026-01-05")
	eq(t, "rows", dates(upcoming(t, a, upDay)), []string{"USAGE 2026-07-28", "RECURRING 2026-08-05"})
}

// Equal dates sort by invoice id.
func TestUpcomingSortTieBreaksByInvoiceID(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	ids := []string{"00000000-0000-0000-0000-00000000000a", "00000000-0000-0000-0000-00000000000b", "00000000-0000-0000-0000-00000000000c"}
	for _, id := range []string{ids[1], ids[2], ids[0]} {
		exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, billing_cycle_ref)
VALUES ($1, $2, 'USAGE', 'USD', 0, 0, 'DRAFT', '{"cut_off_day": 28}')`, id, c)
	}
	var got []string
	for _, r := range upcoming(t, a, upDay) {
		got = append(got, r.InvoiceID.String())
	}
	eq(t, "ids", got, ids)
}

// A recurring and a usage row on one date sort by invoice id, though the
// recurring rows are listed first.
func TestUpcomingSortTieAcrossModesByInvoiceID(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	rec, use := "ffffffff-ffff-ffff-ffff-ffffffffffff", "00000000-0000-0000-0000-000000000001"
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template, billing_cycle_ref)
VALUES ($1, $2, 'RECURRING', 'USD', 5, 0, 'DRAFT', true, '{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-05", "end_mode": "NEVER"}')`, rec, c)
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, billing_cycle_ref)
VALUES ($1, $2, 'USAGE', 'USD', 0, 0, 'DRAFT', '{"cut_off_day": 5}')`, use, c)
	var got []string
	for _, r := range upcoming(t, a, upDay) {
		got = append(got, r.Mode+" "+r.NextDate.Format("2006-01-02")+" "+r.InvoiceID.String())
	}
	eq(t, "rows", got, []string{"USAGE 2026-08-05 " + use, "RECURRING 2026-08-05 " + rec})
}

// The date decides before the invoice id: the earlier date has the larger id.
func TestUpcomingSortsByDateBeforeInvoiceID(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	late, early := "00000000-0000-0000-0000-000000000001", "ffffffff-ffff-ffff-ffff-ffffffffffff"
	for id, day := range map[string]int{late: 28, early: 25} {
		exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, billing_cycle_ref)
VALUES ($1, $2, 'USAGE', 'USD', 0, 0, 'DRAFT', jsonb_build_object('cut_off_day', $3::int))`, id, c, day)
	}
	var got []string
	for _, r := range upcoming(t, a, upDay) {
		got = append(got, r.InvoiceID.String())
	}
	eq(t, "ids", got, []string{early, late})
}

func TestUpcomingWithoutCustomerHasNullCustomer(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	usage(t, a, nil, "DRAFT", `{"cut_off_day": 28}`)
	rows := upcoming(t, a, upDay)
	if len(rows) != 1 || rows[0].CustomerID != nil || rows[0].CustomerName != nil {
		t.Fatalf("rows = %+v", rows)
	}
}

// cut_off_day must be a JSON integer 1-31: Python's isinstance(dom, int),
// which counts true as 1 and rejects "20", 20.0 and false (0).
func TestUsageCutOffDayTypes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		raw  string
		want string // "" = skipped
	}{
		{`20`, "2026-08-20"},
		{`1`, "2026-08-01"},
		{`31`, "2026-07-31"},
		{`true`, "2026-08-01"},
		{`"20"`, ""},
		{`20.0`, ""},
		{`2e1`, "2026-08-20"}, // jsonb stores 2e1 as the integer 20, so Python reads an int too
		{`false`, ""},
		{`0`, ""},
		{`32`, ""},
		{`-5`, ""},
		{`null`, ""},
		{`[20]`, ""},
		{`99999999999999999999`, ""},
	} {
		t.Run(c.raw, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			cu := newCustomer(t, a, "Acme Corp")
			usage(t, a, &cu, "DRAFT", fmt.Sprintf(`{"cut_off_day": %s}`, c.raw))
			got := dates(upcoming(t, a, upDay))
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("cut_off_day %s: rows = %v, want skipped", c.raw, got)
				}
				return
			}
			if len(got) != 1 || got[0] != "USAGE "+c.want {
				t.Fatalf("cut_off_day %s: rows = %v, want USAGE %s", c.raw, got, c.want)
			}
		})
	}
}

// Day 31 clamps to the month's length: Feb 28 (and Feb 29 in a leap year),
// and Apr 30 on Apr 30 itself.
func TestUsageCutOffClampsToMonthEnd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		today time.Time
		want  string
	}{
		{date(2026, time.February, 10), "2026-02-28"},
		{date(2028, time.February, 10), "2028-02-29"},
		{date(2026, time.April, 30), "2026-04-30"},
		{date(2026, time.February, 28), "2026-02-28"},
		{date(2026, time.December, 31), "2026-12-31"},
		{date(2026, time.November, 30), "2026-11-30"},
	} {
		t.Run(c.today.Format("2006-01-02"), func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			cu := newCustomer(t, a, "Acme Corp")
			usage(t, a, &cu, "DRAFT", `{"cut_off_day": 31}`)
			if got := dates(upcoming(t, a, c.today)); len(got) != 1 || got[0] != "USAGE "+c.want {
				t.Fatalf("today %s: rows = %v, want USAGE %s", c.today.Format("2006-01-02"), got, c.want)
			}
		})
	}
}

// Past the cut-off day this month the next one is next month, clamped there.
func TestUsageCutOffRollsToNextMonth(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	cu := newCustomer(t, a, "Acme Corp")
	usage(t, a, &cu, "DRAFT", `{"cut_off_day": 30}`)
	// Jan 31: day 30 is past, next is Feb 28 (clamped).
	if got := dates(upcoming(t, a, date(2026, time.January, 31))); len(got) != 1 || got[0] != "USAGE 2026-02-28" {
		t.Fatalf("rows = %v", got)
	}
	// Dec 31 rolls into the next year.
	if got := dates(upcoming(t, a, date(2026, time.December, 31))); len(got) != 1 || got[0] != "USAGE 2027-01-30" {
		t.Fatalf("rows = %v", got)
	}
}
