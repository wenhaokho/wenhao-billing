package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
)

// Go's own (PORT-NOTES 165): a bill payment relieves the payable at the
// bill's issue basis, as an invoice payment relieves the receivable
// (PORT-NOTES 163), and the difference against the cash's base on the
// payment date is realised FX.

// BillPayment is the bill payment being posted, in the bill's currency.
// Date is its payment date: its calendar date is the FX as-of date. Memo is
// the entry's memo; empty is "Bill payment <id> against bill <bill id>".
type BillPayment struct {
	ID       uuid.UUID
	Amount   decimal.Decimal
	Currency string
	Date     time.Time
	Memo     string
}

// BillBasis is the bill's payable on the ledger: Face is Σ(credit − debit)
// and Base Σ(base_credit − base_debit) over the 2000 lines of the entries
// sourced from the bill (BILL) or from any of its bill payments
// (BILL_PAYMENT), reversals included. Issued is whether the bill has an
// active issue entry.
func BillBasis(ctx context.Context, tx pgx.Tx, billID uuid.UUID) (Basis, error) {
	row, err := ledgerdb.New(tx).BillAPBasis(ctx, billID)
	if err != nil {
		return Basis{}, fmt.Errorf("ledger: bill basis: %w", err)
	}
	_, issued, err := ActiveEntryFor(ctx, tx, SourceBill, billID)
	if err != nil {
		return Basis{}, err
	}
	return Basis{Issued: issued, Face: row.Face, Base: row.Base}, nil
}

// PostBillPayment posts a bill payment, at now: DR payable for the amount
// at the bill's issue basis (BillBasis, reliefBase) / CR bank for the amount
// converted on the payment date. The base difference (payable relief −
// bank) is realised FX in baseCurrency: above zero FX Gain (CR), below zero
// FX Loss (DR). A bill with no active issue entry, or a payment that
// already has an active entry, is refused (ErrLedger); a missing rate is
// fx's error (errors.Is fx.ErrRateMissing), checked first. Nothing is
// written unless the entry balances.
func PostBillPayment(ctx context.Context, tx pgx.Tx, baseCurrency string, bp BillPayment, billID uuid.UUID) (Entry, error) {
	return postBillPayment(ctx, tx, baseCurrency, bp, billID, nil)
}

// PostBillPaymentAt is PostBillPayment posted at postedAt (never later than
// now): the back-post's legacy bill payments.
func PostBillPaymentAt(ctx context.Context, tx pgx.Tx, baseCurrency string, bp BillPayment, billID uuid.UUID, postedAt time.Time) (Entry, error) {
	return postBillPayment(ctx, tx, baseCurrency, bp, billID, &postedAt)
}

func postBillPayment(ctx context.Context, tx pgx.Tx, baseCurrency string, bp BillPayment, billID uuid.UUID, postedAt *time.Time) (Entry, error) {
	if !bp.Amount.IsPositive() {
		// The table's CHECK (amount > 0) keeps one from being stored: a
		// programming error, not a refusal.
		return Entry{}, fmt.Errorf("ledger: bill payment %s amount must be > 0 (got %s)", bp.ID, bp.Amount)
	}
	q := ledgerdb.New(tx)
	fq := fxdb.New(tx)
	b, err := newBuilder(ctx, q, fq, baseCurrency, bp.Date, BankCode, PayableCode, FXGainCode, FXLossCode)
	if err != nil {
		return Entry{}, err
	}
	bank, err := b.line(BankCode, decimal.Zero, bp.Amount, bp.Currency)
	if err != nil {
		return Entry{}, err
	}

	_, posted, err := ActiveEntryFor(ctx, tx, SourceBillPayment, bp.ID)
	if err != nil {
		return Entry{}, err
	}
	if posted {
		return Entry{}, refuse("bill payment %s already has an active entry", bp.ID)
	}
	basis, err := BillBasis(ctx, tx, billID)
	if err != nil {
		return Entry{}, err
	}
	if !basis.Issued {
		return Entry{}, refuse("bill %s has no issue entry — run api backpost-ledger", billID)
	}
	apBase, err := reliefBase("bill "+billID.String()+" payable", basis, bp.Amount, func(x decimal.Decimal) (decimal.Decimal, error) {
		c, err := fx.Convert(ctx, fq, x, bp.Currency, bp.Date, baseCurrency)
		return c.BaseAmount, err
	})
	if err != nil {
		return Entry{}, err
	}
	apID, ok := b.accounts[PayableCode]
	if !ok {
		return Entry{}, refuse("chart_of_accounts missing code %s", PayableCode)
	}
	lines := []Line{{
		AccountID: apID, Debit: bp.Amount, Credit: decimal.Zero, Currency: bp.Currency,
		BaseDebit: apBase, BaseCredit: decimal.Zero, FXRate: apBase.DivRound(bp.Amount, 8),
	}, bank}

	// Above zero the payable we relieved was worth more than the cash paid:
	// an FX gain (CR). Below zero, less: an FX loss (DR).
	diff := apBase.Sub(bank.BaseCredit)
	if !diff.IsZero() {
		var plug Line
		if diff.IsPositive() {
			plug, err = b.line(FXGainCode, decimal.Zero, diff, baseCurrency)
		} else {
			plug, err = b.line(FXLossCode, diff.Neg(), decimal.Zero, baseCurrency)
		}
		if err != nil {
			return Entry{}, err
		}
		lines = append(lines, plug)
	}

	memo := bp.Memo
	if memo == "" {
		memo = fmt.Sprintf("Bill payment %s against bill %s", bp.ID, billID)
	}
	e := Entry{ID: uuid.New(), SourceType: SourceBillPayment, SourceID: bp.ID, Memo: &memo, Lines: lines}
	if err := checkBalanced(e.Lines); err != nil {
		return Entry{}, err
	}
	if err := insert(ctx, q, e, postedAt); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// IssuedShares are the shares of the source's active issue entry: each
// line other than the control account's (1100 for an invoice, 2000 for a
// bill), as its account and face amount. False when it has none.
func IssuedShares(ctx context.Context, tx pgx.Tx, sourceType string, id uuid.UUID) ([]Share, bool, error) {
	control := ReceivableCode
	if sourceType == SourceBill {
		control = PayableCode
	}
	entryID, ok, err := ActiveEntryFor(ctx, tx, sourceType, id)
	if err != nil || !ok {
		return nil, false, err
	}
	accounts, err := AccountsByCode(ctx, tx, []string{control})
	if err != nil {
		return nil, false, err
	}
	rows, err := ledgerdb.New(tx).EntryLines(ctx, entryID)
	if err != nil {
		return nil, false, fmt.Errorf("ledger: entry lines: %w", err)
	}
	var out []Share
	for _, r := range rows {
		if a, ok := accounts[control]; ok && r.AccountID == a.ID {
			continue
		}
		out = append(out, Share{AccountID: r.AccountID, Amount: r.Debit.Add(r.Credit)})
	}
	return out, true, nil
}
