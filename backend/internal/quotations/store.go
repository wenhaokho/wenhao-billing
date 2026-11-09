package quotations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// foreignKeys are the references a client names on the quotation row, as
// the 422 at the field each becomes (Python answered 500).
var foreignKeys = map[string]struct{ field, sentence string }{
	"quotations_customer_id_fkey": {"customer_id", "customer not found"},
	"quotations_project_id_fkey":  {"project_id", "project not found"},
}

// mapWriteError is a failed write of a quotation row as the client error it
// is: an unknown customer or project is the 422 at its field, and a taken
// number (quotations_quotation_number_key) the 409 quotation_number_exists;
// Python answered 500 for both. Any other error comes back unchanged.
func mapWriteError(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	if fk, ok := foreignKeys[pg.ConstraintName]; ok {
		return platform.FieldError(fk.field, fk.sentence)
	}
	if pg.ConstraintName == "quotations_quotation_number_key" {
		return platform.Err(http.StatusConflict, "quotation_number_exists", "quotation number already exists")
	}
	return err
}

// lock reads quotation id FOR UPDATE; an unknown one is Python's refusal (a
// 400 on every write route).
func lock(ctx context.Context, q *quotationsdb.Queries, id uuid.UUID) (quotationsdb.Quotation, error) {
	row, err := q.GetQuotationForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return quotationsdb.Quotation{}, refuse("quotation_not_found", "quotation %s not found", id)
	}
	return row, err
}

// insertLines stores the lines of quotation id, each amount qty × price
// rounded half-up, each position as sent. An unknown item is the 422 at its
// line.
func insertLines(ctx context.Context, q *quotationsdb.Queries, id uuid.UUID, lines []QuotationLineItemIn) error {
	for i, ln := range lines {
		qty, price := ln.Quantity.Decimal(), ln.UnitPrice.Decimal()
		err := q.InsertQuotationLine(ctx, quotationsdb.InsertQuotationLineParams{
			LineItemID: uuid.New(), QuotationID: id, ItemID: pgUUID(ln.ItemID.Merge(nil)), Position: ln.Position.Value,
			Description: ln.Description, Quantity: qty, UnitPrice: price, Amount: invoices.LineAmount(qty, price),
		})
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.ConstraintName == "quotation_line_items_item_id_fkey" {
			return platform.FieldError(fmt.Sprintf("line_items.%d.item_id", i), "item not found")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// totals are quoting.py's _compute_totals (SQ:50-70): invoicing's without
// withholding (NONE leaves the discounted amount as it is).
func totals(lines []invoices.LineIn, discountType *string, discountValue *decimal.Decimal, currency string) invoices.Totals {
	return invoices.ComputeTotals(lines, discountType, discountValue, currency, "NONE", nil, false)
}

// validityDays is how long a quotation is valid when no valid_until is sent.
const validityDays = 30

// lastDate is Python's date.max.
var lastDate = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// createQuotation is Python's create_quotation (quoting.py:110-142) in tx:
// the number comes from NextQuotationNumber's lock, so the insert must be in
// the same tx.
func createQuotation(ctx context.Context, tx pgx.Tx, b QuotationCreate, today time.Time) (QuotationOut, error) {
	q := quotationsdb.New(tx)
	f := b.QuotationFields
	if len(b.LineItems) == 0 {
		return QuotationOut{}, refuse("line_items_required", "quotation must have at least one line item")
	}
	lines := make([]invoices.LineIn, 0, len(b.LineItems))
	for _, ln := range b.LineItems {
		lines = append(lines, invoices.LineIn{Quantity: ln.Quantity.Decimal(), UnitPrice: ln.UnitPrice.Decimal()})
	}
	discountValue := decOf(f.DiscountValue.Merge(nil))
	t := totals(lines, f.DiscountType.Merge(nil), discountValue, b.Currency)

	issue := today
	if v := f.IssueDate.Merge(nil); v != nil {
		issue = v.Time
	}
	var valid time.Time
	if v := f.ValidUntil.Merge(nil); v != nil {
		valid = v.Time
	} else {
		// Python's date arithmetic raised past date.max (a 500).
		if issue.After(lastDate.AddDate(0, 0, -validityDays)) {
			return QuotationOut{}, refuse("valid_until_out_of_range", "valid until is out of range")
		}
		valid = issue.AddDate(0, 0, validityDays)
	}
	number := f.QuotationNumber.Merge(nil)
	if number == nil || *number == "" {
		n, err := NextQuotationNumber(ctx, tx, today)
		if err != nil {
			return QuotationOut{}, err
		}
		number = &n
	}

	id := uuid.New()
	err := q.InsertQuotation(ctx, quotationsdb.InsertQuotationParams{
		QuotationID: id, CustomerID: b.CustomerID, ProjectID: pgUUID(f.ProjectID.Merge(nil)),
		QuotationNumber: number, PoSoNumber: f.PoSoNumber.Merge(nil), Currency: b.Currency,
		Subtotal: nullDec(&t.Subtotal), DiscountType: f.DiscountType.Merge(nil), DiscountValue: nullDec(discountValue),
		Amount: t.Amount, Status: "DRAFT", IssueDate: pgDate(issue), ValidUntil: pgDate(valid),
		PaymentTerms: f.PaymentTerms.Merge(nil), Notes: f.Notes.Merge(nil), Footer: f.Footer.Merge(nil),
	})
	if err != nil {
		return QuotationOut{}, mapWriteError(err)
	}
	if err := insertLines(ctx, q, id, b.LineItems); err != nil {
		return QuotationOut{}, err
	}
	return get(ctx, q, id)
}

// updateQuotation is Python's update_quotation (quoting.py:145-202) in tx:
// the fields sent overlay the row (read FOR UPDATE), the lines are replaced
// when line_items is a list (an empty one included), and the totals are
// recomputed from the stored lines and discount whatever changed, so a
// discount-only PATCH reprices. Nothing is re-defaulted: a null number,
// issue date or valid_until stays null.
func updateQuotation(ctx context.Context, tx pgx.Tx, id uuid.UUID, b QuotationUpdate) (QuotationOut, error) {
	q := quotationsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return QuotationOut{}, err
	}
	if cur.Status != "DRAFT" && cur.Status != "SENT" {
		return QuotationOut{}, refuse("quotation_not_editable", "only DRAFT or SENT quotations can be edited (got %s)", cur.Status)
	}

	f := b.QuotationFields
	if b.LineItems != nil {
		if err := q.DeleteQuotationLines(ctx, id); err != nil {
			return QuotationOut{}, err
		}
		if err := insertLines(ctx, q, id, *b.LineItems); err != nil {
			return QuotationOut{}, err
		}
	}
	stored, err := q.ListQuotationLines(ctx, id)
	if err != nil {
		return QuotationOut{}, err
	}
	lines := make([]invoices.LineIn, 0, len(stored))
	for _, l := range stored {
		lines = append(lines, invoices.LineIn{Quantity: l.Quantity, UnitPrice: l.UnitPrice})
	}
	discountType := f.DiscountType.Merge(cur.DiscountType)
	discountValue := cur.DiscountValue
	if f.DiscountValue.Set {
		discountValue = nullDec(decOf(f.DiscountValue.Merge(nil)))
	}
	currency := b.Currency.Or(cur.Currency)
	t := totals(lines, discountType, storedDec(discountValue), currency)

	err = q.UpdateQuotation(ctx, quotationsdb.UpdateQuotationParams{
		QuotationID: id, CustomerID: b.CustomerID.Or(cur.CustomerID), ProjectID: mergeUUID(f.ProjectID, cur.ProjectID),
		QuotationNumber: f.QuotationNumber.Merge(cur.QuotationNumber), PoSoNumber: f.PoSoNumber.Merge(cur.PoSoNumber),
		Currency: currency, Subtotal: nullDec(&t.Subtotal), DiscountType: discountType, DiscountValue: discountValue,
		Amount: t.Amount, IssueDate: mergeDate(f.IssueDate, cur.IssueDate), ValidUntil: mergeDate(f.ValidUntil, cur.ValidUntil),
		PaymentTerms: f.PaymentTerms.Merge(cur.PaymentTerms), Notes: f.Notes.Merge(cur.Notes), Footer: f.Footer.Merge(cur.Footer),
	})
	if err != nil {
		return QuotationOut{}, mapWriteError(err)
	}
	return get(ctx, q, id)
}

// deleteQuotation is Python's delete_quotation (quoting.py:254-263): only a
// DRAFT goes, and its lines with it.
func deleteQuotation(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := quotationsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return err
	}
	if cur.Status != "DRAFT" {
		return refuse("quotation_not_deletable", "only DRAFT quotations can be deleted (got %s)", cur.Status)
	}
	return q.DeleteQuotation(ctx, id)
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
