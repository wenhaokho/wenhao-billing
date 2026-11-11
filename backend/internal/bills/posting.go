package bills

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own (PORT-NOTES 164): a bill posts Dr expense / Cr payable for its
// amount when it opens; an edit of an OPEN bill's posting fields reverses
// and re-posts, and going back to DRAFT or VOID reverses. Python posted
// none of these.

// expenseTypes are the account types a bill line may be booked to.
var expenseTypes = map[string]bool{"EXPENSE": true, "COGS": true, "ASSET": true}

// ExpenseShares splits bill's amount across its expense accounts. Each line
// goes to its own expense account, else its item's, else
// ledger.DefaultExpenseCode (6000); the lines are grouped by account, and
// each group takes RoundHalfEven4(amount × group / Σ lines), the largest
// group (the first in line order on a tie) taking the remainder so the
// shares sum to amount. A bill whose lines sum to 0 puts its whole amount on
// 6000. A group left with 0 is dropped.
//
// An account that is not EXPENSE, COGS or ASSET, or is inactive, is refused
// (ledger.ErrLedger). Exported for the back-post (plan 12 Task 7).
func ExpenseShares(ctx context.Context, tx pgx.Tx, bill billsdb.Bill) ([]ledger.Share, error) {
	lines, err := billsdb.New(tx).ListBillLineAccounts(ctx, bill.BillID)
	if err != nil {
		return nil, err
	}
	byCode, err := ledger.AccountsByCode(ctx, tx, []string{ledger.DefaultExpenseCode})
	if err != nil {
		return nil, err
	}
	fallback, ok := byCode[ledger.DefaultExpenseCode]
	if !ok {
		return nil, ledger.Refuse("chart_of_accounts missing code %s", ledger.DefaultExpenseCode)
	}

	var order []int32
	groups := map[int32]decimal.Decimal{}
	total := decimal.Zero
	for _, l := range lines {
		id := fallback.ID
		switch {
		case l.LineAccountID != nil:
			id = *l.LineAccountID
		case l.ItemAccountID != nil:
			id = *l.ItemAccountID
		}
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = groups[id].Add(l.Amount)
		total = total.Add(l.Amount)
	}
	if !total.IsPositive() {
		order, groups = []int32{fallback.ID}, map[int32]decimal.Decimal{fallback.ID: decimal.NewFromInt(1)}
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
		if !expenseTypes[a.Type] {
			return nil, ledger.Refuse("account %s (%s) cannot take an expense; set the line's or the item's expense account to an EXPENSE, COGS or ASSET account", a.Code, a.Type)
		}
		if !a.Active {
			return nil, ledger.Refuse("account %s is inactive; set the line's or the item's expense account to an active account", a.Code)
		}
	}

	shares := make([]ledger.Share, len(order))
	largest, sum := 0, decimal.Zero
	for i, id := range order {
		amt := platform.RoundHalfEven4(bill.Amount.Mul(groups[id]).Div(total))
		shares[i] = ledger.Share{AccountID: id, Amount: amt}
		sum = sum.Add(amt)
		if groups[id].GreaterThan(groups[order[largest]]) {
			largest = i
		}
	}
	shares[largest].Amount = shares[largest].Amount.Add(bill.Amount.Sub(sum))

	out := shares[:0]
	for _, s := range shares {
		if !s.Amount.IsZero() {
			out = append(out, s)
		}
	}
	return out, nil
}

// openBill posts bill's issue entry, as the bill now stands (lines stored,
// amount repriced). The caller holds the bill's row lock (FOR UPDATE), so
// ledger.PostIssued's one-entry check is not raced. A bill of 0 posts
// nothing. The rate date and the posting day are the issue date, else the
// created_at date (bills never default issue_date).
func openBill(ctx context.Context, tx pgx.Tx, base string, bill billsdb.Bill) error {
	if !bill.Amount.IsPositive() {
		return nil
	}
	shares, err := ExpenseShares(ctx, tx, bill)
	if err != nil {
		return err
	}
	on := issueDay(bill)
	_, err = ledger.PostIssued(ctx, tx, base, ledger.Document{
		SourceType: ledger.SourceBill, ID: bill.BillID, Currency: bill.Currency, Amount: bill.Amount,
		RateDate: on, IssueDate: on, Memo: "Bill " + billLabel(bill) + " opened", Shares: shares,
	})
	return err
}

// unpostBill reverses bill id's active issue entry with memo, when it has
// one. The caller holds the bill's row lock.
func unpostBill(ctx context.Context, tx pgx.Tx, id uuid.UUID, memo string) error {
	_, err := ledger.ReverseIssued(ctx, tx, ledger.SourceBill, id, memo)
	return err
}

// issueDay is bill's issue date, else its created_at date.
func issueDay(bill billsdb.Bill) time.Time {
	if bill.IssueDate.Valid {
		d := bill.IssueDate.Time
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	}
	c := bill.CreatedAt.UTC()
	return time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC)
}
