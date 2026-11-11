package bills_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own (PORT-NOTES 164): a bill posts Dr expense / Cr 2000 when it
// opens, an edit of an OPEN bill re-posts, and going back to DRAFT or VOID
// reverses the entry.

// base is the ledger's base currency in these tests (apitest seeds X→IDR rates).
const base = "IDR"

// bEntry is a BILL journal entry: its memo, posting time, whether it is
// reversed, and its lines as "code debit/credit base_debit/base_credit".
type bEntry struct {
	id       uuid.UUID
	memo     string
	postedAt time.Time
	reversed bool
	lines    map[string]string
}

// billEntries are the journal entries whose source is bill id (journal
// entries have no creation time, so in no particular order: find one by memo).
func billEntries(t *testing.T, a *apitest.App, id uuid.UUID) []bEntry {
	t.Helper()
	return billEntriesOf(t, a, "BILL", id)
}

// billEntriesOf are the journal entries whose source is (sourceType, id).
func billEntriesOf(t *testing.T, a *apitest.App, sourceType string, id uuid.UUID) []bEntry {
	t.Helper()
	rows, err := a.Pool.Query(t.Context(), `SELECT entry_id, COALESCE(memo, ''), posted_at, reversed_by_entry_id IS NOT NULL
FROM journal_entries WHERE source_type = $2 AND source_id = $1 ORDER BY posted_at, entry_id`, id, sourceType)
	if err != nil {
		t.Fatal(err)
	}
	var out []bEntry
	for rows.Next() {
		var e bEntry
		if err := rows.Scan(&e.id, &e.memo, &e.postedAt, &e.reversed); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		lr, err := a.Pool.Query(t.Context(), `SELECT a.code, l.debit::text || '/' || l.credit::text || ' ' || l.base_amount_debit::text || '/' || l.base_amount_credit::text
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id WHERE l.entry_id = $1`, out[i].id)
		if err != nil {
			t.Fatal(err)
		}
		out[i].lines = map[string]string{}
		for lr.Next() {
			var code, v string
			if err := lr.Scan(&code, &v); err != nil {
				t.Fatal(err)
			}
			out[i].lines[code] = v
		}
		if err := lr.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// find is the entry of es with memo.
func find(t *testing.T, es []bEntry, memo string) bEntry {
	t.Helper()
	for _, e := range es {
		if e.memo == memo {
			return e
		}
	}
	t.Fatalf("no entry %q in %+v", memo, es)
	return bEntry{}
}

// activeEntries are bill id's entries neither reversed nor reversals.
func activeEntries(t *testing.T, a *apitest.App, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM journal_entries e
WHERE e.source_type = 'BILL' AND e.source_id = $1 AND e.reversed_by_entry_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// netBase is Σ base debit − base credit on code over bill id's entries.
func netBase(t *testing.T, a *apitest.App, id uuid.UUID, code string) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := a.Pool.QueryRow(t.Context(), `SELECT COALESCE(sum(l.base_amount_debit - l.base_amount_credit), 0)
FROM journal_lines l JOIN journal_entries e ON e.entry_id = l.entry_id JOIN chart_of_accounts c ON c.account_id = l.account_id
WHERE e.source_type = 'BILL' AND e.source_id = $1 AND c.code = $2`, id, code).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func wantJournalRows(t *testing.T, a *apitest.App, entries int) {
	t.Helper()
	if n := countRows(t, a, "journal_entries"); n != entries {
		t.Errorf("%d journal entries, want %d", n, entries)
	}
}

func accountID(t *testing.T, a *apitest.App, code string) int32 {
	t.Helper()
	var id int32
	if err := a.Pool.QueryRow(t.Context(), `SELECT account_id FROM chart_of_accounts WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("account %s: %v", code, err)
	}
	return id
}

// expenseItem stores an item whose expense account has code.
func expenseItem(t *testing.T, a *apitest.App, code string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO items (item_id, name, item_type, expense_account_id)
VALUES ($1, 'Item', 'SERVICE', (SELECT account_id FROM chart_of_accounts WHERE code = $2))`, id, code)
	return id
}

func accountLine(account int32, qty, price any) map[string]any {
	l := line(qty, price)
	l["expense_account_id"] = account
	return l
}

func TestPatchOpenPostsBill(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	item := expenseItem(t, a, "5000")
	itemLine := line(1, 300)
	itemLine["item_id"] = item
	out := post(t, a, body(insertVendor(t, a), map[string]any{
		"currency": "USD", "bill_number": "B-7", "issue_date": "2026-01-10", "discount_type": "AMOUNT", "discount_value": 100,
		// The line's own account, the item's, and neither (6000).
		"line_items": []any{accountLine(accountID(t, a, "5100"), 1, 600), itemLine, line(1, 100)},
	}))
	wantJournalRows(t, a, 0)
	got := patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	if got.Status != "OPEN" {
		t.Fatalf("status %s", got.Status)
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "900")
	es := billEntries(t, a, out.BillID)
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1", len(es))
	}
	e := es[0]
	if e.memo != "Bill B-7 opened" || e.postedAt.UTC().Format(time.DateOnly) != "2026-01-10" {
		t.Errorf("memo %q posted %s", e.memo, e.postedAt)
	}
	// 900 split 600:300:100 after the discount; base at 16000.
	want := map[string]string{
		"5100": "540.0000/0.0000 8640000.0000/0.0000",
		"5000": "270.0000/0.0000 4320000.0000/0.0000",
		"6000": "90.0000/0.0000 1440000.0000/0.0000",
		"2000": "0.0000/900.0000 0.0000/14400000.0000",
	}
	if len(e.lines) != len(want) {
		t.Errorf("lines %v", e.lines)
	}
	for code, w := range want {
		if e.lines[code] != w {
			t.Errorf("%s line %q, want %q", code, e.lines[code], w)
		}
	}
}

// A bill with no issue date posts on its created_at date, at that date's rate.
func TestOpenNullIssueDateUsesCreatedAt(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"currency": "USD"}))
	exec(t, a, `UPDATE bills SET created_at = '2026-03-05 18:00:00' WHERE bill_id = $1`, out.BillID)
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	es := billEntries(t, a, out.BillID)
	if len(es) != 1 || es[0].postedAt.UTC().Format(time.DateOnly) != "2026-03-05" || es[0].memo != "Bill "+out.BillID.String()+" opened" {
		t.Fatalf("entries %+v", es)
	}
	if es[0].lines["2000"] != "0.0000/1000.0000 0.0000/16000000.0000" {
		t.Errorf("2000 line %q", es[0].lines["2000"])
	}
}

func TestOpenZeroBillPostsNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, map[string]any{"vendor_id": insertVendor(t, a), "currency": "IDR"})
	got := patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	if got.Status != "OPEN" {
		t.Errorf("status %s", got.Status)
	}
	wantJournalRows(t, a, 0)
	// Voiding it has nothing to reverse, and is not refused.
	if got := void(t, a, out.BillID, map[string]any{}); got.Status != "VOID" {
		t.Errorf("status %s", got.Status)
	}
	wantJournalRows(t, a, 0)
}

func TestBillExpenseTypeRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, setup, detail string
		lineFor             func(t *testing.T, a *apitest.App) map[string]any
	}{
		{
			"income line account", "", "account 4000 (INCOME) cannot take an expense; set the line's or the item's expense account to an EXPENSE, COGS or ASSET account",
			func(t *testing.T, a *apitest.App) map[string]any {
				return accountLine(accountID(t, a, "4000"), 1, 1000)
			},
		},
		{
			"liability item account", "", "account 2000 (LIABILITY) cannot take an expense; set the line's or the item's expense account to an EXPENSE, COGS or ASSET account",
			func(t *testing.T, a *apitest.App) map[string]any {
				l := line(1, 1000)
				l["item_id"] = expenseItem(t, a, "2000")
				return l
			},
		},
		{
			"inactive account", `INSERT INTO chart_of_accounts (code, name, type, active) VALUES ('6-900', 'Old', 'EXPENSE', false)`,
			"account 6-900 is inactive; set the line's or the item's expense account to an active account",
			func(t *testing.T, a *apitest.App) map[string]any {
				return accountLine(accountID(t, a, "6-900"), 1, 1000)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			if c.setup != "" {
				exec(t, a, c.setup)
			}
			out := post(t, a, body(insertVendor(t, a), map[string]any{"line_items": []any{c.lineFor(t, a)}}))
			wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": "OPEN"}),
				http.StatusBadRequest, "ledger_refused", c.detail, "", "")
			if got := get(t, a, out.BillID); got.Status != "DRAFT" {
				t.Errorf("status %s, want DRAFT", got.Status)
			}
			wantJournalRows(t, a, 0)
		})
	}
	t.Run("asset and cogs allowed", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		out := post(t, a, body(insertVendor(t, a), map[string]any{"line_items": []any{
			accountLine(accountID(t, a, "1300"), 1, 100), accountLine(accountID(t, a, "5200"), 1, 100),
		}}))
		patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
		if n := activeEntries(t, a, out.BillID); n != 1 {
			t.Errorf("%d active entries, want 1", n)
		}
	})
}

func TestOpenMissingRateLeavesDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"currency": "EUR", "issue_date": "2026-01-15"}))
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": "OPEN"}),
		http.StatusBadRequest, "fx_rate_missing", "no fx rate for EUR->IDR on or before 2026-01-15", "", "")
	if got := get(t, a, out.BillID); got.Status != "DRAFT" {
		t.Errorf("status %s", got.Status)
	}
	wantJournalRows(t, a, 0)
}

func TestOpenBillEditReposts(t *testing.T) {
	t.Parallel()
	t.Run("posting fields re-post", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		out := post(t, a, body(insertVendor(t, a), nil))
		patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
		got := patch(t, a, out.BillID, map[string]any{"line_items": []any{line(3, 1000)}})
		wantDec4(t, "amount", p4(got.Amount), "3000")
		// Python left the balance at 1000.
		wantDec4(t, "balance_due", p4(got.BalanceDue), "3000")
		es := billEntries(t, a, out.BillID)
		if len(es) != 3 {
			t.Fatalf("entries %+v", es)
		}
		find(t, es, "Edit of bill "+out.BillID.String())
		if n := activeEntries(t, a, out.BillID); n != 1 {
			t.Errorf("%d active entries, want 1", n)
		}
		wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "-3000")
		wantD(t, "net 6000", netBase(t, a, out.BillID, "6000"), "3000")
		// The currency and the issue date re-post too; a discount-only PATCH
		// does not reprice (Python's oddity 6), so it does not re-post
		// (TestOpenEditRepostsOnlyOnChange).
		for _, change := range []map[string]any{{"currency": "USD"}, {"issue_date": "2026-02-01"}, {"discount_type": "AMOUNT", "discount_value": 1}} {
			patch(t, a, out.BillID, change)
		}
		if es := billEntries(t, a, out.BillID); len(es) != 7 {
			t.Errorf("%d entries after three more edits, want 7", len(es))
		}
		wantD(t, "net 2000 in USD at 16000", netBase(t, a, out.BillID, "2000"), "-48000000")
		if n := activeEntries(t, a, out.BillID); n != 1 {
			t.Errorf("%d active entries, want 1", n)
		}
	})
	t.Run("other fields post nothing", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		out := post(t, a, body(insertVendor(t, a), nil))
		patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
		patch(t, a, out.BillID, map[string]any{"notes": "n", "bill_number": "B-2", "due_date": "2026-03-01", "status": "OPEN"})
		if es := billEntries(t, a, out.BillID); len(es) != 1 {
			t.Errorf("%d entries, want 1", len(es))
		}
	})
}

func TestOpenToDraftReverses(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	got := patch(t, a, out.BillID, map[string]any{"status": "DRAFT"})
	if got.Status != "DRAFT" {
		t.Errorf("status %s", got.Status)
	}
	es := billEntries(t, a, out.BillID)
	if len(es) != 2 || !find(t, es, "Bill "+out.BillID.String()+" opened").reversed {
		t.Fatalf("entries %+v", es)
	}
	find(t, es, "Bill "+out.BillID.String()+" returned to draft")
	wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "0")
	wantD(t, "net 6000", netBase(t, a, out.BillID, "6000"), "0")
	// A DRAFT edit posts nothing; opening again posts a new entry.
	patch(t, a, out.BillID, map[string]any{"line_items": []any{line(2, 1000)}})
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	if n := activeEntries(t, a, out.BillID); n != 1 {
		t.Errorf("%d active entries, want 1", n)
	}
	wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "-2000")
}

// PAID records a payment (TestPatchToPaidRecordsPayment, PORT-NOTES 165).
func TestPatchToPartialRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ from, to, detail string }{
		{"DRAFT", "PARTIAL", "PARTIAL comes from recording a payment"},
		{"OPEN", "PARTIAL", "PARTIAL comes from recording a payment"},
	} {
		t.Run(c.from+" to "+c.to, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			out := openBill(t, a, c.from, nil)
			before := countRows(t, a, "journal_entries")
			wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": c.to, "notes": "x"}),
				http.StatusBadRequest, "bill_status_not_settable", c.detail, "", "")
			if got := get(t, a, out.BillID); got.Status != c.from || got.Notes != nil {
				t.Errorf("stored %s %q", got.Status, deref(got.Notes))
			}
			wantJournalRows(t, a, before)
		})
	}
}

func TestPatchToVoidUsesVoid(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"notes": "n"}))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	got := patch(t, a, out.BillID, map[string]any{"status": "VOID"})
	if got.Status != "VOID" || deref(got.Notes) != "n\n[voided] status set to VOID via update" {
		t.Errorf("status %s notes %q", got.Status, deref(got.Notes))
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "0")
	es := billEntries(t, a, out.BillID)
	if len(es) != 2 || !find(t, es, "Bill "+out.BillID.String()+" opened").reversed {
		t.Fatalf("entries %+v", es)
	}
	find(t, es, "Void of bill "+out.BillID.String())
	wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "0")
	// A DRAFT goes to VOID the same way, with nothing to reverse.
	d := post(t, a, body(insertVendor(t, a), nil))
	if got := patch(t, a, d.BillID, map[string]any{"status": "VOID"}); got.Status != "VOID" || deref(got.Notes) != "\n[voided] status set to VOID via update" {
		t.Errorf("draft: status %s notes %q", got.Status, deref(got.Notes))
	}
	if es := billEntries(t, a, d.BillID); len(es) != 0 {
		t.Errorf("draft: %d entries", len(es))
	}
}

func TestVoidBillReversesIssue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"currency": "USD", "issue_date": "2026-01-10"}))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	got := void(t, a, out.BillID, map[string]any{"reason": "duplicate"})
	if got.Status != "VOID" || got.BalanceDue.Decimal().Sign() != 0 {
		t.Errorf("status %s balance %s", got.Status, got.BalanceDue.Decimal())
	}
	es := billEntries(t, a, out.BillID)
	if len(es) != 2 || !find(t, es, "Bill "+out.BillID.String()+" opened").reversed {
		t.Fatalf("entries %+v", es)
	}
	if v := find(t, es, "Void of bill "+out.BillID.String()); v.lines["2000"] != "1000.0000/0.0000 16000000.0000/0.0000" {
		t.Errorf("reversal 2000 line %q", v.lines["2000"])
	}
	wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "0")
	wantD(t, "net 6000", netBase(t, a, out.BillID, "6000"), "0")
}

// A bill opened before this change has no issue entry: voiding it is refused
// until the back-post has run.
func TestVoidWithoutIssueEntryRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"OPEN", "PARTIAL"} {
		out := post(t, a, body(insertVendor(t, a), nil))
		setStatus(t, a, out.BillID, status)
		wantProblem(t, a.Do(t, http.MethodPost, voidPath(out.BillID), map[string]any{}), http.StatusBadRequest,
			"ledger_refused", "bill "+out.BillID.String()+" has no issue entry — run api backpost-ledger", "", "")
		if got := get(t, a, out.BillID); got.Status != status {
			t.Errorf("%s: status %s", status, got.Status)
		}
	}
	wantJournalRows(t, a, 0)
}

func TestDraftBillNeverPosts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	patch(t, a, out.BillID, map[string]any{"line_items": []any{line(2, 500)}, "currency": "USD", "notes": "n"})
	patch(t, a, out.BillID, map[string]any{"status": "DRAFT", "issue_date": "2026-01-02"})
	if err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error { return bills.DeleteBill(t.Context(), tx, out.BillID) }); err != nil {
		t.Fatal(err)
	}
	wantJournalRows(t, a, 0)
	if n := countRows(t, a, "journal_lines"); n != 0 {
		t.Errorf("%d journal lines", n)
	}
}

// Two opens at once post one entry: the second waits on the bill's row lock,
// then finds the bill OPEN with nothing to re-post.
func TestConcurrentOpenPostsOnce(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	ctx := context.Background()
	open := bills.BillUpdate{Status: platform.NotNull[string]{Set: true, Value: "OPEN"}}

	first, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if _, err := bills.UpdateBill(ctx, first, base, out.BillID, open); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- db.InTx(ctx, a.Pool, func(tx pgx.Tx) error {
			_, err := bills.UpdateBill(ctx, tx, base, out.BillID, open)
			return err
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := a.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second open never waited on a lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil && !errors.Is(err, bills.ErrBilling) {
		t.Errorf("second open: %v", err)
	}
	if es := billEntries(t, a, out.BillID); len(es) != 1 {
		t.Errorf("%d entries, want 1", len(es))
	}
}

func wantD(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}
