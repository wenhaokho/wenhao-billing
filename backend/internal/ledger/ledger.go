// Package ledger posts double-entry journal entries, as Python's
// services/ledger.py does: a cleared payment (bank, withholding to Prepaid
// Tax, receivable, realised FX gain or loss across currencies) and the
// reversal of an entry. It runs inside the caller's transaction and has no
// routes.
//
// Every entry balances in the base currency; an entry whose lines share one
// currency also balances in that currency. A cross-currency entry balances
// through its FX gain or loss line, booked in the base currency.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
)

// The chart-of-accounts codes the ledger posts to.
const (
	BankCode       = "1000" // Operating Bank
	ReceivableCode = "1100" // Accounts Receivable
	PrepaidTaxCode = "1300" // Prepaid Income Tax (PPh withheld by the customer)
	FXGainCode     = "4900" // FX Gain (Realised)
	FXLossCode     = "5900" // FX Loss (Realised)
)

// ErrLedger is matched (errors.Is) by every refusal; the error's text is
// Python's LedgerError sentence.
var ErrLedger = errors.New("ledger")

type ledgerError struct{ msg string }

func (e ledgerError) Error() string        { return e.msg }
func (e ledgerError) Is(target error) bool { return target == ErrLedger }

func refuse(format string, args ...any) error { return ledgerError{fmt.Sprintf(format, args...)} }

// Payment is the payment being posted. Date is its payment date: its
// calendar date is the FX as-of date (see fx.GetRate).
type Payment struct {
	ID       uuid.UUID
	Status   string
	Amount   decimal.Decimal
	Currency string
	Date     time.Time
}

// Invoice is the invoice the payment settles.
type Invoice struct {
	ID         uuid.UUID
	Currency   string
	BalanceDue decimal.Decimal
}

// Line is a journal line: one side positive, the other zero, and the base
// amounts restated at FXRate.
type Line struct {
	AccountID                     int32
	Debit, Credit                 decimal.Decimal
	Currency                      string
	BaseDebit, BaseCredit, FXRate decimal.Decimal
}

// Entry is a posted journal entry; posted_at is the database's.
type Entry struct {
	ID         uuid.UUID
	SourceType string
	SourceID   uuid.UUID
	Memo       *string
	Lines      []Line
}

// PostPaymentCleared posts DR bank (and DR Prepaid Tax for withheld, in the
// invoice currency, when it is above zero) / CR receivable for a CLEARED
// payment, converting each line into baseCurrency on the payment date.
//
// Same currency: the receivable is relieved by p.Amount + withheld, its base
// credit the exact sum of the debit lines' base debits. Cross currency: the
// payment settles the invoice's whole balance; the receivable is credited
// inv.BalanceDue in the invoice currency and the base difference (bank +
// withheld − receivable) is booked to FX Loss (below zero) or FX Gain (above),
// in baseCurrency.
//
// Nothing is written unless the entry balances. A missing rate returns the
// error fx returns (errors.Is fx.ErrRateMissing).
func PostPaymentCleared(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal) (Entry, error) {
	if p.Status != "CLEARED" {
		return Entry{}, refuse("refuse to post ledger for payment %s with status %s — only CLEARED may post", p.ID, pyRepr(p.Status))
	}
	if withheld.IsNegative() {
		return Entry{}, refuse("withheld tax cannot be negative (got %s)", pyStr(withheld))
	}
	q := ledgerdb.New(tx)
	b, err := newBuilder(ctx, q, fxdb.New(tx), baseCurrency, p.Date)
	if err != nil {
		return Entry{}, err
	}

	bank, err := b.line(BankCode, p.Amount, decimal.Zero, p.Currency)
	if err != nil {
		return Entry{}, err
	}
	lines := []Line{bank}
	withheldBase := decimal.Zero
	if withheld.IsPositive() {
		wht, err := b.line(PrepaidTaxCode, withheld, decimal.Zero, inv.Currency)
		if err != nil {
			return Entry{}, err
		}
		lines = append(lines, wht)
		withheldBase = wht.BaseDebit
	}

	if p.Currency == inv.Currency {
		ar, err := b.line(ReceivableCode, decimal.Zero, p.Amount.Add(withheld), p.Currency)
		if err != nil {
			return Entry{}, err
		}
		// Bank and withholding convert separately, so their rounded base
		// amounts can differ from base(amount + withheld) by 0.0001. All
		// lines share one rate; relieve AR by the exact sum of the debit bases.
		debitBase := decimal.Zero
		for _, l := range lines {
			debitBase = debitBase.Add(l.BaseDebit)
		}
		ar.BaseCredit = debitBase
		lines = append(lines, ar)
	} else {
		if !inv.BalanceDue.IsPositive() {
			return Entry{}, refuse("cross-currency settlement requires invoice balance_due > 0 (invoice %s balance=%s)", inv.ID, pyStr(inv.BalanceDue))
		}
		ar, err := b.line(ReceivableCode, decimal.Zero, inv.BalanceDue, inv.Currency)
		if err != nil {
			return Entry{}, err
		}
		lines = append(lines, ar)
		// Below zero we received less value than we relieved: an FX loss
		// (DR). Above zero, more: an FX gain (CR).
		diff := bank.BaseDebit.Add(withheldBase).Sub(ar.BaseCredit)
		if !diff.IsZero() {
			var plug Line
			if diff.IsNegative() {
				plug, err = b.line(FXLossCode, diff.Neg(), decimal.Zero, baseCurrency)
			} else {
				plug, err = b.line(FXGainCode, decimal.Zero, diff, baseCurrency)
			}
			if err != nil {
				return Entry{}, err
			}
			lines = append(lines, plug)
		}
	}

	memo := fmt.Sprintf("Payment %s cleared against invoice %s", p.ID, inv.ID)
	e := Entry{ID: uuid.New(), SourceType: "PAYMENT", SourceID: p.ID, Memo: &memo, Lines: lines}
	if err := checkBalanced(e.Lines); err != nil {
		return Entry{}, err
	}
	if err := insert(ctx, q, e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// PostReversal posts a new entry with the original's source, memo (or
// "Reversal of <id>" when memo is nil or empty) and every line with debit and
// credit, and base debit and base credit, swapped at the same rate; then
// points the original's reversed_by_entry_id at it. The original is locked
// until the caller's transaction ends, so a concurrent reversal of it waits
// and is then refused. The reversal copies the original's base amounts, so
// baseCurrency is not used; it keeps the two posting calls alike.
func PostReversal(ctx context.Context, tx pgx.Tx, _ string, originalID uuid.UUID, memo *string) (Entry, error) {
	q := ledgerdb.New(tx)
	orig, err := q.LockEntry(ctx, originalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, refuse("entry %s not found", originalID)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: lock entry: %w", err)
	}
	if orig.Reversed {
		return Entry{}, refuse("entry %s already reversed", originalID)
	}
	rows, err := q.EntryLines(ctx, originalID)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: entry lines: %w", err)
	}
	lines := make([]Line, len(rows))
	for i, r := range rows {
		lines[i] = Line{
			AccountID: r.AccountID, Debit: r.Credit, Credit: r.Debit, Currency: r.Currency,
			BaseDebit: r.BaseAmountCredit, BaseCredit: r.BaseAmountDebit, FXRate: r.FxRate,
		}
	}
	m := "Reversal of " + originalID.String()
	if memo != nil && *memo != "" {
		m = *memo
	}
	e := Entry{ID: uuid.New(), SourceType: orig.SourceType, SourceID: orig.SourceID, Memo: &m, Lines: lines}
	if err := checkBalanced(e.Lines); err != nil {
		return Entry{}, err
	}
	if err := insert(ctx, q, e); err != nil {
		return Entry{}, err
	}
	if err := q.SetReversedBy(ctx, ledgerdb.SetReversedByParams{ReversedBy: e.ID, EntryID: originalID}); err != nil {
		return Entry{}, fmt.Errorf("ledger: set reversed_by_entry_id: %w", err)
	}
	return e, nil
}

// builder makes lines converted into base on one date.
type builder struct {
	ctx      context.Context
	fx       *fxdb.Queries
	base     string
	on       time.Time
	accounts map[string]int32
}

func newBuilder(ctx context.Context, q *ledgerdb.Queries, fq *fxdb.Queries, base string, on time.Time) (*builder, error) {
	rows, err := q.AccountIDsByCode(ctx, []string{BankCode, ReceivableCode, PrepaidTaxCode, FXGainCode, FXLossCode})
	if err != nil {
		return nil, fmt.Errorf("ledger: account ids: %w", err)
	}
	accounts := make(map[string]int32, len(rows))
	for _, r := range rows {
		accounts[r.Code] = r.AccountID
	}
	return &builder{ctx: ctx, fx: fq, base: base, on: on, accounts: accounts}, nil
}

// line is a line on the account with code, its one positive side converted
// into base. A code missing from the chart of accounts is refused when a line
// needs it, as Python looks each code up as it builds the line.
func (b *builder) line(code string, debit, credit decimal.Decimal, currency string) (Line, error) {
	id, ok := b.accounts[code]
	if !ok {
		return Line{}, refuse("chart_of_accounts missing code %s", code)
	}
	var amount decimal.Decimal
	isDebit := debit.IsPositive() && credit.IsZero()
	switch {
	case isDebit:
		amount = debit
	case credit.IsPositive() && debit.IsZero():
		amount = credit
	default:
		return Line{}, refuse("journal line for account %s needs exactly one positive side (debit=%s credit=%s)", code, pyStr(debit), pyStr(credit))
	}
	conv, err := fx.Convert(b.ctx, b.fx, amount, currency, b.on, b.base)
	if err != nil {
		return Line{}, err
	}
	l := Line{
		AccountID: id, Debit: debit, Credit: credit, Currency: currency,
		BaseDebit: decimal.Zero, BaseCredit: decimal.Zero, FXRate: conv.Rate,
	}
	if isDebit {
		l.BaseDebit = conv.BaseAmount
	} else {
		l.BaseCredit = conv.BaseAmount
	}
	return l, nil
}

// checkBalanced refuses lines whose base amounts do not net to zero, and lines
// that share one currency but do not net to zero in it.
func checkBalanced(lines []Line) error {
	net := decimal.Zero
	for _, l := range lines {
		net = net.Add(l.BaseDebit.Sub(l.BaseCredit))
	}
	if !net.IsZero() {
		return refuse("journal entry unbalanced in base currency: net=%s", pyStr(net))
	}
	if len(lines) == 0 {
		return nil
	}
	for _, l := range lines[1:] {
		if l.Currency != lines[0].Currency {
			return nil // the plug lives in the base currency
		}
	}
	net = decimal.Zero
	for _, l := range lines {
		net = net.Add(l.Debit.Sub(l.Credit))
	}
	if !net.IsZero() {
		return refuse("journal entry unbalanced in %s: net=%s", lines[0].Currency, pyStr(net))
	}
	return nil
}

func insert(ctx context.Context, q *ledgerdb.Queries, e Entry) error {
	if err := q.InsertEntry(ctx, ledgerdb.InsertEntryParams{
		EntryID: e.ID, SourceType: e.SourceType, SourceID: e.SourceID, Memo: e.Memo,
	}); err != nil {
		return fmt.Errorf("ledger: insert entry: %w", err)
	}
	for _, l := range e.Lines {
		if err := q.InsertLine(ctx, ledgerdb.InsertLineParams{
			LineID: uuid.New(), EntryID: e.ID, AccountID: l.AccountID,
			Debit: l.Debit, Credit: l.Credit, Currency: l.Currency,
			BaseAmountDebit: l.BaseDebit, BaseAmountCredit: l.BaseCredit, FxRate: l.FXRate,
		}); err != nil {
			return fmt.Errorf("ledger: insert line: %w", err)
		}
	}
	return nil
}

// pyStr prints d as Python's str(Decimal) does: plain notation, keeping the
// exponent's places (0.0000, -1.50), unless the exponent is positive or the
// number is below 1E-6, which print in scientific notation (1E+3, 1E-7).
func pyStr(d decimal.Decimal) string {
	digits := new(big.Int).Abs(d.Coefficient()).String()
	sign := ""
	if d.IsNegative() {
		sign = "-"
	}
	exp := int(d.Exponent())
	left := exp + len(digits)
	dot := 1
	if exp <= 0 && left > -6 {
		dot = left
	}
	var intPart, frac string
	switch {
	case dot <= 0:
		intPart, frac = "0", "."+strings.Repeat("0", -dot)+digits
	case dot >= len(digits):
		intPart = digits + strings.Repeat("0", dot-len(digits))
	default:
		intPart, frac = digits[:dot], "."+digits[dot:]
	}
	e := ""
	if left != dot {
		e = fmt.Sprintf("E%+d", left-dot)
	}
	return sign + intPart + frac + e
}

// pyRepr quotes s as Python's repr(str) does for printable text: single
// quotes, or double quotes when s holds a single quote and no double one.
func pyRepr(s string) string {
	q := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteRune(q)
	for _, r := range s {
		switch {
		case r == '\\' || r == q:
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune(q)
	return b.String()
}
