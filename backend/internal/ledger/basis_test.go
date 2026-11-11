package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Go's own (PORT-NOTES 163): a payment relieves the receivable at the
// invoice's issue basis, and the difference against the cash's base on the
// payment date is realised FX in every currency combination.

// The basis tests' USD→IDR rates (2025, before apitest's 2026 seed).
var (
	jan = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // 15000
	feb = time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC) // 16000
	mar = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC) // 14000
)

// basisPool is a fresh database with the basis tests' rates.
func basisPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newPool(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ('USD', 'IDR', 15000, '2025-01-01', 'TEST'), ('USD', 'IDR', 16000, '2025-02-01', 'TEST'), ('USD', 'IDR', 14000, '2025-03-01', 'TEST')`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// issueIn posts inv's issue entry in tx on the day on, all on 4000, for its
// BalanceDue.
func issueIn(t *testing.T, tx pgx.Tx, inv Invoice, on time.Time) Entry {
	t.Helper()
	ctx := context.Background()
	accounts, err := AccountsByCode(ctx, tx, []string{FixedFeeCode})
	if err != nil {
		t.Fatal(err)
	}
	e, err := PostIssued(ctx, tx, base, Document{
		SourceType: SourceInvoice, ID: inv.ID, Currency: inv.Currency, Amount: inv.BalanceDue,
		RateDate: on, IssueDate: on, Shares: []Share{{AccountID: accounts[FixedFeeCode].ID, Amount: inv.BalanceDue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// issuedInvoice stores an OPEN invoice row (and its customer) for amount in
// currency and posts its issue entry on the day on.
func issuedInvoice(t *testing.T, pool *pgxpool.Pool, currency, amount string, on time.Time) Invoice {
	t.Helper()
	ctx := context.Background()
	inv := Invoice{ID: uuid.New(), Currency: currency, BalanceDue: dec(amount)}
	customer := uuid.New()
	tx := begin(t, pool)
	if _, err := tx.Exec(ctx, `INSERT INTO customers (customer_id, name, matching_aliases) VALUES ($1, 'Basis Co', '[]'::jsonb)`, customer); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, issue_date)
VALUES ($1, $2, 'MILESTONE', $3, $4, $4, 'OPEN', $5)`, inv.ID, customer, currency, inv.BalanceDue, on); err != nil {
		t.Fatal(err)
	}
	issueIn(t, tx, inv, on)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return inv
}

// payInvoice stores a CLEARED payment of amount in currency on the day on
// against inv and posts it with withheld, committing. inv.BalanceDue is the
// balance the posting sees (the cross-currency relief).
func payInvoice(t *testing.T, pool *pgxpool.Pool, inv Invoice, amount, currency string, on time.Time, withheld string) Entry {
	t.Helper()
	ctx := context.Background()
	p := Payment{ID: uuid.New(), Status: "CLEARED", Amount: dec(amount), Currency: currency, Date: on}
	tx := begin(t, pool)
	if _, err := tx.Exec(ctx, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status)
VALUES ($1, $2, $3, $4, 'Basis Co', $5, 'MANUAL', $6, 'CLEARED')`, p.ID, inv.ID, p.Amount, currency, on, "ref-"+p.ID.String()); err != nil {
		t.Fatal(err)
	}
	e, err := PostPaymentCleared(ctx, tx, base, p, inv, dec(withheld))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

// wantBasis checks inv's receivable on the ledger.
func wantBasis(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, face, baseAmt string) {
	t.Helper()
	tx := begin(t, pool)
	b, err := InvoiceBasis(context.Background(), tx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Issued || !b.Face.Equal(dec(face)) || !b.Base.Equal(dec(baseAmt)) {
		t.Errorf("basis = %+v, want issued, face %s, base %s", b, face, baseAmt)
	}
}

func TestUSDPaidSameCurrencyBooksFXGain(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	inv := issuedInvoice(t, pool, "USD", "100", jan)
	wantBasis(t, pool, inv.ID, "100", "1500000")
	e := payInvoice(t, pool, inv, "100", "USD", feb, "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1600000", "0", "16000"},
		{ReceivableCode, "0", "100", "USD", "0", "1500000", "15000"},
		{FXGainCode, "0", "100000", "IDR", "0", "100000", "1"},
	})
	wantBasis(t, pool, inv.ID, "0", "0")
}

func TestUSDPaidSameCurrencyBooksFXLoss(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	inv := issuedInvoice(t, pool, "USD", "100", jan)
	e := payInvoice(t, pool, inv, "100", "USD", mar, "0")
	checkLines(t, pool, e, []want{
		{BankCode, "100", "0", "USD", "1400000", "0", "14000"},
		{ReceivableCode, "0", "100", "USD", "0", "1500000", "15000"},
		{FXLossCode, "100000", "0", "IDR", "100000", "0", "1"},
	})
	wantBasis(t, pool, inv.ID, "0", "0")
}

// 100.0001 USD at 15000 is 1,500,001.5. The first payment takes its share
// of the base (33.3333 × 15000); the last takes what is left, so the
// receivable ends at exactly 0 in both currencies.
func TestTwoPartialsClearARBaseExactly(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	inv := issuedInvoice(t, pool, "USD", "100.0001", jan)
	first := payInvoice(t, pool, inv, "33.3333", "USD", feb, "0")
	checkLines(t, pool, first, []want{
		{BankCode, "33.3333", "0", "USD", "533332.8", "0", "16000"},
		{ReceivableCode, "0", "33.3333", "USD", "0", "499999.5", "15000"},
		{FXGainCode, "0", "33333.3", "IDR", "0", "33333.3", "1"},
	})
	wantBasis(t, pool, inv.ID, "66.6668", "1000002")
	second := payInvoice(t, pool, inv, "66.6668", "USD", mar, "0")
	checkLines(t, pool, second, []want{
		{BankCode, "66.6668", "0", "USD", "933335.2", "0", "14000"},
		{ReceivableCode, "0", "66.6668", "USD", "0", "1000002", "15000"},
		{FXLossCode, "66666.8", "0", "IDR", "66666.8", "0", "1"},
	})
	wantBasis(t, pool, inv.ID, "0", "0.0000")
	var fx decimal.Decimal
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.base_amount_credit - l.base_amount_debit), 0)
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id WHERE a.code IN ('4900', '5900')`).Scan(&fx); err != nil {
		t.Fatal(err)
	}
	// Cash base 533,332.8 + 933,335.2 less the issue base 1,500,001.5.
	if !fx.Equal(dec("-33333.5")) {
		t.Errorf("realised FX = %s, want -33333.5", fx)
	}
}

// Rounding the share: 7 USD issued at 15000.00007 is 105000.0005; 3 of
// them take 45000.000214… → 45000.0002, at the rate 15000.00006667.
func TestPartialReliefRoundsShare(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ('USD', 'IDR', 15000.00007, '2025-01-15', 'TEST')`); err != nil {
		t.Fatal(err)
	}
	inv := issuedInvoice(t, pool, "USD", "7", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))
	wantBasis(t, pool, inv.ID, "7", "105000.0005")
	e := payInvoice(t, pool, inv, "3", "USD", feb, "0")
	ar := e.Lines[1]
	if !ar.BaseCredit.Equal(dec("45000.0002")) || !ar.FXRate.Equal(dec("15000.00006667")) {
		t.Errorf("receivable %+v, want base 45000.0002 at 15000.00006667", ar)
	}
	wantBasis(t, pool, inv.ID, "4", "60000.0003")
}

func TestWithholdingAtIssueBasis(t *testing.T) {
	t.Parallel()
	t.Run("IDR", func(t *testing.T) {
		t.Parallel()
		pool := basisPool(t)
		inv := issuedInvoice(t, pool, "IDR", "1100000", jan)
		e := payInvoice(t, pool, inv, "1000000", "IDR", feb, "100000")
		checkLines(t, pool, e, []want{
			{BankCode, "1000000", "0", "IDR", "1000000", "0", "1"},
			{PrepaidTaxCode, "100000", "0", "IDR", "100000", "0", "1"},
			{ReceivableCode, "0", "1100000", "IDR", "0", "1100000", "1"},
		})
		wantBasis(t, pool, inv.ID, "0", "0")
	})
	t.Run("USD", func(t *testing.T) {
		t.Parallel()
		pool := basisPool(t)
		inv := issuedInvoice(t, pool, "USD", "110", jan)
		e := payInvoice(t, pool, inv, "100", "USD", feb, "10")
		checkLines(t, pool, e, []want{
			{BankCode, "100", "0", "USD", "1600000", "0", "16000"},
			{PrepaidTaxCode, "10", "0", "USD", "160000", "0", "16000"},
			{ReceivableCode, "0", "110", "USD", "0", "1650000", "15000"},
			{FXGainCode, "0", "110000", "IDR", "0", "110000", "1"},
		})
		wantBasis(t, pool, inv.ID, "0", "0")
	})
}

func TestCrossCurrencyRelievesRemainingBasis(t *testing.T) {
	t.Parallel()
	t.Run("IDR payment, USD invoice", func(t *testing.T) {
		t.Parallel()
		pool := basisPool(t)
		inv := issuedInvoice(t, pool, "USD", "100", jan)
		payInvoice(t, pool, inv, "40", "USD", feb, "0")
		wantBasis(t, pool, inv.ID, "60", "900000")
		inv.BalanceDue = dec("60")
		e := payInvoice(t, pool, inv, "1000000", "IDR", mar, "0")
		checkLines(t, pool, e, []want{
			{BankCode, "1000000", "0", "IDR", "1000000", "0", "1"},
			{ReceivableCode, "0", "60", "USD", "0", "900000", "15000"},
			{FXGainCode, "0", "100000", "IDR", "0", "100000", "1"},
		})
		wantBasis(t, pool, inv.ID, "0", "0")
	})
	t.Run("USD payment, IDR invoice", func(t *testing.T) {
		t.Parallel()
		pool := basisPool(t)
		inv := issuedInvoice(t, pool, "IDR", "1550000", jan)
		e := payInvoice(t, pool, inv, "100", "USD", feb, "0")
		checkLines(t, pool, e, []want{
			{BankCode, "100", "0", "USD", "1600000", "0", "16000"},
			{ReceivableCode, "0", "1550000", "IDR", "0", "1550000", "1"},
			{FXGainCode, "0", "50000", "IDR", "0", "50000", "1"},
		})
		wantBasis(t, pool, inv.ID, "0", "0")
	})
}

// An over-payment relieves the face at the issue basis and the excess at
// the payment date's rate; a payment against a receivable already at or
// below 0 converts whole at the payment date's rate.
func TestOverReliefExcessAtPaymentRate(t *testing.T) {
	t.Parallel()
	pool := basisPool(t)
	inv := issuedInvoice(t, pool, "USD", "100", jan)
	e := payInvoice(t, pool, inv, "120", "USD", feb, "0")
	checkLines(t, pool, e, []want{
		{BankCode, "120", "0", "USD", "1920000", "0", "16000"},
		// 1,500,000 + 20 × 16000.
		{ReceivableCode, "0", "120", "USD", "0", "1820000", "15166.66666667"},
		{FXGainCode, "0", "100000", "IDR", "0", "100000", "1"},
	})
	wantBasis(t, pool, inv.ID, "-20", "-320000")
	again := payInvoice(t, pool, inv, "10", "USD", mar, "0")
	checkLines(t, pool, again, []want{
		{BankCode, "10", "0", "USD", "140000", "0", "14000"},
		{ReceivableCode, "0", "10", "USD", "0", "140000", "14000"},
	})
	wantBasis(t, pool, inv.ID, "-30", "-460000")
}

func TestUnissuedInvoiceRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	inv := invoice("IDR", "100")
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "IDR"), inv, dec("0"))
	refused(t, tx, err, "invoice "+inv.ID.String()+" has no issue entry — run api backpost-ledger")
}

// A reversed issue entry is not an issue entry.
func TestReversedIssueEntryRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	inv := invoice("IDR", "100")
	issueIn(t, tx, inv, payDay)
	if ok, err := ReverseIssued(context.Background(), tx, SourceInvoice, inv.ID, "void"); err != nil || !ok {
		t.Fatalf("reverse: %v %v", ok, err)
	}
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("100", "IDR"), inv, dec("0"))
	refused(t, tx, err, "invoice "+inv.ID.String()+" has no issue entry — run api backpost-ledger")
}

// PostPaymentClearedAt keeps the given posted_at.
func TestPostPaymentClearedAt(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ctx := context.Background()
	tx := begin(t, pool)
	inv := invoice("IDR", "100")
	issueIn(t, tx, inv, payDay)
	at := time.Date(2026, 1, 20, 8, 30, 0, 0, time.UTC)
	e, err := PostPaymentClearedAt(ctx, tx, base, cleared("100", "IDR"), inv, dec("0"), at)
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

// A receivable with a face above 0 but a base at or below 0 cannot have been
// booked by the ledger: a payment against it is refused, never relieved at a
// zero or negative rate.
func TestInconsistentBasisRefused(t *testing.T) {
	t.Parallel()
	for _, b := range []string{"0", "-1"} {
		t.Run(b, func(t *testing.T) {
			t.Parallel()
			pool := newPool(t)
			ctx := context.Background()
			tx := begin(t, pool)
			inv := invoice("USD", "100")
			issue := issueIn(t, tx, inv, payDay)
			if _, err := tx.Exec(ctx, `UPDATE journal_lines SET base_amount_debit = $2::numeric WHERE entry_id = $1 AND debit > 0`, issue.ID, b); err != nil {
				t.Fatal(err)
			}
			_, err := PostPaymentCleared(ctx, tx, base, cleared("100", "USD"), inv, dec("0"))
			refused(t, tx, err, "invoice "+inv.ID.String()+" receivable basis is inconsistent (face 100.0000, base "+dec(b).StringFixed(4)+")")
		})
	}
}

// Across currencies the relief is balance_due; one above what the ledger
// still holds (PORT-NOTES 7: a reversal under Python's arithmetic) is
// refused rather than relieved, so no false FX loss is booked.
func TestCrossCurrencyReliefBeyondFaceRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tx := begin(t, pool)
	inv := invoice("USD", "100")
	issueIn(t, tx, inv, payDay)
	inv.BalanceDue = dec("100.0001")
	_, err := PostPaymentCleared(context.Background(), tx, base, cleared("1600000", "IDR"), inv, dec("0"))
	refused(t, tx, err, "cross-currency settlement of invoice "+inv.ID.String()+
		" would relieve 100.0001 USD but the ledger receivable is 100.0000 USD; correct balance_due first")
}
