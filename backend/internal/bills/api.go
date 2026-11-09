// Package bills ports the Python backend's AP bill service
// (app/services/billing_ap.py) and its routes at /api/v1/bills: create,
// list, read and partial update. Bills round half-even and sum their rounded
// lines (totals.go), unlike invoices and quotations, so they have their own
// totals. Recording a payment and voiding build on the lock helper in
// store.go.
package bills

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ErrBilling is matched (errors.Is) by every refusal of Python's BillError:
// the routes answer it with a 400 whose detail is the error's text, Python's
// sentence.
var ErrBilling = errors.New("billing")

type billingError struct{ code, msg string }

func (e billingError) Error() string        { return e.msg }
func (e billingError) Is(target error) bool { return target == ErrBilling }

// refuse is an ErrBilling whose text is the formatted sentence and whose
// code (the 400's stable snake_case code) is code.
func refuse(code, format string, args ...any) error {
	return billingError{code: code, msg: fmt.Sprintf(format, args...)}
}

// errNotFound is an unknown bill: a 404 on every bill route.
var errNotFound = errors.New("bill not found")

// ErrNotFound is errNotFound for the MCP bills tools: test a delete or a
// write of an unknown id with errors.Is(err, ErrNotFound).
var ErrNotFound = errNotFound

// clientError is err as the client reads it: a refusal is a 400 with
// Python's sentence, an unknown bill the 404; anything else is unchanged.
func clientError(err error) error {
	var e billingError
	if errors.As(err, &e) {
		return platform.Err(http.StatusBadRequest, e.code, e.msg)
	}
	if errors.Is(err, errNotFound) {
		return notFound()
	}
	return err
}

func notFound() error { return platform.Err(http.StatusNotFound, "bill_not_found", "bill not found") }

// BillLineItemIn is one line of a create or a PATCH (schemas/bill.py:9-15).
// The description and price are stored as sent: nothing is copied from the
// item, and the expense account is not checked for its type.
type BillLineItemIn struct {
	ItemID           platform.Optional[uuid.UUID] `json:"item_id" doc:"The catalogue item, if any; it must exist."`
	ExpenseAccountID platform.Optional[int32]     `json:"expense_account_id" doc:"The chart-of-accounts account, if any; it must exist."`
	Description      string                       `json:"description"`
	Quantity         platform.Dec4                `json:"quantity" doc:"At least 0, at most 4 decimal places."`
	UnitPrice        platform.Dec4                `json:"unit_price" doc:"At least 0, at most 4 decimal places."`
	Position         platform.NotNull[int32]      `json:"position" doc:"Lines are listed by position. Defaults to 0, and 0 is stored as the line's index in the list."`
}

// BillFields are the fields a create and a PATCH share. On create a field
// left out or null is no value; on PATCH a field left out keeps the stored
// value and a null clears it.
type BillFields struct {
	ProjectID     platform.Optional[uuid.UUID]     `json:"project_id" doc:"It must exist."`
	BillNumber    platform.Optional[string]        `json:"bill_number" maxLength:"100" doc:"The vendor's own number; not unique."`
	PoNumber      platform.Optional[string]        `json:"po_number" maxLength:"100"`
	IssueDate     platform.Optional[platform.Date] `json:"issue_date" doc:"Never defaulted."`
	DueDate       platform.Optional[platform.Date] `json:"due_date" doc:"Never defaulted."`
	PaymentTerms  platform.Optional[string]        `json:"payment_terms" maxLength:"30" doc:"Never parsed."`
	Notes         platform.Optional[string]        `json:"notes"`
	DiscountType  platform.Optional[string]        `json:"discount_type" enum:"PERCENT,AMOUNT"`
	DiscountValue platform.Optional[platform.Dec4] `json:"discount_value" doc:"At least 0, at most 4 decimal places; applies only with a discount_type."`
}

// BillCreate adds a DRAFT bill (schemas/bill.py:31-45).
type BillCreate struct {
	VendorID uuid.UUID `json:"vendor_id" doc:"It must exist."`
	Currency string    `json:"currency" minLength:"3" maxLength:"3" doc:"Upper-cased."`
	BillFields
	// A slice, not NotNull: Huma reaches into a slice's items (each line's
	// optional fields, its decimals' refusals at their line), not into a
	// NotNull's Value. A null is therefore taken as no lines (a bill of 0),
	// where Python answered 422.
	LineItems []BillLineItemIn `json:"line_items,omitempty" doc:"Optional; a bill without lines is 0."`
}

// BillUpdate changes the fields sent of a DRAFT or OPEN bill
// (schemas/bill.py:48-63).
type BillUpdate struct {
	VendorID platform.NotNull[uuid.UUID] `json:"vendor_id" doc:"It must exist."`
	Currency platform.NotNull[string]    `json:"currency" minLength:"3" maxLength:"3" doc:"Upper-cased."`
	Status   platform.NotNull[string]    `json:"status" enum:"DRAFT,OPEN,PARTIAL,PAID,VOID" doc:"Any of the five, with no payment or void behind it; the only way to open a bill."`
	BillFields
	// A pointer, not Optional, for the reason BillCreate.LineItems is a slice.
	LineItems *[]BillLineItemIn `json:"line_items" doc:"A list replaces every line and reprices the bill (an empty one leaves none); null or left out keeps the lines and the totals."`
}

// negativeSentence is the 422 of Python's ge=0 constraints.
const negativeSentence = "Input should be greater than or equal to 0"

// checkFields is the 422 for the first refused field Huma's validation does
// not check (Dec4 has no bounds of its own): a negative discount value,
// quantity or price.
func checkFields(f BillFields, lines []BillLineItemIn) error {
	if f.DiscountValue.Set && !f.DiscountValue.Null && f.DiscountValue.Value.Decimal().IsNegative() {
		return platform.FieldError("discount_value", negativeSentence)
	}
	for i, ln := range lines {
		if ln.Quantity.Decimal().IsNegative() {
			return platform.FieldError(fmt.Sprintf("line_items.%d.quantity", i), negativeSentence)
		}
		if ln.UnitPrice.Decimal().IsNegative() {
			return platform.FieldError(fmt.Sprintf("line_items.%d.unit_price", i), negativeSentence)
		}
	}
	return nil
}

// BillLineItemOut is a stored line, in the field order of Python's
// BillLineItemOut (schemas/bill.py:18-28).
type BillLineItemOut struct {
	LineItemID       uuid.UUID     `json:"line_item_id"`
	ItemID           *uuid.UUID    `json:"item_id"`
	ExpenseAccountID *int32        `json:"expense_account_id"`
	Description      string        `json:"description"`
	Quantity         platform.Dec4 `json:"quantity"`
	UnitPrice        platform.Dec4 `json:"unit_price"`
	Amount           platform.Dec4 `json:"amount"`
	Position         int           `json:"position"`
}

// BillOut is a bill as the routes answer it, in the field order of Python's
// BillOut (schemas/bill.py:66-86); line_items is [] when there are none.
type BillOut struct {
	BillID        uuid.UUID         `json:"bill_id"`
	VendorID      uuid.UUID         `json:"vendor_id"`
	ProjectID     *uuid.UUID        `json:"project_id"`
	BillNumber    *string           `json:"bill_number"`
	PoNumber      *string           `json:"po_number"`
	Currency      string            `json:"currency"`
	Subtotal      *platform.Dec4    `json:"subtotal"`
	DiscountType  *string           `json:"discount_type"`
	DiscountValue *platform.Dec4    `json:"discount_value"`
	Amount        platform.Dec4     `json:"amount"`
	BalanceDue    platform.Dec4     `json:"balance_due"`
	Status        string            `json:"status" enum:"DRAFT,OPEN,PARTIAL,PAID,VOID"`
	IssueDate     *platform.Date    `json:"issue_date"`
	DueDate       *platform.Date    `json:"due_date"`
	PaymentTerms  *string           `json:"payment_terms"`
	Notes         *string           `json:"notes"`
	CreatedAt     platform.Time     `json:"created_at"`
	LineItems     []BillLineItemOut `json:"line_items" nullable:"false"`
}

// Get is bill id with its lines, or pgx.ErrNoRows when there is none.
func get(ctx context.Context, q *billsdb.Queries, id uuid.UUID) (BillOut, error) {
	row, err := q.GetBill(ctx, id)
	if err != nil {
		return BillOut{}, err
	}
	lines, err := q.ListBillLines(ctx, id)
	if err != nil {
		return BillOut{}, err
	}
	return billOut(row, lines), nil
}

// Get is the bill id with its lines, exported for the MCP read tools
// (inventory §4). An unknown id is pgx.ErrNoRows.
func Get(ctx context.Context, q *billsdb.Queries, id uuid.UUID) (BillOut, error) {
	return get(ctx, q, id)
}

// ListFilter narrows List; the zero value lists every bill. Statuses and
// VendorID filter when set (an empty Statuses is no filter, as in Python);
// Limit caps the page when set (nil is unlimited, 0 lists nothing).
type ListFilter struct {
	Statuses []string
	VendorID *uuid.UUID
	Limit    *int32
}

// List is the bills matching f, rendered as the routes render them, newest
// first with the id breaking ties, never nil.
func List(ctx context.Context, q *billsdb.Queries, f ListFilter) ([]BillOut, error) {
	var statuses []string
	if len(f.Statuses) > 0 {
		statuses = f.Statuses
	}
	rows, err := q.ListBills(ctx, billsdb.ListBillsParams{
		Statuses: statuses, VendorID: pgUUID(f.VendorID), RowLimit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	return list(ctx, q, rows)
}

// list renders bills, loading all their lines in one query.
func list(ctx context.Context, q *billsdb.Queries, rows []billsdb.Bill) ([]BillOut, error) {
	out := make([]BillOut, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.BillID)
	}
	lines, err := q.ListLinesOfBills(ctx, ids)
	if err != nil {
		return nil, err
	}
	byBill := map[uuid.UUID][]billsdb.BillLineItem{}
	for _, l := range lines {
		byBill[l.BillID] = append(byBill[l.BillID], l)
	}
	for _, r := range rows {
		out = append(out, billOut(r, byBill[r.BillID]))
	}
	return out, nil
}

// billOut renders a stored bill and its lines (in position, line_item_id
// order).
func billOut(r billsdb.Bill, lines []billsdb.BillLineItem) BillOut {
	out := BillOut{
		BillID: r.BillID, VendorID: r.VendorID, ProjectID: platform.UUIDPtr(r.ProjectID), BillNumber: r.BillNumber,
		PoNumber: r.PoNumber, Currency: r.Currency, Subtotal: dec4Ptr(r.Subtotal), DiscountType: r.DiscountType,
		DiscountValue: dec4Ptr(r.DiscountValue), Amount: platform.NewDec4(r.Amount), BalanceDue: platform.NewDec4(r.BalanceDue),
		Status: r.Status, IssueDate: datePtr(r.IssueDate), DueDate: datePtr(r.DueDate), PaymentTerms: r.PaymentTerms,
		Notes: r.Notes, CreatedAt: platform.Time{Time: r.CreatedAt},
		LineItems: make([]BillLineItemOut, 0, len(lines)),
	}
	for _, l := range lines {
		out.LineItems = append(out.LineItems, BillLineItemOut{
			LineItemID: l.LineItemID, ItemID: platform.UUIDPtr(l.ItemID), ExpenseAccountID: l.ExpenseAccountID,
			Description: l.Description, Quantity: platform.NewDec4(l.Quantity), UnitPrice: platform.NewDec4(l.UnitPrice),
			Amount: platform.NewDec4(l.Amount), Position: int(l.Position),
		})
	}
	return out
}

func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func dec4Ptr(d decimal.NullDecimal) *platform.Dec4 {
	if !d.Valid {
		return nil
	}
	v := platform.NewDec4(d.Decimal)
	return &v
}

func datePtr(d pgtype.Date) *platform.Date {
	if !d.Valid {
		return nil
	}
	return &platform.Date{Time: d.Time}
}
