package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

// Go's own (PORT-NOTES 165): a bill payment relieves the payable at the
// bill's issue basis; the difference against the cash's base on the payment
// date is realised FX.

// openBillIn stores an OPEN bill row (and its vendor) for amount in currency
// in tx, and posts its issue entry on the day on, all on 6000.
func openBillIn(t *testing.T, tx pgx.Tx, currency, amount string, on time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, vendor := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'Supplier')`, vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bills (bill_id, vendor_id, currency, amount, balance_due, status, issue_date)
VALUES ($1, $2, $3, $4, $4, 'OPEN', $5)`, id, vendor, currency, dec(amount), on); err != nil {
		t.Fatal(err)
	}
	accounts, err := AccountsByCode(ctx, tx, []string{DefaultExpenseCode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PostIssued(ctx, tx, base, Document{
		SourceType: SourceBill, ID: id, Currency: currency, Amount: dec(amount), RateDate: on, IssueDate: on,
		Shares: []Share{{AccountID: accounts[DefaultExpenseCode].ID, Amount: dec(amount)}},
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// storeBillPayment stores a CLEARED bill payment row of amount on the day
// on against bill in tx.
func storeBillPayment(t *testing.T, tx pgx.Tx, bill uuid.UUID, amount, currency string, on time.Time) BillPayment {
	t.Helper()
	bp := BillPayment{ID: uuid.New(), Amount: dec(amount), Currency: currency, Date: on}
	if _, err := tx.Exec(context.Background(), `INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date)
VALUES ($1, $2, $3, $4, $5)`, bp.ID, bill, bp.Amount, currency, on); err != nil {
		t.Fatal(err)
	}
	return bp
}

// payBillIn stores and posts a bill payment in tx.
func payBillIn(t *testing.T, tx pgx.Tx, bill uuid.UUID, amount, currency string, on time.Time) Entry {
	t.Helper()
	e, err := PostBillPayment(context.Background(), tx, base, storeBillPayment(t, tx, bill, amount, currency, on), bill)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func wantBillBasis(t *testing.T, tx pgx.Tx, bill uuid.UUID, face, baseAmt string) {
	t.Helper()
	b, err := BillBasis(context.Background(), tx, bill)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Issued || !b.Face.Equal(dec(face)) || !b.Base.Equal(dec(baseAmt)) {
		t.Errorf("bill basis = %+v, want issued, face %s, base %s", b, face, baseAmt)
	}
}

// billPaymentRows is the number of BILL_PAYMENT entries q sees.
func billPaymentRows(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
},
) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), `SELECT count(*) FROM journal_entries WHERE source_type = 'BILL_PAYMENT'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// commit commits tx, so checkLines (which reads through pool) sees it.
func commit(t *testing.T, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIDRBillPaymentPostsAPAgainstBank(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "IDR", "1000", payDay)
	e := payBillIn(t, tx, bill, "400", "IDR", payDay)
	commit(t, tx)
	checkLines(t, pool, e, []want{
		{PayableCode, "400", "0", "IDR", "400", "0", "1"},
		{BankCode, "0", "400", "IDR", "0", "400", "1"},
	})
	if *e.Memo != "Bill payment "+e.SourceID.String()+" against bill "+bill.String() || e.SourceType != SourceBillPayment {
		t.Errorf("entry %s %q", e.SourceType, *e.Memo)
	}
}

func TestUSDBillPaymentBooksFX(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		on   time.Time
		bank string
		rate string
		plug want
	}{
		// Opened at 15000; paid at 16000 the cash is worth more than the
		// payable relieved: a loss. Paid at 14000, less: a gain.
		{"loss", feb, "1600000", "16000", want{FXLossCode, "100000", "0", "IDR", "100000", "0", "1"}},
		{"gain", mar, "1400000", "14000", want{FXGainCode, "0", "100000", "IDR", "0", "100000", "1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pool := basisPool(t)
			tx := begin(t, pool)
			bill := openBillIn(t, tx, "USD", "100", jan)
			wantBillBasis(t, tx, bill, "100", "1500000")
			e := payBillIn(t, tx, bill, "100", "USD", c.on)
			wantBillBasis(t, tx, bill, "0", "0")
			commit(t, tx)
			checkLines(t, pool, e, []want{
				{PayableCode, "100", "0", "USD", "1500000", "0", "15000"},
				{BankCode, "0", "100", "USD", "0", c.bank, c.rate},
				c.plug,
			})
		})
	}
}

// Two partials at two rates leave the payable at exactly 0 in USD and IDR;
// the FX lines are the true differences.
func TestBillPartialsClearAPExactly(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "USD", "100.0001", jan) // base 1,500,001.5
	first := payBillIn(t, tx, bill, "33.3333", "USD", feb)
	wantBillBasis(t, tx, bill, "66.6668", "1000002")
	second := payBillIn(t, tx, bill, "66.6668", "USD", mar)
	wantBillBasis(t, tx, bill, "0", "0")
	commit(t, tx)
	checkLines(t, pool, first, []want{
		{PayableCode, "33.3333", "0", "USD", "499999.5", "0", "15000"},
		{BankCode, "0", "33.3333", "USD", "0", "533332.8", "16000"},
		{FXLossCode, "33333.3", "0", "IDR", "33333.3", "0", "1"},
	})
	checkLines(t, pool, second, []want{
		{PayableCode, "66.6668", "0", "USD", "1000002", "0", "15000"},
		{BankCode, "0", "66.6668", "USD", "0", "933335.2", "14000"},
		{FXGainCode, "0", "66666.8", "IDR", "0", "66666.8", "1"},
	})
}

// A reversed bill payment's entry is undone in the basis: the next payment
// relieves at the issue basis again.
func TestReversedBillPaymentRestoresBasis(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "USD", "100", jan)
	e := payBillIn(t, tx, bill, "40", "USD", feb)
	wantBillBasis(t, tx, bill, "60", "900000")
	memo := "Reversal: wrong bill"
	if _, err := PostReversal(context.Background(), tx, base, e.ID, &memo); err != nil {
		t.Fatal(err)
	}
	wantBillBasis(t, tx, bill, "100", "1500000")
}

func TestBillPaymentUnissuedRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "IDR", "100", payDay)
	if _, err := ReverseIssued(ctx, tx, SourceBill, bill, "gone"); err != nil {
		t.Fatal(err)
	}
	_, err := PostBillPayment(ctx, tx, base, storeBillPayment(t, tx, bill, "100", "IDR", payDay), bill)
	billRefused(t, tx, err, "bill "+bill.String()+" has no issue entry — run api backpost-ledger")
}

// A payable with a face above 0 but a base at or below 0 is refused, never
// relieved at a zero or negative rate.
func TestBillPaymentInconsistentBasisRefused(t *testing.T) {
	t.Parallel()
	for _, b := range []string{"0", "-1"} {
		t.Run(b, func(t *testing.T) {
			t.Parallel()
			pool := newPool(t)
			ctx := context.Background()
			tx := begin(t, pool)
			bill := openBillIn(t, tx, "USD", "100", payDay)
			if _, err := tx.Exec(ctx, `UPDATE journal_lines SET base_amount_credit = $2::numeric
WHERE credit > 0 AND entry_id IN (SELECT entry_id FROM journal_entries WHERE source_id = $1)`, bill, b); err != nil {
				t.Fatal(err)
			}
			_, err := PostBillPayment(ctx, tx, base, storeBillPayment(t, tx, bill, "100", "USD", payDay), bill)
			billRefused(t, tx, err, "bill "+bill.String()+" payable basis is inconsistent (face 100.0000, base "+dec(b).StringFixed(4)+")")
		})
	}
}

func TestBillPaymentPostedTwiceRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "IDR", "100", payDay)
	bp := storeBillPayment(t, tx, bill, "50", "IDR", payDay)
	if _, err := PostBillPayment(ctx, tx, base, bp, bill); err != nil {
		t.Fatal(err)
	}
	_, err := PostBillPayment(ctx, tx, base, bp, bill)
	if !errors.Is(err, ErrLedger) || err.Error() != "bill payment "+bp.ID.String()+" already has an active entry" {
		t.Errorf("err = %v", err)
	}
	if n := billPaymentRows(t, tx); n != 1 {
		t.Errorf("%d bill payment entries, want 1", n)
	}
}

func TestBillPaymentRateMissing(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "USD", "100", jan)
	early := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err := PostBillPayment(ctx, tx, base, storeBillPayment(t, tx, bill, "100", "USD", early), bill)
	if !errors.Is(err, fx.ErrRateMissing) {
		t.Errorf("err = %v, want fx.ErrRateMissing", err)
	}
	if n := billPaymentRows(t, tx); n != 0 {
		t.Errorf("%d bill payment entries written", n)
	}
}

func TestPostBillPaymentAt(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "IDR", "100", payDay)
	at := time.Date(2026, 1, 20, 8, 30, 0, 0, time.UTC)
	e, err := PostBillPaymentAt(ctx, tx, base, storeBillPayment(t, tx, bill, "100", "IDR", payDay), bill, at)
	if err != nil {
		t.Fatal(err)
	}
	var postedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT posted_at FROM journal_entries WHERE entry_id = $1`, e.ID).Scan(&postedAt); err != nil {
		t.Fatal(err)
	}
	if !postedAt.Equal(at) {
		t.Errorf("posted_at %s, want %s", postedAt, at)
	}
}

func TestIssuedSharesOfBill(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	bill := openBillIn(t, tx, "IDR", "100", payDay)
	got, ok, err := IssuedShares(ctx, tx, SourceBill, bill)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	accounts, err := AccountsByCode(ctx, tx, []string{DefaultExpenseCode})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AccountID != accounts[DefaultExpenseCode].ID || !got[0].Amount.Equal(dec("100")) {
		t.Errorf("shares %+v", got)
	}
	if _, ok, err := IssuedShares(ctx, tx, SourceBill, uuid.New()); ok || err != nil {
		t.Errorf("unknown bill: ok %v err %v", ok, err)
	}
}

// billRefused checks err is an ErrLedger with text msg and that tx holds no
// BILL_PAYMENT entry.
func billRefused(t *testing.T, tx pgx.Tx, err error, msg string) {
	t.Helper()
	if !errors.Is(err, ErrLedger) {
		t.Fatalf("err = %v, want ErrLedger", err)
	}
	if err.Error() != msg {
		t.Errorf("text = %q, want %q", err.Error(), msg)
	}
	if n := billPaymentRows(t, tx); n != 0 {
		t.Errorf("%d bill payment entries written, want none", n)
	}
}
