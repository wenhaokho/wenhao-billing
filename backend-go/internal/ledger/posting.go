package ledger

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

type PaymentInput struct {
	PaymentID, InvoiceID                            pgtype.UUID
	Status, Currency, InvoiceCurrency, BaseCurrency string
	Amount, Withheld, InvoiceBalance                decimal.Decimal
	PaymentDate                                     time.Time
}

type journalLine struct {
	Line
	account int32
	rate    decimal.Decimal
}

func newID() (pgtype.UUID, error) {
	var id pgtype.UUID
	_, err := rand.Read(id.Bytes[:])
	id.Valid = err == nil
	id.Bytes[6] = (id.Bytes[6] & 15) | 64
	id.Bytes[8] = (id.Bytes[8] & 63) | 128
	return id, err
}
func uuidString(id pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", id.Bytes[:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:])
}
func numeric(v decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{Int: v.Coefficient(), Exp: v.Exponent(), Valid: true}
}
func fromNumeric(v pgtype.Numeric) decimal.Decimal { return decimal.NewFromBigInt(v.Int, v.Exp) }
func makeLine(ctx context.Context, tx pgx.Tx, code, currency, base string, amount decimal.Decimal, debit bool, date time.Time) (journalLine, error) {
	q := dbquery.New(tx)
	account, err := q.LedgerAccount(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return journalLine{}, fmt.Errorf("chart of accounts missing code %s", code)
	}
	if err != nil {
		return journalLine{}, err
	}
	if !amount.IsPositive() {
		return journalLine{}, errors.New("journal line amount must be positive")
	}
	conv, err := fx.Convert(ctx, tx, amount, currency, base, date)
	if err != nil {
		return journalLine{}, err
	}
	l := journalLine{Line: Line{Currency: currency}, account: account, rate: conv.Rate}
	if debit {
		l.Debit = amount
		l.BaseDebit = conv.BaseAmount
	} else {
		l.Credit = amount
		l.BaseCredit = conv.BaseAmount
	}
	return l, nil
}
func insert(ctx context.Context, tx pgx.Tx, source string, sourceID pgtype.UUID, memo pgtype.Text, lines []journalLine) (pgtype.UUID, error) {
	checks := make([]Line, len(lines))
	for i, l := range lines {
		checks[i] = l.Line
	}
	if err := ValidateBalance(checks); err != nil {
		return pgtype.UUID{}, errors.New("journal entry is unbalanced")
	}
	id, err := newID()
	if err != nil {
		return id, err
	}
	q := dbquery.New(tx)
	if err = q.LedgerCreateEntry(ctx, dbquery.LedgerCreateEntryParams{EntryID: id, SourceType: source, SourceID: sourceID, Memo: memo}); err != nil {
		return pgtype.UUID{}, err
	}
	for _, l := range lines {
		lineID, e := newID()
		if e != nil {
			return pgtype.UUID{}, e
		}
		e = q.LedgerCreateLine(ctx, dbquery.LedgerCreateLineParams{LineID: lineID, EntryID: id, AccountID: l.account, Debit: numeric(l.Debit), Credit: numeric(l.Credit), Currency: l.Currency, BaseAmountDebit: numeric(l.BaseDebit), BaseAmountCredit: numeric(l.BaseCredit), FxRate: numeric(l.rate)})
		if e != nil {
			return pgtype.UUID{}, e
		}
	}
	return id, nil
}

// PostPayment posts inside a savepoint of the caller's transaction; the caller owns commit.
func PostPayment(ctx context.Context, tx pgx.Tx, in PaymentInput) (pgtype.UUID, error) {
	for _, amount := range []decimal.Decimal{in.Amount, in.Withheld, in.InvoiceBalance} {
		if !amount.Equal(amount.Round(4)) {
			return pgtype.UUID{}, errors.New("amount exceeds four decimal places")
		}
	}
	if in.Status != "CLEARED" {
		return pgtype.UUID{}, errors.New("only cleared payments can be posted")
	}
	if in.Withheld.IsNegative() {
		return pgtype.UUID{}, errors.New("withholding cannot be negative")
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer sp.Rollback(ctx)
	bank, err := makeLine(ctx, sp, "1000", in.Currency, in.BaseCurrency, in.Amount, true, in.PaymentDate)
	if err != nil {
		return pgtype.UUID{}, err
	}
	lines := []journalLine{bank}
	total := bank.BaseDebit
	if in.Withheld.IsPositive() {
		w, e := makeLine(ctx, sp, "1300", in.InvoiceCurrency, in.BaseCurrency, in.Withheld, true, in.PaymentDate)
		if e != nil {
			return pgtype.UUID{}, e
		}
		lines = append(lines, w)
		total = total.Add(w.BaseDebit)
	}
	arAmount := in.Amount.Add(in.Withheld)
	if in.Currency != in.InvoiceCurrency {
		arAmount = in.InvoiceBalance
		if !arAmount.IsPositive() {
			return pgtype.UUID{}, errors.New("cross-currency invoice balance must be positive")
		}
	}
	ar, err := makeLine(ctx, sp, "1100", in.InvoiceCurrency, in.BaseCurrency, arAmount, false, in.PaymentDate)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if in.Currency == in.InvoiceCurrency {
		ar.BaseCredit = total
	}
	lines = append(lines, ar)
	if in.Currency != in.InvoiceCurrency {
		diff := total.Sub(ar.BaseCredit)
		if !diff.IsZero() {
			code := "4900"
			debit := false
			if diff.IsNegative() {
				code = "5900"
				debit = true
				diff = diff.Neg()
			}
			l, e := makeLine(ctx, sp, code, in.BaseCurrency, in.BaseCurrency, diff, debit, in.PaymentDate)
			if e != nil {
				return pgtype.UUID{}, e
			}
			lines = append(lines, l)
		}
	}
	id, err := insert(ctx, sp, "PAYMENT", in.PaymentID, pgtype.Text{String: fmt.Sprintf("Payment %s cleared against invoice %s", uuidString(in.PaymentID), uuidString(in.InvoiceID)), Valid: true}, lines)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = sp.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

// Reverse locks and reverses the entry within the caller's transaction.
func Reverse(ctx context.Context, tx pgx.Tx, originalID pgtype.UUID, memo string) (pgtype.UUID, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer sp.Rollback(ctx)
	q := dbquery.New(sp)
	original, err := q.LedgerLockEntry(ctx, originalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, errors.New("journal entry not found")
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	if original.ReversedByEntryID.Valid {
		return pgtype.UUID{}, errors.New("journal entry already reversed")
	}
	rows, err := q.LedgerLines(ctx, originalID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	lines := make([]journalLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, journalLine{Line: Line{Currency: r.Currency, Debit: fromNumeric(r.Credit), Credit: fromNumeric(r.Debit), BaseDebit: fromNumeric(r.BaseAmountCredit), BaseCredit: fromNumeric(r.BaseAmountDebit)}, account: r.AccountID, rate: fromNumeric(r.FxRate)})
	}
	if memo == "" {
		memo = "Reversal of " + uuidString(originalID)
	}
	id, err := insert(ctx, sp, original.SourceType, original.SourceID, pgtype.Text{String: memo, Valid: true}, lines)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = q.LedgerLinkReversal(ctx, dbquery.LedgerLinkReversalParams{EntryID: originalID, ReversedByEntryID: id}); err != nil {
		return pgtype.UUID{}, err
	}
	if err = sp.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
