package bills

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// foreignKeys are the references a client names on the bill row, as the 422
// at the field each becomes (Python answered 500).
var foreignKeys = map[string]struct{ field, sentence string }{
	"bills_vendor_id_fkey":  {"vendor_id", "vendor not found"},
	"bills_project_id_fkey": {"project_id", "project not found"},
}

// mapWriteError is a failed write of a bill row as the client error it is:
// an unknown vendor or project is the 422 at its field. Any other error
// comes back unchanged.
func mapWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if fk, ok := foreignKeys[pg.ConstraintName]; ok {
			return platform.FieldError(fk.field, fk.sentence)
		}
	}
	return err
}

// lock reads bill id FOR UPDATE; an unknown one is errNotFound (the 404 of
// every bill route, once clientError has it).
func lock(ctx context.Context, q *billsdb.Queries, id uuid.UUID) (billsdb.Bill, error) {
	row, err := q.GetBillForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return billsdb.Bill{}, errNotFound
	}
	return row, err
}

// lineAmounts are the amounts of lines, each qty × price rounded half-even.
func lineAmounts(lines []BillLineItemIn) []decimal.Decimal {
	amounts := make([]decimal.Decimal, 0, len(lines))
	for _, ln := range lines {
		amounts = append(amounts, lineAmount(ln.Quantity.Decimal(), ln.UnitPrice.Decimal()))
	}
	return amounts
}

// insertLines stores the lines of bill id, each with its amount from
// lineAmounts, each position as sent, or the line's index when it is 0
// (`ln.position or idx`). An unknown item or expense account is the 422 at
// its line.
func insertLines(ctx context.Context, q *billsdb.Queries, id uuid.UUID, lines []BillLineItemIn, amounts []decimal.Decimal) error {
	for i, ln := range lines {
		qty, price, amount := ln.Quantity.Decimal(), ln.UnitPrice.Decimal(), amounts[i]
		position := ln.Position.Value
		if position == 0 {
			position = int32(i)
		}
		err := q.InsertBillLine(ctx, billsdb.InsertBillLineParams{
			LineItemID: uuid.New(), BillID: id, ItemID: pgUUID(ln.ItemID.Merge(nil)), ExpenseAccountID: ln.ExpenseAccountID.Merge(nil),
			Position: position, Description: ln.Description, Quantity: qty, UnitPrice: price, Amount: amount,
		})
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			switch pg.ConstraintName {
			case "bill_line_items_item_id_fkey":
				return platform.FieldError(fmt.Sprintf("line_items.%d.item_id", i), "item not found")
			case "bill_line_items_expense_account_id_fkey":
				return platform.FieldError(fmt.Sprintf("line_items.%d.expense_account_id", i), "expense account not found")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateBill is the create route's bill: the field checks, then createBill
// in tx. Exported for the MCP create_bill tool (inventory §4).
func CreateBill(ctx context.Context, tx pgx.Tx, b BillCreate) (BillOut, error) {
	if err := checkFields(b.BillFields, b.LineItems); err != nil {
		return BillOut{}, err
	}
	return createBill(ctx, tx, b)
}

// UpdateBill is the update route's bill: the field checks, then updateBill
// in tx. Exported for the MCP update_bill tool (inventory §4).
func UpdateBill(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID, b BillUpdate) (BillOut, error) {
	var lines []BillLineItemIn
	if b.LineItems != nil {
		lines = *b.LineItems
	}
	if err := checkFields(b.BillFields, lines); err != nil {
		return BillOut{}, err
	}
	return updateBill(ctx, tx, base, id, b)
}

// DeleteBill is MCP delete_bill's delete (inventory §4): a DRAFT bill
// locked, checked and deleted in tx. An unknown id is ErrNotFound; a bill
// of any other status is an ErrBilling refusal with Python's sentence, and
// so is a DRAFT bill with payment or ledger history (PORT-NOTES 165).
func DeleteBill(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := billsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return err
	}
	if cur.Status != "DRAFT" {
		return refuse("bill_not_draft", "only DRAFT bills can be deleted (got %s)", cur.Status)
	}
	if has, err := q.BillHasHistory(ctx, id); err != nil {
		return err
	} else if has {
		return refuse("bill_has_history", "bill %s has payment or ledger history and cannot be deleted; void it instead", billLabel(cur))
	}
	n, err := q.DeleteBill(ctx, id)
	if err != nil {
		return err
	}
	if n != 1 {
		// The row was locked above, so its status cannot have changed:
		// nothing deleted means the row is gone another way.
		return errNotFound
	}
	return nil
}

// createBill is Python's create_bill (billing_ap.py:45-81) in tx: a DRAFT
// bill whose balance is its amount. Lines are optional (a bill of 0), and
// no date is defaulted.
func createBill(ctx context.Context, tx pgx.Tx, b BillCreate) (BillOut, error) {
	q := billsdb.New(tx)
	f := b.BillFields
	// Python's str.upper (billing_ap.py:51, :91) can lengthen the value ("ß"
	// is "SS"), which no longer fits the column: a 422 here (Go change 4).
	currency, err := platform.PyUpperAtMost("currency", b.Currency, 3)
	if err != nil {
		return BillOut{}, err
	}
	discountType, discountValue := f.DiscountType.Merge(nil), decOf(f.DiscountValue.Merge(nil))

	amounts := lineAmounts(b.LineItems)
	subtotal, amount := totals(amounts, discountType, discountValue)

	id := uuid.New()
	// The row first, as Python: its own foreign keys are refused before the
	// lines'.
	err = q.InsertBill(ctx, billsdb.InsertBillParams{
		BillID: id, VendorID: b.VendorID, ProjectID: pgUUID(f.ProjectID.Merge(nil)), BillNumber: f.BillNumber.Merge(nil),
		PoNumber: f.PoNumber.Merge(nil), Currency: currency, Subtotal: nullDec(&subtotal), DiscountType: discountType,
		DiscountValue: nullDec(discountValue), Amount: amount, BalanceDue: amount, Status: "DRAFT",
		IssueDate: platform.MergeDate(f.IssueDate, pgtype.Date{}), DueDate: platform.MergeDate(f.DueDate, pgtype.Date{}),
		PaymentTerms: f.PaymentTerms.Merge(nil), Notes: f.Notes.Merge(nil),
	})
	if err != nil {
		return BillOut{}, mapWriteError(err)
	}
	if err := insertLines(ctx, q, id, b.LineItems, amounts); err != nil {
		return BillOut{}, err
	}
	return get(ctx, q, id)
}

// updateBill is Python's update_bill (billing_ap.py:84-115) in tx, the bill
// read FOR UPDATE. Only a DRAFT or OPEN bill is editable (and a PARTIAL one
// sent to PAID). The fields sent overlay the row. Only a list of lines
// replaces the lines and reprices the bill, with the discount as it is after
// the overlay; a discount-only PATCH does not reprice. The balance is
// re-baselined to the new amount while the status after the overlay is
// DRAFT (_apply_totals runs after the fields are set).
//
// Go's own (PORT-NOTES 164, 165), under the row lock: the status sent is a
// transition, not a field. PARTIAL is refused (it comes from a payment).
// DRAFT→OPEN posts the issue entry (openBill) and re-baselines the balance.
// An OPEN bill whose lines, discount, currency or issue date are sent
// re-posts only when its amount, currency, issue day or expense split
// actually changed: it reverses its entry, re-baselines the balance and
// posts again. OPEN→DRAFT reverses the entry. Both are refused while the
// bill has payments not reversed. VOID writes the other fields, then voids
// as POST /void does. PAID writes the other fields (a DRAFT is opened
// first), then records a payment of the whole balance dated CURRENT_DATE
// (payRest). A ledger refusal or a missing rate rolls the whole PATCH back.
func updateBill(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID, b BillUpdate) (BillOut, error) {
	q := billsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return BillOut{}, err
	}
	target := b.Status.Or(cur.Status)
	if cur.Status != "DRAFT" && cur.Status != "OPEN" && (cur.Status != "PARTIAL" || target != "PAID") {
		return BillOut{}, refuse("bill_not_editable", "bill is %s and cannot be edited", cur.Status)
	}
	if target == "PARTIAL" {
		return BillOut{}, refuse("bill_status_not_settable", "%s comes from recording a payment", target)
	}
	// VOID goes through voidBill and PAID through payRest once the other
	// fields are written; PAID opens a DRAFT first.
	status := target
	switch target {
	case "VOID":
		status = cur.Status
	case "PAID":
		status = cur.Status
		if status == "DRAFT" {
			status = "OPEN"
		}
	}
	f := b.BillFields
	reposts := b.LineItems != nil || f.DiscountType.Set || f.DiscountValue.Set || b.Currency.Set || f.IssueDate.Set

	currency := cur.Currency
	if b.Currency.Set {
		if currency, err = platform.PyUpperAtMost("currency", b.Currency.Value, 3); err != nil {
			return BillOut{}, err
		}
	}
	discountType := f.DiscountType.Merge(cur.DiscountType)
	discountValue := cur.DiscountValue
	if f.DiscountValue.Set {
		discountValue = nullDec(decOf(f.DiscountValue.Merge(nil)))
	}
	subtotal, amount, balance := cur.Subtotal, cur.Amount, cur.BalanceDue
	if b.LineItems != nil {
		if err := q.DeleteBillLines(ctx, id); err != nil {
			return BillOut{}, err
		}
		amounts := lineAmounts(*b.LineItems)
		if err := insertLines(ctx, q, id, *b.LineItems, amounts); err != nil {
			return BillOut{}, err
		}
		s, a := totals(amounts, discountType, storedDec(discountValue))
		subtotal, amount = nullDec(&s), a
		if status == "DRAFT" {
			balance = a
		}
	}
	opening := cur.Status == "DRAFT" && status == "OPEN"
	editing := cur.Status != "DRAFT" && status == cur.Status && reposts
	if opening {
		balance = amount
	}

	err = q.UpdateBill(ctx, billsdb.UpdateBillParams{
		BillID: id, VendorID: b.VendorID.Or(cur.VendorID), ProjectID: platform.MergeUUID(f.ProjectID, cur.ProjectID),
		BillNumber: f.BillNumber.Merge(cur.BillNumber), PoNumber: f.PoNumber.Merge(cur.PoNumber), Currency: currency,
		Subtotal: subtotal, DiscountType: discountType, DiscountValue: discountValue, Amount: amount, BalanceDue: balance,
		Status: status, IssueDate: platform.MergeDate(f.IssueDate, cur.IssueDate), DueDate: platform.MergeDate(f.DueDate, cur.DueDate),
		PaymentTerms: f.PaymentTerms.Merge(cur.PaymentTerms), Notes: f.Notes.Merge(cur.Notes),
	})
	if err != nil {
		return BillOut{}, mapWriteError(err)
	}
	if target == "VOID" {
		return voidBill(ctx, tx, id, "status set to VOID via update")
	}
	row, err := q.GetBill(ctx, id)
	if err != nil {
		return BillOut{}, err
	}

	switch {
	case cur.Status == "OPEN" && status == "DRAFT":
		if err := hasActivePayments(ctx, q, id, "cannot return a bill with payments to draft — reverse them first"); err != nil {
			return BillOut{}, err
		}
		if err := unpostBill(ctx, tx, id, "Bill "+billLabel(row)+" returned to draft"); err != nil {
			return BillOut{}, err
		}
	case opening:
		if err := openBill(ctx, tx, base, row); err != nil {
			return BillOut{}, err
		}
	case editing:
		if err := repost(ctx, tx, base, cur, row); err != nil {
			return BillOut{}, err
		}
	}
	if target == "PAID" {
		if err := payRest(ctx, tx, base, id); err != nil {
			return BillOut{}, err
		}
	}
	return get(ctx, q, id)
}

// repost is an edit of a posted bill (cur before it, row after it, both
// locked): when its issue entry would change (postingChanged), the entry is
// reversed, the balance re-baselined to the amount and the bill posted
// again; refused while it has payments not reversed, and for a bill with no
// active entry whose balance is not its amount (paid in part under Python,
// before the back-post).
func repost(ctx context.Context, tx pgx.Tx, base string, cur, row billsdb.Bill) error {
	changed, err := postingChanged(ctx, tx, cur, row)
	if err != nil || !changed {
		return err
	}
	q := billsdb.New(tx)
	if err := hasActivePayments(ctx, q, row.BillID, "cannot re-post a bill with payments — reverse them first"); err != nil {
		return err
	}
	// A bill with no active entry whose balance is not its amount was paid
	// in part under Python, which stored no bill payment: re-baselining
	// the balance would drop that payment. The back-post posts it first.
	_, posted, err := ledger.ActiveEntryFor(ctx, tx, ledger.SourceBill, row.BillID)
	if err != nil {
		return err
	}
	if !posted && !cur.BalanceDue.Equal(cur.Amount) {
		return refuse("bill_not_posted", "bill %s has a balance of %s of its %s but no journal entry — run api backpost-ledger before editing it",
			billLabel(cur), cur.BalanceDue.StringFixed(4), cur.Amount.StringFixed(4))
	}
	if err := unpostBill(ctx, tx, row.BillID, "Edit of bill "+billLabel(row)); err != nil {
		return err
	}
	if err := q.SetBillBalance(ctx, billsdb.SetBillBalanceParams{BillID: row.BillID, BalanceDue: row.Amount, Status: row.Status}); err != nil {
		return err
	}
	return openBill(ctx, tx, base, row)
}

// postingChanged reports whether an edit of a posted bill changes what its
// issue entry would be: the amount, the currency, the issue day (issueDay)
// or the expense split (ExpenseShares against the active entry's shares).
// A bill with no active entry (opened before bills posted) always re-posts.
func postingChanged(ctx context.Context, tx pgx.Tx, cur, row billsdb.Bill) (bool, error) {
	if !cur.Amount.Equal(row.Amount) || cur.Currency != row.Currency || !issueDay(cur).Equal(issueDay(row)) {
		return true, nil
	}
	posted, ok, err := ledger.IssuedShares(ctx, tx, ledger.SourceBill, row.BillID)
	if err != nil || !ok {
		return !ok, err
	}
	shares, err := ExpenseShares(ctx, tx, row)
	if err != nil {
		return false, err
	}
	return !sameShares(posted, shares), nil
}

// sameShares reports whether a and b book the same amount to each account.
func sameShares(a, b []ledger.Share) bool {
	sum := func(ss []ledger.Share) map[int32]decimal.Decimal {
		m := map[int32]decimal.Decimal{}
		for _, s := range ss {
			m[s.AccountID] = m[s.AccountID].Add(s.Amount)
		}
		return m
	}
	ma, mb := sum(a), sum(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if w, ok := mb[k]; !ok || !w.Equal(v) {
			return false
		}
	}
	return true
}

func decOf(d *platform.Dec4) *decimal.Decimal {
	if d == nil {
		return nil
	}
	v := d.Decimal()
	return &v
}

func storedDec(d decimal.NullDecimal) *decimal.Decimal {
	if !d.Valid {
		return nil
	}
	return &d.Decimal
}

func nullDec(d *decimal.Decimal) decimal.NullDecimal {
	if d == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *d, Valid: true}
}
