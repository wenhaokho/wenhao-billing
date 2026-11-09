package bills

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
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
		IssueDate: mergeDate(f.IssueDate, pgtype.Date{}), DueDate: mergeDate(f.DueDate, pgtype.Date{}),
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
// read FOR UPDATE. Only a DRAFT or OPEN bill is editable. The fields sent
// overlay the row, status included (any of the five). Only a list of lines
// replaces the lines and reprices the bill, with the discount as it is after
// the overlay; a discount-only PATCH does not reprice. The balance is
// re-baselined to the new amount only while the status after the overlay is
// DRAFT: _apply_totals runs after the fields are set.
func updateBill(ctx context.Context, tx pgx.Tx, id uuid.UUID, b BillUpdate) (BillOut, error) {
	q := billsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return BillOut{}, err
	}
	if cur.Status != "DRAFT" && cur.Status != "OPEN" {
		return BillOut{}, refuse("bill_not_editable", "bill is %s and cannot be edited", cur.Status)
	}

	f := b.BillFields
	currency := cur.Currency
	if b.Currency.Set {
		if currency, err = platform.PyUpperAtMost("currency", b.Currency.Value, 3); err != nil {
			return BillOut{}, err
		}
	}
	status := b.Status.Or(cur.Status)
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

	err = q.UpdateBill(ctx, billsdb.UpdateBillParams{
		BillID: id, VendorID: b.VendorID.Or(cur.VendorID), ProjectID: mergeUUID(f.ProjectID, cur.ProjectID),
		BillNumber: f.BillNumber.Merge(cur.BillNumber), PoNumber: f.PoNumber.Merge(cur.PoNumber), Currency: currency,
		Subtotal: subtotal, DiscountType: discountType, DiscountValue: discountValue, Amount: amount, BalanceDue: balance,
		Status: status, IssueDate: mergeDate(f.IssueDate, cur.IssueDate), DueDate: mergeDate(f.DueDate, cur.DueDate),
		PaymentTerms: f.PaymentTerms.Merge(cur.PaymentTerms), Notes: f.Notes.Merge(cur.Notes),
	})
	if err != nil {
		return BillOut{}, mapWriteError(err)
	}
	return get(ctx, q, id)
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

func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func mergeUUID(f platform.Optional[uuid.UUID], cur pgtype.UUID) pgtype.UUID {
	if !f.Set {
		return cur
	}
	return pgUUID(f.Merge(nil))
}

func mergeDate(f platform.Optional[platform.Date], cur pgtype.Date) pgtype.Date {
	if !f.Set {
		return cur
	}
	if f.Null {
		return pgtype.Date{}
	}
	return pgDate(f.Value.Time)
}
