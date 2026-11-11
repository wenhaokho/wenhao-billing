package invoices

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own (PORT-NOTES 162): an issued invoice posts Dr receivable / Cr
// revenue for its amount, and a void reverses that entry. Python posted
// neither.

// ModeRevenueCode is the revenue account of a line without an item revenue
// account, by the invoice's billing mode: RECURRING 4100, USAGE 4200, and
// anything else (MILESTONE, free-text types) 4000.
func ModeRevenueCode(invoiceType string) string {
	switch invoiceType {
	case "RECURRING":
		return ledger.SupportCode
	case "USAGE":
		return ledger.UsageCode
	}
	return ledger.FixedFeeCode
}

// RevenueShares splits inv's amount across its revenue accounts. Each line
// goes to its item's revenue account, else the billing mode's
// (ModeRevenueCode); the lines are grouped by account, and each group takes
// RoundHalfEven4(amount × group / Σ lines), the largest group (the first in
// line order on a tie) taking the remainder so the shares sum to amount. An
// invoice whose lines sum to 0 (none, as a milestone) puts its whole amount
// on the mode account. A group left with 0 is dropped.
//
// An account that is not INCOME, or is inactive, is refused
// (ledger.ErrLedger). Exported for
// the back-post (plan 12 Task 7).
func RevenueShares(ctx context.Context, tx pgx.Tx, inv invoicesdb.Invoice) ([]ledger.Share, error) {
	lines, err := invoicesdb.New(tx).ListInvoiceLineAccounts(ctx, inv.InvoiceID)
	if err != nil {
		return nil, err
	}
	modeCode := ModeRevenueCode(inv.InvoiceType)
	byCode, err := ledger.AccountsByCode(ctx, tx, []string{modeCode})
	if err != nil {
		return nil, err
	}
	mode, ok := byCode[modeCode]
	if !ok {
		return nil, ledger.Refuse("chart_of_accounts missing code %s", modeCode)
	}

	var order []int32
	groups := map[int32]decimal.Decimal{}
	total := decimal.Zero
	for _, l := range lines {
		id := mode.ID
		if l.RevenueAccountID != nil {
			id = *l.RevenueAccountID
		}
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = groups[id].Add(l.Amount)
		total = total.Add(l.Amount)
	}
	if !total.IsPositive() {
		order, groups = []int32{mode.ID}, map[int32]decimal.Decimal{mode.ID: decimal.NewFromInt(1)}
		total = decimal.NewFromInt(1)
	}

	accounts, err := ledger.AccountsByID(ctx, tx, order)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		a, ok := accounts[id]
		if !ok {
			return nil, ledger.Refuse("chart_of_accounts missing account %d", id)
		}
		if a.Type != "INCOME" {
			return nil, ledger.Refuse("account %s (%s) cannot take revenue; set the item's revenue account to an INCOME account", a.Code, a.Type)
		}
		if !a.Active {
			return nil, ledger.Refuse("account %s is inactive and cannot take revenue; reactivate it or choose another revenue account", a.Code)
		}
	}

	shares := make([]ledger.Share, len(order))
	largest, sum := 0, decimal.Zero
	for i, id := range order {
		amt := platform.RoundHalfEven4(inv.Amount.Mul(groups[id]).Div(total))
		shares[i] = ledger.Share{AccountID: id, Amount: amt}
		sum = sum.Add(amt)
		if groups[id].GreaterThan(groups[order[largest]]) {
			largest = i
		}
	}
	shares[largest].Amount = shares[largest].Amount.Add(inv.Amount.Sub(sum))

	out := shares[:0]
	for _, s := range shares {
		if !s.Amount.IsZero() {
			out = append(out, s)
		}
	}
	return out, nil
}

// postIssue posts inv's issue entry. The caller holds inv's row lock
// (FOR UPDATE), so ledger.PostIssued's one-entry check is not raced. The
// rate date and the posting day are the issue date, else the created_at
// date (an invoice PATCH can null issue_date).
func postIssue(ctx context.Context, tx pgx.Tx, base string, inv invoicesdb.Invoice) error {
	shares, err := RevenueShares(ctx, tx, inv)
	if err != nil {
		return err
	}
	on := issueDay(inv)
	who := inv.InvoiceID.String()
	if inv.InvoiceNumber != nil && *inv.InvoiceNumber != "" {
		who = *inv.InvoiceNumber
	}
	_, err = ledger.PostIssued(ctx, tx, base, ledger.Document{
		SourceType: ledger.SourceInvoice, ID: inv.InvoiceID, Currency: inv.Currency, Amount: inv.Amount,
		RateDate: on, IssueDate: on, Memo: "Invoice " + who + " issued", Shares: shares,
	})
	return err
}

// issueDay is inv's issue date, else its created_at date (zero when both are
// null: the ledger then posts at now).
func issueDay(inv invoicesdb.Invoice) time.Time {
	if inv.IssueDate.Valid {
		d := inv.IssueDate.Time
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	}
	if inv.CreatedAt != nil {
		c := inv.CreatedAt.UTC()
		return time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}
