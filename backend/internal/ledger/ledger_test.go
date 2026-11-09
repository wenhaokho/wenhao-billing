package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const base = "IDR"

// payDay is after apitest's seed rates (USD 16000, SGD 12000 on 2026-01-01).
var payDay = time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return apitest.New(t, func(huma.API, apitest.Deps) {}).Pool
}

// begin is a transaction rolled back when the test ends unless committed.
func begin(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func cleared(amount, currency string) Payment {
	return Payment{ID: uuid.New(), Status: "CLEARED", Amount: dec(amount), Currency: currency, Date: payDay}
}

func invoice(currency, balance string) Invoice {
	return Invoice{ID: uuid.New(), Currency: currency, BalanceDue: dec(balance)}
}

// post posts p against inv and commits.
func post(t *testing.T, pool *pgxpool.Pool, p Payment, inv Invoice, withheld string) Entry {
	t.Helper()
	tx := begin(t, pool)
	e, err := PostPaymentCleared(context.Background(), tx, base, p, inv, dec(withheld))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

// want is one expected line: account code, debit, credit, currency, base
// debit, base credit, rate.
type want struct{ code, debit, credit, ccy, baseDebit, baseCredit, rate string }

func accountIDs(t *testing.T, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
},
) map[string]int32 {
	t.Helper()
	rows, err := q.Query(context.Background(), `SELECT code, account_id FROM chart_of_accounts`)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int32{}
	for rows.Next() {
		var code string
		var id int32
		if err := rows.Scan(&code, &id); err != nil {
			t.Fatal(err)
		}
		ids[code] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// checkLines compares e's lines, in order, with wants, and checks the stored
// entry and lines are e's.
func checkLines(t *testing.T, pool *pgxpool.Pool, e Entry, wants []want) {
	t.Helper()
	ids := accountIDs(t, pool)
	if len(e.Lines) != len(wants) {
		t.Fatalf("%d lines, want %d: %+v", len(e.Lines), len(wants), e.Lines)
	}
	for i, w := range wants {
		l := e.Lines[i]
		ok := l.AccountID == ids[w.code] && l.Currency == w.ccy &&
			l.Debit.Equal(dec(w.debit)) && l.Credit.Equal(dec(w.credit)) &&
			l.BaseDebit.Equal(dec(w.baseDebit)) && l.BaseCredit.Equal(dec(w.baseCredit)) &&
			l.FXRate.Equal(dec(w.rate))
		if !ok {
			t.Errorf("line %d = %+v, want %+v", i, l, w)
		}
	}
	checkStored(t, pool, e)
}

// checkStored checks the database holds exactly e: its entry row and lines.
func checkStored(t *testing.T, pool *pgxpool.Pool, e Entry) {
	t.Helper()
	ctx := context.Background()
	var sourceType string
	var sourceID uuid.UUID
	var memo *string
	var postedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT source_type, source_id, memo, posted_at FROM journal_entries WHERE entry_id = $1`, e.ID).
		Scan(&sourceType, &sourceID, &memo, &postedAt); err != nil {
		t.Fatalf("entry %s: %v", e.ID, err)
	}
	if sourceType != e.SourceType || sourceID != e.SourceID || memo == nil || e.Memo == nil || *memo != *e.Memo || postedAt == nil {
		t.Errorf("stored entry = %s %s %v %v, want %s %s %v", sourceType, sourceID, memo, postedAt, e.SourceType, e.SourceID, e.Memo)
	}
	stored := storedLines(t, pool, e.ID)
	if len(stored) != len(e.Lines) {
		t.Fatalf("%d stored lines, want %d", len(stored), len(e.Lines))
	}
	for _, l := range e.Lines {
		s, ok := stored[l.AccountID]
		if !ok || !s.Debit.Equal(l.Debit) || !s.Credit.Equal(l.Credit) || s.Currency != l.Currency ||
			!s.BaseDebit.Equal(l.BaseDebit) || !s.BaseCredit.Equal(l.BaseCredit) || !s.FXRate.Equal(l.FXRate) {
			t.Errorf("stored line for account %d = %+v, want %+v", l.AccountID, s, l)
		}
	}
}

// storedLines are an entry's lines by account (an entry posts to an account once).
func storedLines(t *testing.T, pool *pgxpool.Pool, entryID uuid.UUID) map[int32]Line {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT account_id, debit, credit, currency, base_amount_debit, base_amount_credit, fx_rate
FROM journal_lines WHERE entry_id = $1`, entryID)
	if err != nil {
		t.Fatal(err)
	}
	lines := map[int32]Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.AccountID, &l.Debit, &l.Credit, &l.Currency, &l.BaseDebit, &l.BaseCredit, &l.FXRate); err != nil {
			t.Fatal(err)
		}
		lines[l.AccountID] = l
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// countRows is the number of journal entries and lines q sees.
func countRows(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
},
) (entries, lines int) {
	t.Helper()
	if err := q.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM journal_entries), (SELECT count(*) FROM journal_lines)`).
		Scan(&entries, &lines); err != nil {
		t.Fatal(err)
	}
	return entries, lines
}

// refused checks err is an ErrLedger with text msg, and that tx (the
// posting's transaction) holds no journal rows.
func refused(t *testing.T, tx pgx.Tx, err error, msg string) {
	t.Helper()
	if !errors.Is(err, ErrLedger) {
		t.Fatalf("err = %v, want ErrLedger", err)
	}
	if err.Error() != msg {
		t.Errorf("text = %q, want %q", err.Error(), msg)
	}
	if e, l := countRows(t, tx); e != 0 || l != 0 {
		t.Errorf("%d entries and %d lines written, want none", e, l)
	}
}

func TestSameCurrencyIDR(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	p, inv := cleared("1000000", "IDR"), invoice("IDR", "1000000")
	e := post(t, pool, p, inv, "0")
	if e.SourceType != "PAYMENT" || e.SourceID != p.ID || e.Memo == nil ||
		*e.Memo != "Payment "+p.ID.String()+" cleared against invoice "+inv.ID.String() {
		t.Errorf("entry = %s %s %v", e.SourceType, e.SourceID, e.Memo)
	}
	checkLines(t, pool, e, []want{
		{BankCode, "1000000", "0", "IDR", "1000000", "0", "1"},
		{ReceivableCode, "0", "1000000", "IDR", "0", "1000000", "1"},
	})
}

func TestSameCurrencyUSD(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	e := post(t, pool, cleared("100", "USD"), invoice("USD", "100"), "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000.0000", "0", "16000"},
		{ReceivableCode, "0", "100", "USD", "0", "1600000.0000", "16000"},
	})
}

// At 16000.00003 per USD, 1 USD and 1 USD each convert to 16000.0000
// (16000.00003 rounds down) but 2 USD converts to 32000.0001 (32000.00006
// rounds up): AR is relieved by the debits' 32000.0000, not its own 32000.0001.
func TestSameCurrencyWithholdingRelievesExactDebitBase(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000.00003, '2026-03-01', 'TEST')`); err != nil {
		t.Fatal(err)
	}
	p := cleared("1", "USD")
	p.Date = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	e := post(t, pool, p, invoice("USD", "2"), "1")
	checkLines(t, pool, e, []want{
		{BankCode, "1", "0", "USD", "16000.0000", "0", "16000.00003"},
		{PrepaidTaxCode, "1", "0", "USD", "16000.0000", "0", "16000.00003"},
		{ReceivableCode, "0", "2", "USD", "0", "32000.0000", "16000.00003"},
	})
}

func TestCrossCurrencyGain(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	e := post(t, pool, cleared("100", "USD"), invoice("IDR", "1550000"), "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000.0000", "0", "16000"},
		{ReceivableCode, "0", "1550000", "IDR", "0", "1550000.0000", "1"},
		{FXGainCode, "0", "50000", "IDR", "0", "50000.0000", "1"},
	})
}

func TestCrossCurrencyLoss(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	e := post(t, pool, cleared("100", "USD"), invoice("IDR", "1650000"), "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000.0000", "0", "16000"},
		{ReceivableCode, "0", "1650000", "IDR", "0", "1650000.0000", "1"},
		{FXLossCode, "50000", "0", "IDR", "50000.0000", "0", "1"},
	})
}

func TestCrossCurrencyExact(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	e := post(t, pool, cleared("100", "USD"), invoice("IDR", "1600000"), "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000.0000", "0", "16000"},
		{ReceivableCode, "0", "1600000", "IDR", "0", "1600000.0000", "1"},
	})
}

// diff = 1,600,000 bank + 50,000 withheld − 1,700,000 AR = −50,000: a loss
// of 50,000, not the 100,000 it would be without the withholding.
func TestCrossCurrencyWithholdingCountsInDiff(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	e := post(t, pool, cleared("100", "USD"), invoice("IDR", "1700000"), "50000")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000.0000", "0", "16000"},
		{PrepaidTaxCode, "50000", "0", "IDR", "50000", "0", "1"},
		{ReceivableCode, "0", "1700000", "IDR", "0", "1700000.0000", "1"},
		{FXLossCode, "50000", "0", "IDR", "50000.0000", "0", "1"},
	})
}

func TestRefusesNonCleared(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	p := cleared("100", "IDR")
	p.Status = "PENDING_MANUAL_REVIEW"
	_, err := PostPaymentCleared(context.Background(), tx, base, p, invoice("IDR", "100"), dec("0"))
	refused(t, tx, err, "refuse to post ledger for payment "+p.ID.String()+" with status 'PENDING_MANUAL_REVIEW' — only CLEARED may post")
}

func TestRefusesNegativeWithheld(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "IDR"), invoice("IDR", "100"), dec("-1.50"))
	refused(t, tx, err, "withheld tax cannot be negative (got -1.50)")
}

func TestRefusesCrossCurrencyZeroBalance(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	inv := invoice("IDR", "0.0000")
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "USD"), inv, dec("0"))
	refused(t, tx, err, "cross-currency settlement requires invoice balance_due > 0 (invoice "+inv.ID.String()+" balance=0.0000)")
}

// A payment amount with no positive side would break the one-sided line rule;
// Python fails on it with an AttributeError (PORT-NOTES).
func TestRefusesNonPositiveAmount(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	// A non-positive amount is a programming error (Python crashes), not a
	// refusal: it must not be routed to manual review as ErrLedger is.
	for _, amount := range []string{"0", "-5"} {
		p := cleared(amount, "IDR")
		_, err := PostPaymentCleared(context.Background(), tx, base, p, invoice("IDR", "100"), dec("0"))
		if err == nil || errors.Is(err, ErrLedger) {
			t.Fatalf("%s: err = %v, want a plain error (not ErrLedger)", amount, err)
		}
		if want := "ledger: payment " + p.ID.String() + " amount must be > 0 (got " + amount + ")"; err.Error() != want {
			t.Errorf("text = %q, want %q", err.Error(), want)
		}
		if e, l := countRows(t, tx); e != 0 || l != 0 {
			t.Errorf("%s: %d entries and %d lines written, want none", amount, e, l)
		}
	}
}

func TestRateMissingPassesThrough(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "EUR"), invoice("EUR", "100"), dec("0"))
	if !errors.Is(err, fx.ErrRateMissing) {
		t.Fatalf("err = %v, want fx.ErrRateMissing", err)
	}
	if want := "no fx rate for EUR->IDR on or before 2026-01-15"; err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
	if e, l := countRows(t, tx); e != 0 || l != 0 {
		t.Errorf("%d entries and %d lines written, want none", e, l)
	}
}

func TestMissingAccountCode(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	if _, err := tx.Exec(context.Background(), `UPDATE chart_of_accounts SET code = '1301' WHERE code = '1300'`); err != nil {
		t.Fatal(err)
	}
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "IDR"), invoice("IDR", "110"), dec("10"))
	refused(t, tx, err, "chart_of_accounts missing code 1300")
}

func TestReversalSwapsSidesAndLinks(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	orig := post(t, pool, cleared("100", "USD"), invoice("IDR", "1550000"), "0")
	tx := begin(t, pool)
	memo := "Reversal: bank returned the transfer"
	rev, err := PostReversal(context.Background(), tx, base, orig.ID, &memo)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rev.SourceType != orig.SourceType || rev.SourceID != orig.SourceID || rev.Memo == nil || *rev.Memo != memo || rev.ID == orig.ID {
		t.Errorf("reversal = %s %s %v %s", rev.SourceType, rev.SourceID, rev.Memo, rev.ID)
	}
	byAccount := map[int32]Line{}
	for _, l := range rev.Lines {
		byAccount[l.AccountID] = l
	}
	if len(byAccount) != len(orig.Lines) {
		t.Fatalf("%d reversal lines, want %d", len(rev.Lines), len(orig.Lines))
	}
	for _, o := range orig.Lines {
		r := byAccount[o.AccountID]
		if !r.Debit.Equal(o.Credit) || !r.Credit.Equal(o.Debit) || !r.BaseDebit.Equal(o.BaseCredit) ||
			!r.BaseCredit.Equal(o.BaseDebit) || !r.FXRate.Equal(o.FXRate) || r.Currency != o.Currency {
			t.Errorf("reversal of %+v = %+v", o, r)
		}
	}
	checkStored(t, pool, rev)
	var reversedBy *uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT reversed_by_entry_id FROM journal_entries WHERE entry_id = $1`, orig.ID).
		Scan(&reversedBy); err != nil {
		t.Fatal(err)
	}
	if reversedBy == nil || *reversedBy != rev.ID {
		t.Errorf("reversed_by_entry_id = %v, want %s", reversedBy, rev.ID)
	}
}

func TestReversalDefaultMemo(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	orig := post(t, pool, cleared("100", "IDR"), invoice("IDR", "100"), "0")
	for _, memo := range []*string{nil, new(string)} {
		tx := begin(t, pool)
		rev, err := PostReversal(context.Background(), tx, base, orig.ID, memo)
		if err != nil {
			t.Fatal(err)
		}
		if want := "Reversal of " + orig.ID.String(); rev.Memo == nil || *rev.Memo != want {
			t.Errorf("memo %v: reversal memo = %v, want %q", memo, rev.Memo, want)
		}
		_ = tx.Rollback(context.Background())
	}
}

func TestReverseTwiceFails(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	orig := post(t, pool, cleared("100", "IDR"), invoice("IDR", "100"), "0")
	tx := begin(t, pool)
	if _, err := PostReversal(context.Background(), tx, base, orig.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	tx2 := begin(t, pool)
	_, err := PostReversal(context.Background(), tx2, base, orig.ID, nil)
	if !errors.Is(err, ErrLedger) || err.Error() != "entry "+orig.ID.String()+" already reversed" {
		t.Fatalf("err = %v, want already reversed", err)
	}
	if e, _ := countRows(t, tx2); e != 2 {
		t.Errorf("%d entries, want the original and one reversal", e)
	}
}

func TestReverseUnknownFails(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	id := uuid.New()
	_, err := PostReversal(context.Background(), tx, base, id, nil)
	refused(t, tx, err, "entry "+id.String()+" not found")
}

// Transaction A reverses the entry and holds it; B then tries while A is
// open, and A commits only once B waits on a lock. B must then see A's
// reversal and refuse. Without the row lock B reads the entry as unreversed,
// posts a second reversal and overwrites reversed_by_entry_id.
func TestReverseConcurrentOnlyOnce(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	orig := post(t, pool, cleared("100", "IDR"), invoice("IDR", "100"), "0")

	txA := begin(t, pool)
	revA, err := PostReversal(ctx, txA, base, orig.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		txB, err := pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = txB.Rollback(ctx) }()
		if _, err = PostReversal(ctx, txB, base, orig.ID, nil); err == nil {
			err = txB.Commit(ctx)
		}
		done <- err
	}()

	var errB error
	finished := false
	for deadline := time.Now().Add(10 * time.Second); !finished; {
		if time.Now().After(deadline) {
			t.Fatal("transaction B neither finished nor waited on a lock")
		}
		select {
		case errB = <-done:
			finished = true
			continue
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid() AND wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !finished {
		select {
		case errB = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("transaction B did not finish after A committed")
		}
	}

	if !errors.Is(errB, ErrLedger) || errB.Error() != "entry "+orig.ID.String()+" already reversed" {
		t.Errorf("B: err = %v, want already reversed", errB)
	}
	var reversals int
	var reversedBy uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM journal_entries WHERE source_id = $1 AND entry_id <> $2),
(SELECT reversed_by_entry_id FROM journal_entries WHERE entry_id = $2)`, orig.SourceID, orig.ID).Scan(&reversals, &reversedBy); err != nil {
		t.Fatal(err)
	}
	if reversals != 1 || reversedBy != revA.ID {
		t.Errorf("%d reversals, reversed_by %s; want 1 and %s", reversals, reversedBy, revA.ID)
	}
}

func TestUnbalancedEntryRefused(t *testing.T) {
	t.Parallel()
	line := func(debit, credit, ccy, baseDebit, baseCredit string) Line {
		return Line{
			AccountID: 1, Debit: dec(debit), Credit: dec(credit), Currency: ccy,
			BaseDebit: dec(baseDebit), BaseCredit: dec(baseCredit), FXRate: dec("16000"),
		}
	}
	for _, c := range []struct {
		name  string
		lines []Line
		msg   string
	}{
		{"base off by 0.0001", []Line{
			line("100", "0", "USD", "1600000.0001", "0"),
			line("0", "100", "USD", "0", "1600000.0000"),
		}, "journal entry unbalanced in base currency: net=0.0001"},
		{"single currency off", []Line{
			line("100.0001", "0", "USD", "1600000.0000", "0"),
			line("0", "100.0000", "USD", "0", "1600000.0000"),
		}, "journal entry unbalanced in USD: net=0.0001"},
		{"cross currency balanced in base", []Line{
			line("100", "0", "USD", "1600000.0000", "0"),
			line("0", "1600000", "IDR", "0", "1600000.0000"),
		}, ""},
	} {
		err := checkBalanced(c.lines)
		switch {
		case c.msg == "" && err != nil:
			t.Errorf("%s: err = %v, want none", c.name, err)
		case c.msg != "" && (!errors.Is(err, ErrLedger) || err.Error() != c.msg):
			t.Errorf("%s: err = %v, want ErrLedger %q", c.name, err, c.msg)
		}
	}
}

func TestLinesAreOneSided(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	post(t, pool, cleared("100", "USD"), invoice("IDR", "1550000"), "0")
	post(t, pool, cleared("100", "USD"), invoice("IDR", "1700000"), "50000")
	post(t, pool, cleared("1000000", "IDR"), invoice("IDR", "1100000"), "100000")
	orig := post(t, pool, cleared("100", "USD"), invoice("USD", "100"), "0")
	tx := begin(t, pool)
	if _, err := PostReversal(context.Background(), tx, base, orig.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var total, bad int
	if err := pool.QueryRow(context.Background(), `SELECT count(*),
count(*) FILTER (WHERE NOT ((debit = 0 AND credit > 0 AND base_amount_debit = 0) OR (credit = 0 AND debit > 0 AND base_amount_credit = 0)))
FROM journal_lines`).Scan(&total, &bad); err != nil {
		t.Fatal(err)
	}
	if total != 14 || bad != 0 {
		t.Errorf("%d lines, %d not one-sided; want 14 and 0", total, bad)
	}
}

// pyStr prints like Python's str(Decimal).
func TestPyStr(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"0": "0", "0.0000": "0.0000", "-1.50": "-1.50", "1600000.0001": "1600000.0001",
		"-0.0001": "-0.0001", "1000": "1000", "1E+3": "1E+3", "0.0000001": "1E-7", "0.000001": "0.000001",
		"1.5E+4": "1.5E+4",
	} {
		if got := platform.PyDecimalStr(dec(in)); got != want {
			t.Errorf("platform.PyDecimalStr(%s) = %q, want %q", in, got, want)
		}
	}
}
