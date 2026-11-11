// Package ledger posts double-entry journal entries, as Python's
// services/ledger.py does: a cleared payment (bank, withholding to Prepaid
// Tax, receivable at the invoice's issue basis, realised FX gain or loss)
// and the reversal of an entry; and Go's own document issue entries. It runs inside the caller's transaction and has no
// routes.
//
// Every entry balances in the base currency; an entry whose lines share one
// currency also balances in that currency. An entry with an FX gain or loss
// line (booked in the base currency) balances through it.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The chart-of-accounts codes the ledger posts to.
const (
	BankCode       = "1000" // Operating Bank
	ReceivableCode = "1100" // Accounts Receivable
	PrepaidTaxCode = "1300" // Prepaid Income Tax (PPh withheld by the customer)
	PayableCode    = "2000" // Accounts Payable
	FixedFeeCode   = "4000" // Fixed-Fee Revenue
	SupportCode    = "4100" // Support/Maintenance Revenue
	UsageCode      = "4200" // Usage-Based Revenue (AI Tokens)
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

// Refuse is an ErrLedger whose text is the formatted sentence: a caller's own
// check on what it posts (a share's account that cannot take it) refuses as
// the ledger does.
func Refuse(format string, args ...any) error { return refuse(format, args...) }

// Payment is the payment being posted. Date is its payment date: its
// calendar date is the FX as-of date (see fx.GetRate). Memo is the entry's
// memo; empty is "Payment <id> cleared against invoice <invoice id>" (the
// back-post's restatements set their own, PORT-NOTES 166).
type Payment struct {
	ID       uuid.UUID
	Status   string
	Amount   decimal.Decimal
	Currency string
	Date     time.Time
	Memo     string
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
// payment, posted at now. Bank and Prepaid Tax convert into baseCurrency on
// the payment date.
//
// The receivable is relieved by R: p.Amount + withheld in the same currency;
// across currencies the payment settles the invoice's whole balance, so R is
// inv.BalanceDue in the invoice currency. R is relieved at the invoice's
// issue basis (InvoiceBasis, reliefBase), not at the payment date's rate; its
// fx_rate is that base / R to 8 places. The base difference (Σ debit bases −
// the receivable's base) is realised FX in baseCurrency: above zero FX Gain
// (CR), below zero FX Loss (DR), in every currency combination
// (PORT-NOTES 163). An invoice with no active issue entry is refused.
//
// An amount of 0 is posted only with withheld above 0 in the invoice's
// currency (a manual withholding-only payment): Prepaid Tax against the
// receivable, no bank line. Any other amount that is not positive is a plain
// error.
//
// Nothing is written unless the entry balances. A missing rate returns the
// error fx returns (errors.Is fx.ErrRateMissing).
func PostPaymentCleared(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal) (Entry, error) {
	return postPaymentCleared(ctx, tx, baseCurrency, p, inv, withheld, nil)
}

// PostPaymentClearedAt is PostPaymentCleared posted at postedAt (never later
// than now): a back-post restatement keeps the original entry's posted_at.
func PostPaymentClearedAt(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal, postedAt time.Time) (Entry, error) {
	return postPaymentCleared(ctx, tx, baseCurrency, p, inv, withheld, &postedAt)
}

func postPaymentCleared(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal, postedAt *time.Time) (Entry, error) {
	if p.Status != "CLEARED" {
		return Entry{}, refuse("refuse to post ledger for payment %s with status %s — only CLEARED may post", p.ID, platform.PyRepr(p.Status))
	}
	if withheld.IsNegative() {
		return Entry{}, refuse("withheld tax cannot be negative (got %s)", platform.PyDecimalStr(withheld))
	}
	// A payment that is not positive never reaches here from intake or
	// approval (validated gt=0); Python crashes on one. A programming error,
	// not a refusal: a plain error, so it is never routed to manual review.
	// The one exception is Go's: a manual invoice payment of withholding only
	// (amount 0, withheld above 0, the invoice's currency) posts DR Prepaid
	// Tax / CR receivable with no bank line (PORT-NOTES 161).
	sameCurrency := p.Currency == inv.Currency
	withholdingOnly := p.Amount.IsZero() && withheld.IsPositive() && sameCurrency
	if !p.Amount.IsPositive() && !withholdingOnly {
		return Entry{}, fmt.Errorf("ledger: payment %s amount must be > 0 (got %s)", p.ID, platform.PyDecimalStr(p.Amount))
	}
	relief := p.Amount.Add(withheld)
	if !sameCurrency {
		if !inv.BalanceDue.IsPositive() {
			return Entry{}, refuse("cross-currency settlement requires invoice balance_due > 0 (invoice %s balance=%s)", inv.ID, platform.PyDecimalStr(inv.BalanceDue))
		}
		relief = inv.BalanceDue
	}
	q := ledgerdb.New(tx)
	fq := fxdb.New(tx)
	b, err := newBuilder(ctx, q, fq, baseCurrency, p.Date, BankCode, ReceivableCode, PrepaidTaxCode, FXGainCode, FXLossCode)
	if err != nil {
		return Entry{}, err
	}

	var lines []Line
	if !withholdingOnly {
		bank, err := b.line(BankCode, p.Amount, decimal.Zero, p.Currency)
		if err != nil {
			return Entry{}, err
		}
		lines = append(lines, bank)
	}
	if withheld.IsPositive() {
		wht, err := b.line(PrepaidTaxCode, withheld, decimal.Zero, inv.Currency)
		if err != nil {
			return Entry{}, err
		}
		lines = append(lines, wht)
	}

	basis, err := InvoiceBasis(ctx, tx, inv.ID)
	if err != nil {
		return Entry{}, err
	}
	if !basis.Issued {
		return Entry{}, refuse("invoice %s has no issue entry — run api backpost-ledger", inv.ID)
	}
	// Go's own (PORT-NOTES 7): across currencies the relief is balance_due,
	// which a reversal under Python's arithmetic can leave above what the
	// ledger still holds. Relieving more than the remaining face would book
	// the excess as a false FX loss; refused instead (safe-stop: intake holds
	// the payment for manual review, approval answers 400).
	if !sameCurrency && relief.GreaterThan(basis.Face) {
		return Entry{}, refuse("cross-currency settlement of invoice %s would relieve %s %s but the ledger receivable is %s %s; correct balance_due first",
			inv.ID, relief.StringFixed(4), inv.Currency, basis.Face.StringFixed(4), inv.Currency)
	}
	arBase, err := reliefBase("invoice "+inv.ID.String()+" receivable", basis, relief, func(x decimal.Decimal) (decimal.Decimal, error) {
		c, err := fx.Convert(ctx, fq, x, inv.Currency, p.Date, baseCurrency)
		return c.BaseAmount, err
	})
	if err != nil {
		return Entry{}, err
	}
	arID, ok := b.accounts[ReceivableCode]
	if !ok {
		return Entry{}, refuse("chart_of_accounts missing code %s", ReceivableCode)
	}
	lines = append(lines, Line{
		AccountID: arID, Debit: decimal.Zero, Credit: relief, Currency: inv.Currency,
		BaseDebit: decimal.Zero, BaseCredit: arBase, FXRate: arBase.DivRound(relief, 8),
	})

	// Below zero we received less value than we relieved: an FX loss (DR).
	// Above zero, more: an FX gain (CR).
	diff := arBase.Neg()
	for _, l := range lines {
		diff = diff.Add(l.BaseDebit)
	}
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

	memo := p.Memo
	if memo == "" {
		memo = fmt.Sprintf("Payment %s cleared against invoice %s", p.ID, inv.ID)
	}
	e := Entry{ID: uuid.New(), SourceType: "PAYMENT", SourceID: p.ID, Memo: &memo, Lines: lines}
	if err := checkBalanced(e.Lines); err != nil {
		return Entry{}, err
	}
	if err := insert(ctx, q, e, postedAt); err != nil {
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
// baseCurrency is not used; it keeps the two posting calls alike. The
// reversal is posted at now.
func PostReversal(ctx context.Context, tx pgx.Tx, _ string, originalID uuid.UUID, memo *string) (Entry, error) {
	return postReversal(ctx, tx, originalID, memo, nil)
}

// PostReversalAt is PostReversal posted at postedAt (never later than now):
// a back-post restatement keeps the original entry's posted_at.
func PostReversalAt(ctx context.Context, tx pgx.Tx, originalID uuid.UUID, memo *string, postedAt time.Time) (Entry, error) {
	return postReversal(ctx, tx, originalID, memo, &postedAt)
}

// ReversePayment reverses a payment's entry (an invoice payment's or a bill
// payment's) with memo, posted at the original entry's posted_at, not now: a
// payment reversal is a correction (the payment never applied), so the
// reports as of any date never show a reversed payment, and a payment
// cleared, reversed and cleared again counts once (PORT-NOTES 167). Voids
// and edits are business events and reverse at now (ReverseIssued).
func ReversePayment(ctx context.Context, tx pgx.Tx, originalID uuid.UUID, memo *string) (Entry, error) {
	at, err := ledgerdb.New(tx).EntryPostedAt(ctx, originalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, refuse("entry %s not found", originalID)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: entry posted_at: %w", err)
	}
	return PostReversalAt(ctx, tx, originalID, memo, at)
}

func postReversal(ctx context.Context, tx pgx.Tx, originalID uuid.UUID, memo *string, postedAt *time.Time) (Entry, error) {
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
	if err := insert(ctx, q, e, postedAt); err != nil {
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

// newBuilder is a builder for lines on the accounts with codes.
func newBuilder(ctx context.Context, q *ledgerdb.Queries, fq *fxdb.Queries, base string, on time.Time, codes ...string) (*builder, error) {
	rows, err := q.AccountsByCode(ctx, codes)
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
		return Line{}, refuse("journal line for account %s needs exactly one positive side (debit=%s credit=%s)", code, platform.PyDecimalStr(debit), platform.PyDecimalStr(credit))
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
		return refuse("journal entry unbalanced in base currency: net=%s", platform.PyDecimalStr(net))
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
		return refuse("journal entry unbalanced in %s: net=%s", lines[0].Currency, platform.PyDecimalStr(net))
	}
	return nil
}

// Midnight is d's calendar day at 00:00 UTC: the posted_at of an entry dated
// on a document's date (an issue date, a payment date; PORT-NOTES 162, 167).
func Midnight(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// insert writes e, posted at postedAt (nil = now; never later than now).
func insert(ctx context.Context, q *ledgerdb.Queries, e Entry, postedAt *time.Time) error {
	if err := q.InsertEntry(ctx, ledgerdb.InsertEntryParams{
		EntryID: e.ID, SourceType: e.SourceType, SourceID: e.SourceID, Memo: e.Memo, PostedAt: postedAt,
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

// ActiveEntryFor returns the newest entry for the source that is neither
// reversed nor itself a reversal, or false when there is none. A reversal
// shares its original's source and has reversed_by_entry_id NULL, so that
// test alone would pick it; the original's reversed_by_entry_id points at the
// reversal, so an entry some other entry points at is a reversal and is
// skipped.
func ActiveEntryFor(ctx context.Context, tx pgx.Tx, sourceType string, sourceID uuid.UUID) (uuid.UUID, bool, error) {
	id, err := ledgerdb.New(tx).ActiveEntryFor(ctx, ledgerdb.ActiveEntryForParams{SourceType: sourceType, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("ledger: active entry: %w", err)
	}
	return id, true, nil
}

// Account is a chart-of-accounts row, for callers that check an account's
// type or that it is active before posting to it.
type Account struct {
	ID         int32
	Code, Type string
	Active     bool
}

// AccountsByID are the accounts with the ids, by id; an id with no account is
// absent.
func AccountsByID(ctx context.Context, tx pgx.Tx, ids []int32) (map[int32]Account, error) {
	rows, err := ledgerdb.New(tx).AccountsByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("ledger: accounts by id: %w", err)
	}
	out := make(map[int32]Account, len(rows))
	for _, r := range rows {
		out[r.AccountID] = Account{ID: r.AccountID, Code: r.Code, Type: r.Type, Active: r.Active}
	}
	return out, nil
}

// AccountsByCode are the accounts with the codes, by code; a code with no
// account is absent.
func AccountsByCode(ctx context.Context, tx pgx.Tx, codes []string) (map[string]Account, error) {
	rows, err := ledgerdb.New(tx).AccountsByCode(ctx, codes)
	if err != nil {
		return nil, fmt.Errorf("ledger: accounts by code: %w", err)
	}
	out := make(map[string]Account, len(rows))
	for _, r := range rows {
		out[r.Code] = Account{ID: r.AccountID, Code: r.Code, Type: r.Type, Active: r.Active}
	}
	return out, nil
}
