package scopedledger

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedfx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

type journalLine struct {
	ledger.Line
	account int32
	rate    decimal.Decimal
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func newID() (pgtype.UUID, error) {
	var id pgtype.UUID
	_, err := rand.Read(id.Bytes[:])
	id.Valid = err == nil
	id.Bytes[6] = (id.Bytes[6] & 15) | 64
	id.Bytes[8] = (id.Bytes[8] & 63) | 128
	return id, err
}
func numeric(v decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{Int: v.Coefficient(), Exp: v.Exponent(), Valid: true}
}
func storedDecimal(value pgtype.Numeric) (decimal.Decimal, error) {
	if !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil {
		return decimal.Zero, errors.New("invalid stored journal or payment amount")
	}
	return decimal.NewFromBigInt(value.Int, value.Exp), nil
}

func makeLine(ctx context.Context, tx pgx.Tx, scope business.ID, rates map[string]decimal.Decimal, code, currency, base string, amount decimal.Decimal, debit bool, date time.Time) (journalLine, error) {
	account, err := LedgerAccount(ctx, tx, scope, dbqueryv8.LedgerAccountParams{Code: code})
	if errors.Is(err, pgx.ErrNoRows) {
		return journalLine{}, fmt.Errorf("chart of accounts missing code %s", code)
	}
	if err != nil {
		return journalLine{}, err
	}
	if !amount.IsPositive() {
		return journalLine{}, errors.New("journal line amount must be positive")
	}
	rate, ok := rates[currency]
	if !ok {
		rate, err = scopedfx.GetRate(ctx, tx, scope, currency, base, date)
		if err != nil {
			return journalLine{}, err
		}
		rates[currency] = rate
	}
	conv, err := fx.ConvertAtRate(amount, rate)
	if err != nil {
		return journalLine{}, err
	}
	l := journalLine{Line: ledger.Line{Currency: currency}, account: account, rate: conv.Rate}
	if debit {
		l.Debit = amount
		l.BaseDebit = conv.BaseAmount
	} else {
		l.Credit = amount
		l.BaseCredit = conv.BaseAmount
	}
	return l, nil
}
func validateJournal(lines []journalLine) error {
	if len(lines) < 2 {
		return errors.New("journal entry requires at least two lines")
	}
	checks := make([]ledger.Line, len(lines))
	for i, l := range lines {
		checks[i] = l.Line
	}
	return ledger.ValidateBalance(checks)
}

func insert(ctx context.Context, tx pgx.Tx, scope business.ID, source string, sourceID pgtype.UUID, memo pgtype.Text, lines []journalLine) (pgtype.UUID, error) {
	if err := validateJournal(lines); err != nil {
		return pgtype.UUID{}, err
	}
	id, err := newID()
	if err != nil {
		return id, err
	}
	if err = LedgerCreateEntry(ctx, tx, scope, dbqueryv8.LedgerCreateEntryParams{EntryID: id, SourceType: source, SourceID: sourceID, Memo: memo}); err != nil {
		return pgtype.UUID{}, err
	}
	for _, l := range lines {
		lineID, e := newID()
		if e != nil {
			return pgtype.UUID{}, e
		}
		e = LedgerCreateLine(ctx, tx, scope, dbqueryv8.LedgerCreateLineParams{LineID: lineID, EntryID: id, AccountID: l.account, Debit: numeric(l.Debit), Credit: numeric(l.Credit), Currency: l.Currency, BaseAmountDebit: numeric(l.BaseDebit), BaseAmountCredit: numeric(l.BaseCredit), FxRate: numeric(l.rate)})
		if e != nil {
			return pgtype.UUID{}, e
		}
	}
	return id, nil
}

// PostPayment posts a stored, cleared payment in a savepoint. Call it before
// reducing the invoice balance. The caller owns the outer read-committed transaction.
func PostPayment(ctx context.Context, tx pgx.Tx, scope business.ID, paymentID, invoiceID pgtype.UUID, baseCurrency string) (pgtype.UUID, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.UUID{}, err
	}
	if !paymentID.Valid || !invoiceID.Valid || !currencyCode.MatchString(baseCurrency) {
		return pgtype.UUID{}, errors.New("payment, invoice and base currency are required")
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer sp.Rollback(ctx)

	// The duplicate check must see a posting committed while this transaction
	// waits for the payment lock. A repeatable-read snapshot could miss it.
	var isolation string
	if err = sp.QueryRow(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
		return pgtype.UUID{}, err
	}
	if isolation != "read committed" {
		return pgtype.UUID{}, errors.New("ledger payment posting requires read committed isolation")
	}

	// Keep the same invoice-first ordering as the invoice payment workflow.
	invoice, err := scopedinvoices.LockInvoice(ctx, sp, scope, dbqueryv8.LockInvoiceParams{InvoiceID: invoiceID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	q := dbqueryv8.New(sp)
	payment, err := q.LedgerLockPayment(ctx, dbqueryv8.LedgerLockPaymentParams{
		BusinessID: scope.UUID(), PaymentID: paymentID, InvoiceID: invoiceID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	posted, err := q.LedgerPaymentPosted(ctx, dbqueryv8.LedgerPaymentPostedParams{BusinessID: scope.UUID(), PaymentID: paymentID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if posted {
		return pgtype.UUID{}, errors.New("payment already posted")
	}
	if payment.Status != "CLEARED" || (payment.CustomerID.Valid && payment.CustomerID != invoice.CustomerID) {
		return pgtype.UUID{}, errors.New("payment must be cleared and belong to the invoice customer")
	}
	// Legacy payments can omit a customer. A populated payment customer must
	// match the invoice, and a populated invoice customer must resolve in scope.
	if invoice.CustomerID.Valid {
		if _, err := scopedcustomer.GetCustomer(ctx, sp, scope, dbqueryv8.GetCustomerParams{CustomerID: invoice.CustomerID}); err != nil {
			return pgtype.UUID{}, err
		}
	}
	switch invoice.Status {
	case "OPEN", "SENT", "PARTIAL":
	default:
		return pgtype.UUID{}, errors.New("invoice cannot accept a ledger payment")
	}
	if invoice.IsTemplate || !currencyCode.MatchString(payment.Currency) || !currencyCode.MatchString(invoice.Currency) ||
		!payment.PaymentDate.Valid || payment.PaymentDate.InfinityModifier != pgtype.Finite {
		return pgtype.UUID{}, errors.New("invalid invoice or payment currency or date")
	}
	amount, err := storedDecimal(payment.Amount)
	if err != nil {
		return pgtype.UUID{}, err
	}
	withheld, err := storedDecimal(payment.WhtAmount)
	if err != nil {
		return pgtype.UUID{}, err
	}
	balance, err := storedDecimal(invoice.BalanceDue)
	if err != nil {
		return pgtype.UUID{}, err
	}
	wht, err := storedDecimal(invoice.WhtAmount)
	if err != nil {
		return pgtype.UUID{}, err
	}
	booked, err := storedDecimal(invoice.WhtBooked)
	if err != nil {
		return pgtype.UUID{}, err
	}
	sameCurrency := payment.Currency == invoice.Currency
	if amount.IsNegative() || !balance.IsPositive() || withheld.IsNegative() || booked.IsNegative() ||
		withheld.GreaterThan(wht.Sub(booked)) || withheld.GreaterThan(balance) ||
		(amount.IsZero() && (!sameCurrency || withheld.IsZero())) {
		return pgtype.UUID{}, errors.New("invalid payment amount, invoice balance or withholding")
	}
	if sameCurrency && amount.Add(withheld).GreaterThan(balance) {
		return pgtype.UUID{}, errors.New("payment exceeds invoice balance")
	}
	rates := make(map[string]decimal.Decimal)
	date := payment.PaymentDate.Time
	var lines []journalLine
	total := decimal.Zero
	if amount.IsPositive() {
		bank, err := makeLine(ctx, sp, scope, rates, "1000", payment.Currency, baseCurrency, amount, true, date)
		if err != nil {
			return pgtype.UUID{}, err
		}
		lines = append(lines, bank)
		total = bank.BaseDebit
	}
	if withheld.IsPositive() {
		w, e := makeLine(ctx, sp, scope, rates, "1300", invoice.Currency, baseCurrency, withheld, true, date)
		if e != nil {
			return pgtype.UUID{}, e
		}
		lines = append(lines, w)
		total = total.Add(w.BaseDebit)
	}
	arAmount := amount.Add(withheld)
	if !sameCurrency {
		arAmount = balance
	}
	ar, err := makeLine(ctx, sp, scope, rates, "1100", invoice.Currency, baseCurrency, arAmount, false, date)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if sameCurrency {
		ar.BaseCredit = total
	}
	lines = append(lines, ar)
	if !sameCurrency {
		diff := total.Sub(ar.BaseCredit)
		if !diff.IsZero() {
			code := "4900"
			debit := false
			if diff.IsNegative() {
				code = "5900"
				debit = true
				diff = diff.Neg()
			}
			l, e := makeLine(ctx, sp, scope, rates, code, baseCurrency, baseCurrency, diff, debit, date)
			if e != nil {
				return pgtype.UUID{}, e
			}
			lines = append(lines, l)
		}
	}
	id, err := insert(ctx, sp, scope, "PAYMENT", paymentID, pgtype.Text{String: fmt.Sprintf("Payment %s cleared against invoice %s", paymentID.String(), invoiceID.String()), Valid: true}, lines)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = sp.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

// Reverse locks and reverses the entry within the caller's transaction.
func Reverse(ctx context.Context, tx pgx.Tx, scope business.ID, originalID pgtype.UUID, memo string) (pgtype.UUID, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.UUID{}, err
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer sp.Rollback(ctx)
	original, err := LedgerLockEntry(ctx, sp, scope, dbqueryv8.LedgerLockEntryParams{EntryID: originalID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, errors.New("journal entry not found")
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	q := dbqueryv8.New(sp)
	invalidAccount, err := q.LedgerHasInvalidLineAccount(ctx, dbqueryv8.LedgerHasInvalidLineAccountParams{
		BusinessID: scope.UUID(), EntryID: originalID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if invalidAccount {
		return pgtype.UUID{}, errors.New("original journal line has an invalid account")
	}
	rows, err := LedgerLines(ctx, sp, scope, dbqueryv8.LedgerLinesParams{EntryID: originalID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	lines := make([]journalLine, 0, len(rows))
	for _, r := range rows {
		line := journalLine{Line: ledger.Line{Currency: r.Currency}, account: r.AccountID}
		amounts := []struct {
			stored pgtype.Numeric
			target *decimal.Decimal
		}{
			{r.Credit, &line.Debit}, {r.Debit, &line.Credit},
			{r.BaseAmountCredit, &line.BaseDebit}, {r.BaseAmountDebit, &line.BaseCredit},
			{r.FxRate, &line.rate},
		}
		for _, amount := range amounts {
			*amount.target, err = storedDecimal(amount.stored)
			if err != nil {
				return pgtype.UUID{}, err
			}
		}
		if !currencyCode.MatchString(line.Currency) || !line.rate.IsPositive() ||
			line.Debit.IsNegative() || line.Credit.IsNegative() || line.Debit.IsZero() == line.Credit.IsZero() ||
			line.BaseDebit.IsNegative() || line.BaseCredit.IsNegative() ||
			(line.Debit.IsZero() && !line.BaseDebit.IsZero()) || (line.Credit.IsZero() && !line.BaseCredit.IsZero()) {
			return pgtype.UUID{}, errors.New("invalid original journal line")
		}
		lines = append(lines, line)
	}
	if original.ReversedByEntryID.Valid {
		if err := validateJournal(lines); err != nil {
			return pgtype.UUID{}, err
		}
		match, err := q.LedgerReversalMatches(ctx, dbqueryv8.LedgerReversalMatchesParams{
			BusinessID: scope.UUID(), OriginalID: originalID, ReversalID: original.ReversedByEntryID,
		})
		if err != nil {
			return pgtype.UUID{}, err
		}
		if !match {
			return pgtype.UUID{}, errors.New("invalid journal reversal link")
		}
		return original.ReversedByEntryID, nil
	}
	if memo == "" {
		memo = "Reversal of " + originalID.String()
	}
	id, err := insert(ctx, sp, scope, original.SourceType, original.SourceID, pgtype.Text{String: memo, Valid: true}, lines)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = LedgerLinkReversal(ctx, sp, scope, dbqueryv8.LedgerLinkReversalParams{EntryID: originalID, ReversedByEntryID: id}); err != nil {
		return pgtype.UUID{}, err
	}
	if err = sp.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
