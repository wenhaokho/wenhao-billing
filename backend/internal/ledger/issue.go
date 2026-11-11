package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The source types of the entries a document posts.
const (
	SourceInvoice     = "INVOICE"
	SourceBill        = "BILL"
	SourceBillPayment = "BILL_PAYMENT"
)

// Share is the part of a document's amount booked to one revenue (invoice)
// or expense (bill) account.
type Share struct {
	AccountID int32
	Amount    decimal.Decimal
}

// Document is an issued invoice or bill. RateDate's calendar date is the FX
// as-of date; IssueDate's calendar date is the day the entry is posted on
// (zero: now). Shares sum to Amount, each above zero.
type Document struct {
	SourceType          string
	ID                  uuid.UUID
	Currency            string
	Amount              decimal.Decimal
	RateDate, IssueDate time.Time
	Memo                string
	Shares              []Share
}

// PostIssued posts a document's issue (accrual) entry. An invoice is DR
// receivable Amount / CR each share; a bill is DR each share / CR payable
// Amount. Every line converts into baseCurrency at RateDate's rate; the
// control line's base is fx.Convert(Amount), and the largest share (the
// lowest code on a tie) takes control − Σ the other shares' bases, so the
// entry balances in base whatever the rounding.
//
// The entry is posted at IssueDate 00:00, or now when that is later. A
// document with an active entry already is refused; the caller holds the
// document's row lock so two posts run one after the other. Whether a
// share's account fits (type, active) is the caller's check.
//
// Nothing is written on a refusal (ErrLedger) or a missing rate
// (fx.ErrRateMissing).
func PostIssued(ctx context.Context, tx pgx.Tx, baseCurrency string, d Document) (Entry, error) {
	var control string
	switch d.SourceType {
	case SourceInvoice:
		control = ReceivableCode
	case SourceBill:
		control = PayableCode
	default:
		// A programming error, not a refusal.
		return Entry{}, fmt.Errorf("ledger: %s is not a document source type", d.SourceType)
	}
	who := d.SourceType + " " + d.ID.String()

	_, active, err := ActiveEntryFor(ctx, tx, d.SourceType, d.ID)
	if err != nil {
		return Entry{}, err
	}
	if active {
		return Entry{}, refuse("%s already has an active issue entry", who)
	}
	if !d.Amount.IsPositive() {
		return Entry{}, refuse("%s amount must be > 0 (got %s)", who, platform.PyDecimalStr(d.Amount))
	}
	sum := decimal.Zero
	ids := make([]int32, len(d.Shares))
	for i, s := range d.Shares {
		if !s.Amount.IsPositive() {
			return Entry{}, refuse("%s share on account %d must be > 0 (got %s)", who, s.AccountID, platform.PyDecimalStr(s.Amount))
		}
		sum = sum.Add(s.Amount)
		ids[i] = s.AccountID
	}
	if !sum.Equal(d.Amount) {
		return Entry{}, refuse("%s shares sum to %s, not the amount %s", who, platform.PyDecimalStr(sum), platform.PyDecimalStr(d.Amount))
	}

	accounts, err := AccountsByID(ctx, tx, ids)
	if err != nil {
		return Entry{}, err
	}
	codes := []string{control}
	for _, s := range d.Shares {
		a, ok := accounts[s.AccountID]
		if !ok {
			return Entry{}, refuse("%s chart_of_accounts missing account %d", who, s.AccountID)
		}
		codes = append(codes, a.Code)
	}
	q := ledgerdb.New(tx)
	b, err := newBuilder(ctx, q, fxdb.New(tx), baseCurrency, d.RateDate, codes...)
	if err != nil {
		return Entry{}, err
	}

	invoice := d.SourceType == SourceInvoice
	ctl, err := b.side(control, d.Amount, invoice, d.Currency)
	if err != nil {
		return Entry{}, err
	}
	lines := make([]Line, len(d.Shares))
	largest := 0
	for i, s := range d.Shares {
		if lines[i], err = b.side(codes[i+1], s.Amount, !invoice, d.Currency); err != nil {
			return Entry{}, err
		}
		if c := s.Amount.Cmp(d.Shares[largest].Amount); c > 0 || (c == 0 && codes[i+1] < codes[largest+1]) {
			largest = i
		}
	}
	// The shares convert one by one, so their rounded bases can miss the
	// control's by a few 0.0001s; the largest share absorbs the difference.
	rest := decimal.Zero
	for i, l := range lines {
		if i != largest {
			rest = rest.Add(l.BaseDebit).Add(l.BaseCredit)
		}
	}
	absorbed := ctl.BaseDebit.Add(ctl.BaseCredit).Sub(rest)
	if invoice {
		lines[largest].BaseCredit = absorbed
		lines = append([]Line{ctl}, lines...)
	} else {
		lines[largest].BaseDebit = absorbed
		lines = append(lines, ctl)
	}

	memo := d.Memo
	if memo == "" {
		memo = who + " issued"
	}
	e := Entry{ID: uuid.New(), SourceType: d.SourceType, SourceID: d.ID, Memo: &memo, Lines: lines}
	if err := checkBalanced(e.Lines); err != nil {
		return Entry{}, err
	}
	var postedAt *time.Time
	if !d.IssueDate.IsZero() {
		day := time.Date(d.IssueDate.Year(), d.IssueDate.Month(), d.IssueDate.Day(), 0, 0, 0, 0, time.UTC)
		postedAt = &day
	}
	if err := insert(ctx, q, e, postedAt); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// ReverseIssued reverses the source's active entry with memo (posted at now)
// and reports true, or reports false when it has none.
func ReverseIssued(ctx context.Context, tx pgx.Tx, sourceType string, id uuid.UUID, memo string) (bool, error) {
	entryID, ok, err := ActiveEntryFor(ctx, tx, sourceType, id)
	if err != nil || !ok {
		return false, err
	}
	if _, err := postReversal(ctx, tx, entryID, &memo, nil); err != nil {
		return false, err
	}
	return true, nil
}

// side is a line of amount on the account with code: a debit when debit is
// true, else a credit.
func (b *builder) side(code string, amount decimal.Decimal, debit bool, currency string) (Line, error) {
	if debit {
		return b.line(code, amount, decimal.Zero, currency)
	}
	return b.line(code, decimal.Zero, amount, currency)
}
