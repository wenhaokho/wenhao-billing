package invoices

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

func lockPath(id uuid.UUID) string { return invoicesPath + "/usage/" + id.String() + "/lock" }

// insertUsage stores a USAGE invoice with the given status, withholding
// snapshot and stored cycle config (cfg "" is NULL) and returns its id.
func insertUsage(t *testing.T, pool *pgxpool.Pool, status, cfg string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var ref any
	if cfg != "" {
		ref = cfg
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices (invoice_id, invoice_type, currency, subtotal, amount, balance_due, status,
	wht_type, wht_rate, wht_gross_up, billing_cycle_ref, is_template)
VALUES ($1, 'USAGE', 'IDR', 5, 5, 5, $2, 'PPH23', 2, true, $3::jsonb, false)`, id, status, ref); err != nil {
		t.Fatal(err)
	}
	return id
}

func lockUsageRoute(t *testing.T, a *apitest.App, id uuid.UUID, amount any, cutoff string) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, lockPath(id),
		map[string]any{"accrued_amount": amount, "cutoff_date": cutoff}), http.StatusOK)
}

// Port of test_invoice_withholding.py::test_usage_lock_recomputes_from_snapshot.
func TestUsageLockRecomputesFromSnapshot(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	out := lockUsageRoute(t, a, id, "10000000", "2026-05-25")
	wantDec4(t, "amount", p4(out.Amount), "10204082")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "10204082")
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "204082")
	wantDec4(t, "wht_base", out.WhtBase, "10000000")
}

func TestLockSetsCfg(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25, "note": "x"}`)
	out := lockUsageRoute(t, a, id, "100", "2026-05-25")
	cfg := cfgOf(t, out.BillingCycleRef)
	if cfg["cutoff_date"] != "2026-05-25" || cfg["locked"] != true || cfg["note"] != "x" {
		t.Errorf("cfg = %v", cfg)
	}
	// Still a DRAFT; subtotal untouched (5 was stored).
	if out.Status != "DRAFT" {
		t.Errorf("status = %s", out.Status)
	}
	wantDec4(t, "subtotal", out.Subtotal, "5")

	// A re-lock while DRAFT is allowed and overwrites the amounts.
	out = lockUsageRoute(t, a, id, "200", "2026-06-25")
	if cfgOf(t, out.BillingCycleRef)["cutoff_date"] != "2026-06-25" {
		t.Errorf("cfg = %s", out.BillingCycleRef)
	}
	wantDec4(t, "wht_base", out.WhtBase, "200")
	wantDec4(t, "amount", p4(out.Amount), "204")

	// A NULL config becomes an object.
	nullCfg := insertUsage(t, a.Pool, "DRAFT", "")
	cfg = cfgOf(t, lockUsageRoute(t, a, nullCfg, "0", "2026-05-25").BillingCycleRef)
	if len(cfg) != 2 || cfg["locked"] != true || cfg["cutoff_date"] != "2026-05-25" {
		t.Errorf("cfg from NULL = %v", cfg)
	}
}

func TestLockUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, lockPath(id), map[string]any{"accrued_amount": "1", "cutoff_date": "2026-05-25"}),
		http.StatusBadRequest, "invoice_not_found", "invoice "+id.String()+" not found", "", "")
}

func TestLockNonUsageIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "DRAFT", "10")
	wantProblem(t, a.Do(t, http.MethodPost, lockPath(id), map[string]any{"accrued_amount": "1", "cutoff_date": "2026-05-25"}),
		http.StatusBadRequest, "invoice_not_usage", "invoice "+id.String()+" is not USAGE", "", "")
}

func TestLockNonDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
		id := insertUsage(t, a.Pool, status, `{"cut_off_day": 25}`)
		wantProblem(t, a.Do(t, http.MethodPost, lockPath(id), map[string]any{"accrued_amount": "1", "cutoff_date": "2026-05-25"}),
			http.StatusBadRequest, "usage_invoice_locked", "usage invoice already locked (status="+status+")", "", "")
		if got := get(t, a, id); got.Status != status || cfgOf(t, got.BillingCycleRef)["locked"] != nil {
			t.Errorf("%s: invoice changed: %s %s", status, got.Status, got.BillingCycleRef)
		}
	}
}

func TestLockBodyValidation(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	wantProblem(t, a.Do(t, http.MethodPost, lockPath(id), map[string]any{"accrued_amount": "-1", "cutoff_date": "2026-05-25"}),
		http.StatusUnprocessableEntity, "", "Input should be greater than or equal to 0", "accrued_amount", "Input should be greater than or equal to 0")
	for _, body := range []map[string]any{
		{"cutoff_date": "2026-05-25"},
		{"accrued_amount": "1"},
		{"accrued_amount": "1", "cutoff_date": "not a date"},
	} {
		if rec := a.Do(t, http.MethodPost, lockPath(id), body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: status %d: %s", body, rec.Code, rec.Body)
		}
	}
	if got := get(t, a, id); cfgOf(t, got.BillingCycleRef)["locked"] != nil {
		t.Errorf("a refused body locked the invoice: %s", got.BillingCycleRef)
	}
}

func accruing(amounts map[uuid.UUID]string, seen *[]uuid.UUID) AccrualSource {
	return func(_ context.Context, id uuid.UUID) (decimal.Decimal, error) {
		*seen = append(*seen, id)
		return decimal.RequireFromString(amounts[id]), nil
	}
}

func cutoff(t *testing.T, a *apitest.App, today string, src AccrualSource) (int, error) {
	t.Helper()
	return CutoffScan(t.Context(), a.Pool, day(today), src)
}

func TestCutoffScanLocksMatchingDay(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	byInt := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	byString := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": " 25 "}`)
	amounts := map[uuid.UUID]string{byInt: "1000", byString: "2000"}
	var seen []uuid.UUID
	// Today is the 26th: yesterday is the 25th.
	n, err := cutoff(t, a, "2026-05-26", accruing(amounts, &seen))
	if err != nil || n != 2 {
		t.Fatalf("locked %d, err %v", n, err)
	}
	if len(seen) != 2 || seen[0] != byInt || seen[1] != byString {
		t.Errorf("accrual asked for %v, want oldest first", seen)
	}
	got := get(t, a, byInt)
	cfg := cfgOf(t, got.BillingCycleRef)
	if cfg["locked"] != true || cfg["cutoff_date"] != "2026-05-25" || got.Status != "DRAFT" {
		t.Errorf("cfg = %v, status %s", cfg, got.Status)
	}
	wantDec4(t, "wht_base", got.WhtBase, "1000")
	wantDec4(t, "amount", p4(got.Amount), "1020")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "1020")
	if cfgOf(t, get(t, a, byString).BillingCycleRef)["locked"] != true {
		t.Error("the numeric-string day was not locked")
	}

	// Month start: yesterday is the last day of the previous month.
	eom := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 28}`)
	seen = nil
	n, err = cutoff(t, a, "2026-03-01", accruing(map[uuid.UUID]string{eom: "1"}, &seen))
	if err != nil || n != 1 || cfgOf(t, get(t, a, eom).BillingCycleRef)["cutoff_date"] != "2026-02-28" {
		t.Errorf("end of month: locked %d, err %v", n, err)
	}
}

func TestCutoffScanSkipsLockedAndOtherDays(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantLocked := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	skipped := []uuid.UUID{
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25, "locked": true}`),
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 24}`),
		insertUsage(t, a.Pool, "DRAFT", `{"note": "no day"}`),
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": null}`),
		insertUsage(t, a.Pool, "DRAFT", ""),
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": "last"}`),
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25.5}`),
		insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": true}`),
		insertUsage(t, a.Pool, "OPEN", `{"cut_off_day": 25}`),
		insertWithStatus(t, a.Pool, "DRAFT", "10"),
	}
	// A locked flag that is falsy does not stop the lock.
	falsy := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25, "locked": false}`)
	var seen []uuid.UUID
	// The already-locked invoice has an accrual too, so a scan that relocked
	// it would change its amount.
	n, err := cutoff(t, a, "2026-05-26", accruing(map[uuid.UUID]string{wantLocked: "1", falsy: "1", skipped[0]: "999"}, &seen))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("locked %d, want 2", n)
	}
	if len(seen) != 2 || seen[0] != wantLocked || seen[1] != falsy {
		t.Errorf("accrual asked for %v", seen)
	}
	for _, id := range skipped {
		if got := get(t, a, id); got.Status == "DRAFT" && cfgOf(t, got.BillingCycleRef)["cutoff_date"] != nil {
			t.Errorf("%s was locked: %s", id, got.BillingCycleRef)
		}
	}
	got := get(t, a, skipped[0])
	if got.BillingCycleRef == nil || cfgOf(t, got.BillingCycleRef)["cutoff_date"] != nil {
		t.Errorf("already locked invoice rewritten: %s", got.BillingCycleRef)
	}
	if !got.Amount.Decimal().Equal(decimal.NewFromInt(5)) {
		t.Errorf("already locked invoice amount = %s, want 5 (unchanged)", got.Amount.Decimal())
	}
}

func TestCutoffScanAccrualErrorChangesNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	first := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	second := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	boom := errors.New("metering offline")
	src := func(_ context.Context, id uuid.UUID) (decimal.Decimal, error) {
		if id == second {
			return decimal.Zero, boom
		}
		return decimal.NewFromInt(100), nil
	}
	n, err := cutoff(t, a, "2026-05-26", src)
	if !errors.Is(err, boom) || n != 0 {
		t.Errorf("locked %d, err %v; want 0 and the accrual error", n, err)
	}
	for _, id := range []uuid.UUID{first, second} {
		got := get(t, a, id)
		if cfgOf(t, got.BillingCycleRef)["locked"] != nil {
			t.Errorf("%s locked despite the failed run", id)
		}
		wantDec4(t, "amount", p4(got.Amount), "5")
	}
}

func TestCutoffScanUsesUTCDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	var seen []uuid.UUID
	// 2026-05-26 23:30 in UTC-5 is already the 27th in UTC: yesterday is the 26th.
	late := time.Date(2026, 5, 26, 23, 30, 0, 0, time.FixedZone("x", -5*3600))
	n, err := CutoffScan(t.Context(), a.Pool, late, accruing(map[uuid.UUID]string{id: "1"}, &seen))
	if err != nil || n != 0 {
		t.Errorf("locked %d, err %v", n, err)
	}
}
