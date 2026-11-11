package invoices

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
)

// Go's own (PORT-NOTES 162): finalize posts the issue entry (Dr 1100 / Cr
// revenue) and void reverses it.

// base is the ledger's base currency in these tests (apitest seeds X→IDR rates).
const base = "IDR"

// jLine is a journal line as the tests read it.
type jLine struct {
	code                                 string
	debit, credit, baseDebit, baseCredit decimal.Decimal
	currency                             string
	rate                                 decimal.Decimal
}

// jEntry is an INVOICE journal entry with its lines.
type jEntry struct {
	id         uuid.UUID
	memo       string
	postedAt   time.Time
	reversedBy *uuid.UUID
	lines      []jLine
}

func (e jEntry) line(t *testing.T, code string) jLine {
	t.Helper()
	for _, l := range e.lines {
		if l.code == code {
			return l
		}
	}
	t.Fatalf("entry %s has no %s line: %+v", e.id, code, e.lines)
	return jLine{}
}

// invoiceEntries are the journal entries whose source is invoice id, oldest
// first, with their lines by code.
func invoiceEntries(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []jEntry {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT entry_id, COALESCE(memo, ''), posted_at, reversed_by_entry_id
FROM journal_entries WHERE source_type = 'INVOICE' AND source_id = $1 ORDER BY posted_at, entry_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []jEntry
	for rows.Next() {
		var e jEntry
		if err := rows.Scan(&e.id, &e.memo, &e.postedAt, &e.reversedBy); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		lr, err := pool.Query(t.Context(), `SELECT a.code, l.debit, l.credit, l.base_amount_debit, l.base_amount_credit, l.currency, l.fx_rate
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id WHERE l.entry_id = $1 ORDER BY a.code`, out[i].id)
		if err != nil {
			t.Fatal(err)
		}
		for lr.Next() {
			var l jLine
			if err := lr.Scan(&l.code, &l.debit, &l.credit, &l.baseDebit, &l.baseCredit, &l.currency, &l.rate); err != nil {
				t.Fatal(err)
			}
			out[i].lines = append(out[i].lines, l)
		}
		if err := lr.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// journalRows is the number of journal entries and lines in the database.
func journalRows(t *testing.T, pool *pgxpool.Pool) (entries, lines int) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM journal_entries), (SELECT count(*) FROM journal_lines)`).
		Scan(&entries, &lines); err != nil {
		t.Fatal(err)
	}
	return entries, lines
}

func wantNoJournal(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if e, l := journalRows(t, pool); e != 0 || l != 0 {
		t.Errorf("%d journal entries and %d lines, want none", e, l)
	}
}

func wantD(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

// insertItem stores a sold item whose revenue account has code ("" for
// none) and returns its id.
func insertItem(t *testing.T, pool *pgxpool.Pool, code string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var c *string
	if code != "" {
		c = &code
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO items (item_id, name, item_type, revenue_account_id)
VALUES ($1, 'Item', 'SERVICE', (SELECT account_id FROM chart_of_accounts WHERE code = $2))`, id, c); err != nil {
		t.Fatal(err)
	}
	return id
}

func itemLine(item uuid.UUID, price string) map[string]any {
	l := lineBody(price)
	l["item_id"] = item
	return l
}

func void(t *testing.T, a *apitest.App, id uuid.UUID) *httpResult {
	t.Helper()
	rec := a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{"reason": "mistake"})
	return &httpResult{code: rec.Code, body: rec.Body.String()}
}

type httpResult struct {
	code int
	body string
}

// addRate stores a from→IDR rate as of the date.
func addRate(t *testing.T, pool *pgxpool.Pool, from, rate, on string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ($1, 'IDR', $2::numeric, $3::date, 'TEST')`, from, rate, on); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizePostsIssueEntry(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	support := insertItem(t, a.Pool, ledger.SupportCode)
	inv := post(t, a, map[string]any{
		"customer_id": c, "currency": "IDR", "invoice_type": "MILESTONE", "issue_date": "2026-01-05",
		"line_items": []any{itemLine(support, "600"), lineBody("400")},
	})
	out := finalize(t, a, inv.InvoiceID)
	if out.Status != "OPEN" {
		t.Fatalf("status = %s", out.Status)
	}
	es := invoiceEntries(t, a.Pool, inv.InvoiceID)
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1", len(es))
	}
	e := es[0]
	if len(e.lines) != 3 {
		t.Fatalf("lines %+v", e.lines)
	}
	ar, fixed, sup := e.line(t, ledger.ReceivableCode), e.line(t, ledger.FixedFeeCode), e.line(t, ledger.SupportCode)
	wantD(t, "1100 debit", ar.debit, "1000")
	wantD(t, "1100 base debit", ar.baseDebit, "1000")
	wantD(t, "4100 credit", sup.credit, "600")
	wantD(t, "4100 base credit", sup.baseCredit, "600")
	wantD(t, "4000 credit", fixed.credit, "400")
	wantD(t, "4000 base credit", fixed.baseCredit, "400")
	if want := "Invoice " + *out.InvoiceNumber + " issued"; e.memo != want {
		t.Errorf("memo %q, want %q", e.memo, want)
	}
	if want := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC); !e.postedAt.Equal(want) {
		t.Errorf("posted_at %s, want %s", e.postedAt, want)
	}
}

func TestRevenueSharesByBillingMode(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, c := range []struct{ typ, code string }{
		{"RECURRING", ledger.SupportCode},
		{"USAGE", ledger.UsageCode},
		{"MILESTONE", ledger.FixedFeeCode},
		{"RETAINER", ledger.FixedFeeCode},
	} {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			if got := ModeRevenueCode(c.typ); got != c.code {
				t.Errorf("ModeRevenueCode(%s) = %s, want %s", c.typ, got, c.code)
			}
			cust := insertCustomer(t, a.Pool, "NONE", "", false)
			inv := post(t, a, createBody(cust, "250", map[string]any{"invoice_type": c.typ}))
			got := shareCodes(t, a.Pool, inv.InvoiceID)
			if len(got) != 1 || got[c.code] != "250" {
				t.Errorf("shares %v, want %s: 250", got, c.code)
			}
		})
	}
}

// shareCodes is RevenueShares of invoice id as code → amount.
func shareCodes(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		inv, err := invoicesdb.New(tx).GetInvoice(t.Context(), id)
		if err != nil {
			return err
		}
		shares, err := RevenueShares(t.Context(), tx, inv)
		if err != nil {
			return err
		}
		ids := make([]int32, len(shares))
		for i, s := range shares {
			ids[i] = s.AccountID
		}
		accts, err := ledger.AccountsByID(t.Context(), tx, ids)
		if err != nil {
			return err
		}
		for _, s := range shares {
			out[accts[s.AccountID].Code] = s.Amount.String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRevenueSharesDiscountAndGrossUp(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	support := insertItem(t, a.Pool, ledger.SupportCode)
	inv := post(t, a, map[string]any{
		"customer_id": c, "currency": "USD",
		"line_items":    []any{itemLine(support, "600"), lineBody("400")},
		"discount_type": "PERCENT", "discount_value": "10",
		"wht_type": "PPH23", "wht_gross_up": true,
	})
	// 900 after the discount, grossed up for 2%: 900 / 0.98 = 918.37 (cents).
	wantDec4(t, "amount", p4(inv.Amount), "918.37")
	got := shareCodes(t, a.Pool, inv.InvoiceID)
	// 918.37 × 0.6 = 551.022, × 0.4 = 367.348: the shares sum to the amount.
	if got[ledger.SupportCode] != "551.022" || got[ledger.FixedFeeCode] != "367.348" || len(got) != 2 {
		t.Errorf("shares %v", got)
	}

	// Three equal groups sharing 100: 33.3333 each leaves 0.0001, which the
	// largest group (the first on a tie, in line order) takes.
	usage := insertItem(t, a.Pool, ledger.UsageCode)
	inv3 := post(t, a, map[string]any{
		"customer_id": c, "currency": "IDR",
		"line_items": []any{at(itemLine(usage, "100"), 0), at(lineBody("100"), 1), at(itemLine(support, "100"), 2)},
	})
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET amount = 100, balance_due = 100 WHERE invoice_id = $1`, inv3.InvoiceID); err != nil {
		t.Fatal(err)
	}
	got = shareCodes(t, a.Pool, inv3.InvoiceID)
	if got[ledger.UsageCode] != "33.3334" || got[ledger.FixedFeeCode] != "33.3333" || got[ledger.SupportCode] != "33.3333" {
		t.Errorf("remainder shares %v", got)
	}
}

func TestRevenueSharesNoLines(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": c, "currency": "IDR", "amount": "750", "milestone_ref": "Phase 1",
	}), http.StatusCreated)
	if got := shareCodes(t, a.Pool, inv.InvoiceID); len(got) != 1 || got[ledger.FixedFeeCode] != "750" {
		t.Errorf("shares %v", got)
	}
	finalize(t, a, inv.InvoiceID)
	es := invoiceEntries(t, a.Pool, inv.InvoiceID)
	if len(es) != 1 {
		t.Fatalf("%d entries", len(es))
	}
	wantD(t, "4000 credit", es[0].line(t, ledger.FixedFeeCode).credit, "750")
	// A milestone has no number: the memo names the invoice by id.
	if want := "Invoice " + inv.InvoiceID.String() + " issued"; es[0].memo != want {
		t.Errorf("memo %q, want %q", es[0].memo, want)
	}
}

func TestFinalizeUSDUsesIssueDateRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// apitest has USD 16000 on 01-01; 01-10 is after the issue date.
	addRate(t, a.Pool, "USD", "17000", "2026-01-10")
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "10", map[string]any{"currency": "USD", "issue_date": "2026-01-05"}))
	finalize(t, a, inv.InvoiceID)
	es := invoiceEntries(t, a.Pool, inv.InvoiceID)
	if len(es) != 1 {
		t.Fatalf("%d entries", len(es))
	}
	ar := es[0].line(t, ledger.ReceivableCode)
	wantD(t, "rate", ar.rate, "16000")
	wantD(t, "1100 base debit", ar.baseDebit, "160000")
	if ar.currency != "USD" {
		t.Errorf("currency %s", ar.currency)
	}
	if want := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC); !es[0].postedAt.Equal(want) {
		t.Errorf("posted_at %s, want %s", es[0].postedAt, want)
	}
}

// An invoice PATCHed to no issue date posts at its created_at date, at that
// date's rate.
func TestFinalizeNullIssueDateUsesCreatedAt(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	addRate(t, a.Pool, "USD", "17000", "2026-01-10")
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "10", map[string]any{"currency": "USD"}))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET issue_date = NULL, created_at = '2026-01-07 15:30:00' WHERE invoice_id = $1`, inv.InvoiceID); err != nil {
		t.Fatal(err)
	}
	finalize(t, a, inv.InvoiceID)
	es := invoiceEntries(t, a.Pool, inv.InvoiceID)
	if len(es) != 1 {
		t.Fatalf("%d entries", len(es))
	}
	wantD(t, "rate", es[0].line(t, ledger.ReceivableCode).rate, "16000")
	if want := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC); !es[0].postedAt.Equal(want) {
		t.Errorf("posted_at %s, want %s", es[0].postedAt, want)
	}
}

// A zero-value (or fully discounted) invoice is settled to PAID at finalize
// and posts nothing: the ledger refuses an amount of 0.
func TestFinalizeZeroInvoicePostsNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, inv := range []InvoiceOut{
		zeroValueDraft(t, a, c),
		post(t, a, createBody(c, "100", map[string]any{"discount_type": "PERCENT", "discount_value": "100"})),
	} {
		wantDec4(t, "amount", p4(inv.Amount), "0")
		if out := finalize(t, a, inv.InvoiceID); out.Status != "PAID" {
			t.Errorf("status = %s", out.Status)
		}
	}
	wantNoJournal(t, a.Pool)
}

func TestFinalizeTemplateRefusedPostsNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(tpl.InvoiceID, "finalize"), nil), http.StatusBadRequest, "invoice_is_template",
		"cannot finalize a recurring template", "", "")
	wantNoJournal(t, a.Pool)
}

func TestFinalizeRefusesNonIncomeAccount(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	asset := insertItem(t, a.Pool, ledger.ReceivableCode)
	inv := post(t, a, map[string]any{"customer_id": c, "currency": "IDR", "line_items": []any{itemLine(asset, "100")}})
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(inv.InvoiceID, "finalize"), nil), http.StatusBadRequest, "ledger_refused",
		"account 1100 (ASSET) cannot take revenue; set the item's revenue account to an INCOME account", "", "")
	if got := get(t, a, inv.InvoiceID); got.Status != "DRAFT" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
	wantNoJournal(t, a.Pool)
}

// An inactive revenue account (the item's, or the billing mode's) is refused
// before anything posts.
func TestFinalizeRefusesInactiveAccount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, code string }{{"item account", ledger.SupportCode}, {"mode account", ledger.FixedFeeCode}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			cust := insertCustomer(t, a.Pool, "NONE", "", false)
			line := lineBody("100")
			if c.code == ledger.SupportCode {
				line = itemLine(insertItem(t, a.Pool, c.code), "100")
			}
			inv := post(t, a, map[string]any{"customer_id": cust, "currency": "IDR", "invoice_type": "MILESTONE", "line_items": []any{line}})
			if _, err := a.Pool.Exec(t.Context(), `UPDATE chart_of_accounts SET active = false WHERE code = $1`, c.code); err != nil {
				t.Fatal(err)
			}
			wantProblem(t, a.Do(t, http.MethodPost, actionPath(inv.InvoiceID, "finalize"), nil), http.StatusBadRequest, "ledger_refused",
				"account "+c.code+" is inactive and cannot take revenue; reactivate it or choose another revenue account", "", "")
			if got := get(t, a, inv.InvoiceID); got.Status != "DRAFT" {
				t.Errorf("status = %s after a refusal", got.Status)
			}
			wantNoJournal(t, a.Pool)
		})
	}
}

// A DRAFT whose balance_due is not its amount is refused rather than issued
// with a receivable that disagrees with the balance (or settled to PAID).
func TestFinalizeRefusesBalanceNotAmount(t *testing.T) {
	t.Parallel()
	for _, balance := range []string{"40", "0"} {
		t.Run(balance, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			c := insertCustomer(t, a.Pool, "NONE", "", false)
			inv := post(t, a, createBody(c, "100", nil))
			if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET balance_due = $2::numeric WHERE invoice_id = $1`, inv.InvoiceID, balance); err != nil {
				t.Fatal(err)
			}
			wantProblem(t, a.Do(t, http.MethodPost, actionPath(inv.InvoiceID, "finalize"), nil), http.StatusBadRequest, "ledger_refused",
				"invoice "+inv.InvoiceID.String()+" balance_due "+balance+".0000 is not its amount 100.0000; cannot issue it", "", "")
			if got := get(t, a, inv.InvoiceID); got.Status != "DRAFT" {
				t.Errorf("status = %s after a refusal", got.Status)
			}
			wantNoJournal(t, a.Pool)
		})
	}
}

func TestFinalizeMissingRateLeavesDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "100", map[string]any{"currency": "EUR"}))
	p := apitest.Decode[problem](t, a.Do(t, http.MethodPost, actionPath(inv.InvoiceID, "finalize"), nil), http.StatusBadRequest)
	if p.Code != "fx_rate_missing" {
		t.Errorf("code %q: %+v", p.Code, p)
	}
	if got := get(t, a, inv.InvoiceID); got.Status != "DRAFT" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
	wantNoJournal(t, a.Pool)
}

// Two finalizes of one invoice at once: the second waits on the invoice's row
// lock, then finds it OPEN. Exactly one issue entry is posted.
func TestConcurrentFinalizePostsOnce(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "100", nil))
	ctx := context.Background()

	first, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if _, err := FinalizeInvoice(ctx, first, base, inv.InvoiceID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- db.InTx(ctx, a.Pool, func(tx pgx.Tx) error {
			_, err := FinalizeInvoice(ctx, tx, base, inv.InvoiceID)
			return err
		})
	}()
	// Wait until the second finalize is blocked on a lock the first holds.
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
			t.Fatal("the second finalize never waited on a lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-done
	if !errors.Is(err, ErrInvoicing) || RefusalCode(err) != "invoice_not_draft" {
		t.Errorf("second finalize: %v, want invoice_not_draft", err)
	}
	if es := invoiceEntries(t, a.Pool, inv.InvoiceID); len(es) != 1 {
		t.Errorf("%d issue entries, want 1", len(es))
	}
}

func TestVoidReversesIssueEntry(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "100", map[string]any{"currency": "USD"}))
	finalize(t, a, inv.InvoiceID)
	if r := void(t, a, inv.InvoiceID); r.code != http.StatusOK {
		t.Fatalf("void: %d %s", r.code, r.body)
	}
	if got := get(t, a, inv.InvoiceID); got.Status != "VOID" {
		t.Errorf("status = %s", got.Status)
	}
	es := invoiceEntries(t, a.Pool, inv.InvoiceID)
	if len(es) != 2 {
		t.Fatalf("%d entries, want the issue entry and its reversal", len(es))
	}
	issue, rev := es[0], es[1]
	if issue.reversedBy == nil || *issue.reversedBy != rev.id {
		t.Errorf("issue reversed_by_entry_id = %v, want %s", issue.reversedBy, rev.id)
	}
	if want := "Void of invoice " + inv.InvoiceID.String(); rev.memo != want {
		t.Errorf("memo %q, want %q", rev.memo, want)
	}
	// The trial balance nets receivable and revenue to 0.
	var arNet, revNet decimal.Decimal
	if err := a.Pool.QueryRow(t.Context(), `SELECT
 COALESCE(sum(l.base_amount_debit - l.base_amount_credit) FILTER (WHERE a.code = '1100'), 0),
 COALESCE(sum(l.base_amount_debit - l.base_amount_credit) FILTER (WHERE a.type = 'INCOME'), 0)
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id`).Scan(&arNet, &revNet); err != nil {
		t.Fatal(err)
	}
	if !arNet.IsZero() || !revNet.IsZero() {
		t.Errorf("after void: receivable nets %s, revenue %s; want 0", arNet, revNet)
	}
}

// clearedPayment stores a CLEARED manual payment of amount (withholding wht)
// against invoice id.
func clearedPayment(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, currency, amount, wht string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO payments
(payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status, wht_amount)
VALUES ($1, $2, $3::numeric, $4, 'Payer', '2026-01-15', 'MANUAL', 'CLEARED', $5::numeric)`,
		uuid.New(), id, amount, currency, wht); err != nil {
		t.Fatal(err)
	}
}

func TestVoidWithClearedPaymentRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, amount, wht string }{
		{"partial payment", "40", "0"},
		{"withholding only", "0", "2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			cust := insertCustomer(t, a.Pool, "NONE", "", false)
			inv := post(t, a, createBody(cust, "100", nil))
			finalize(t, a, inv.InvoiceID)
			clearedPayment(t, a.Pool, inv.InvoiceID, "IDR", c.amount, c.wht)
			if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET status = 'PARTIAL', balance_due = 60 WHERE invoice_id = $1`, inv.InvoiceID); err != nil {
				t.Fatal(err)
			}
			e0, l0 := journalRows(t, a.Pool)
			wantProblem(t, a.Do(t, http.MethodPost, actionPath(inv.InvoiceID, "void"), map[string]any{"reason": "x"}), http.StatusBadRequest,
				"invoice_has_payments", "cannot void an invoice with cleared payments — reverse payments first", "", "")
			got := get(t, a, inv.InvoiceID)
			if got.Status != "PARTIAL" {
				t.Errorf("status = %s after a refusal", got.Status)
			}
			wantDec4(t, "balance_due", p4(got.BalanceDue), "60")
			if e, l := journalRows(t, a.Pool); e != e0 || l != l0 {
				t.Errorf("journal %d/%d, was %d/%d", e, l, e0, l0)
			}
			if es := invoiceEntries(t, a.Pool, inv.InvoiceID); len(es) != 1 || es[0].reversedBy != nil {
				t.Errorf("issue entry %+v, want one, not reversed", es)
			}
		})
	}
}

func TestVoidDraftAndTemplatePostNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	draft := post(t, a, createBody(c, "100", nil))
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	for _, id := range []uuid.UUID{draft.InvoiceID, tpl.InvoiceID} {
		if r := void(t, a, id); r.code != http.StatusOK {
			t.Errorf("void %s: %d %s", id, r.code, r.body)
		}
	}
	wantNoJournal(t, a.Pool)
}

// An issued invoice with no issue entry (issued before Go posted at
// finalize) is refused until the back-post has run.
func TestVoidWithoutIssueEntryRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "SENT", "100")
	wantProblem(t, a.Do(t, http.MethodPost, actionPath(id, "void"), map[string]any{"reason": "x"}), http.StatusBadRequest,
		"ledger_refused", "invoice "+id.String()+" has no issue entry — run api backpost-ledger", "", "")
	if got := get(t, a, id); got.Status != "SENT" {
		t.Errorf("status = %s after a refusal", got.Status)
	}
	// One with nothing to reverse is voided: a zero-amount issued invoice.
	zero := insertWithStatus(t, a.Pool, "OPEN", "0")
	if r := void(t, a, zero); r.code != http.StatusOK {
		t.Errorf("void zero: %d %s", r.code, r.body)
	}
	wantNoJournal(t, a.Pool)
}

// Voiding a VOID invoice again (as Python allows) reverses nothing more.
func TestVoidTwicePostsOneReversal(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	inv := post(t, a, createBody(c, "100", nil))
	finalize(t, a, inv.InvoiceID)
	for range 2 {
		if r := void(t, a, inv.InvoiceID); r.code != http.StatusOK {
			t.Fatalf("void: %d %s", r.code, r.body)
		}
	}
	if es := invoiceEntries(t, a.Pool, inv.InvoiceID); len(es) != 2 {
		t.Errorf("%d entries, want the issue entry and one reversal", len(es))
	}
}

func TestRecurringAndUsageDraftsPostNothing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	if child := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-01-01"); child.Status != "DRAFT" {
		t.Errorf("generated %s", child.Status)
	}
	usage := insertUsage(t, a.Pool, "DRAFT", `{"cut_off_day": 25}`)
	if out := lockUsageRoute(t, a, usage, "1000", "2026-01-25"); out.Status != "DRAFT" {
		t.Errorf("locked %s", out.Status)
	}
	wantNoJournal(t, a.Pool)
}

// at is line l at position p (lines are ordered by position).
func at(l map[string]any, p int) map[string]any {
	l["position"] = p
	return l
}
