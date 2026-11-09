// Package quotations ports the Python backend's quoting service
// (app/services/quoting.py) and its routes at /api/v1/quotations: create,
// list, read, partial update and delete, with Q<year><NNNN> numbering, the
// status transitions, and conversion into a DRAFT invoice. The totals are
// invoicing's (invoices.ComputeTotals without withholding), as quoting.py's
// own copy computes them.
package quotations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// ErrQuoting is matched (errors.Is) by every refusal of Python's
// QuotingError: the routes answer it with a 400 whose detail is the error's
// text, Python's sentence.
var ErrQuoting = errors.New("quoting")

type quotingError struct{ code, msg string }

func (e quotingError) Error() string        { return e.msg }
func (e quotingError) Is(target error) bool { return target == ErrQuoting }

// refuse is an ErrQuoting whose text is the formatted sentence and whose
// code (the 400's stable snake_case code) is code.
func refuse(code, format string, args ...any) error {
	return quotingError{code: code, msg: fmt.Sprintf(format, args...)}
}

// clientError is err as the client reads it: an ErrQuoting refusal is a 400
// with Python's sentence, and so is an invoicing refusal a conversion meets
// (Python re-raised InvoicingError as QuotingError, quoting.py:289-294);
// anything else is unchanged.
func clientError(err error) error {
	var e quotingError
	if errors.As(err, &e) {
		return platform.Err(http.StatusBadRequest, e.code, e.msg)
	}
	if errors.Is(err, invoices.ErrInvoicing) {
		return platform.Err(http.StatusBadRequest, invoices.RefusalCode(err), err.Error())
	}
	return err
}

// QuotationLineItemIn is one line of a create or a PATCH
// (schemas/quotation.py:16-21). The description and price are stored as
// sent: nothing is copied from the item.
type QuotationLineItemIn struct {
	ItemID      platform.Optional[uuid.UUID] `json:"item_id" doc:"The catalogue item, if any; it must exist."`
	Description string                       `json:"description"`
	Quantity    platform.Dec4                `json:"quantity" doc:"At least 0, at most 4 decimal places."`
	UnitPrice   platform.Dec4                `json:"unit_price" doc:"At least 0, at most 4 decimal places."`
	Position    platform.NotNull[int32]      `json:"position" doc:"Lines are listed by position. Defaults to 0."`
}

// QuotationFields are the fields a create and a PATCH share. On create a
// field left out or null is no value; on PATCH a field left out keeps the
// stored value and a null clears it.
type QuotationFields struct {
	ProjectID       platform.Optional[uuid.UUID]     `json:"project_id" doc:"It must exist; it is not checked against the customer."`
	QuotationNumber platform.Optional[string]        `json:"quotation_number" maxLength:"50" doc:"Unique. On create, null or empty takes the next Q<year><NNNN> number; on PATCH, null clears it and empty is stored as empty."`
	PoSoNumber      platform.Optional[string]        `json:"po_so_number" maxLength:"100"`
	IssueDate       platform.Optional[platform.Date] `json:"issue_date" doc:"On create, null is today."`
	ValidUntil      platform.Optional[platform.Date] `json:"valid_until" doc:"On create, null is the issue date plus 30 days. Never enforced."`
	PaymentTerms    platform.Optional[string]        `json:"payment_terms" maxLength:"30"`
	Notes           platform.Optional[string]        `json:"notes"`
	Footer          platform.Optional[string]        `json:"footer"`
	DiscountType    platform.Optional[string]        `json:"discount_type" enum:"PERCENT,AMOUNT"`
	DiscountValue   platform.Optional[platform.Dec4] `json:"discount_value" doc:"At least 0, at most 4 decimal places; applies only with a discount_type."`
}

// QuotationCreate adds a DRAFT quotation (schemas/quotation.py:36-51).
type QuotationCreate struct {
	CustomerID uuid.UUID `json:"customer_id" doc:"It must exist."`
	Currency   string    `json:"currency" minLength:"3" maxLength:"3" doc:"Stored as sent."`
	QuotationFields
	// A slice, not NotNull: Huma reaches into a slice's items (each line's
	// optional fields, its decimals' refusals at their line), not into a
	// NotNull's Value. A null is therefore taken as no lines (a 400), where
	// Python answered 422.
	LineItems []QuotationLineItemIn `json:"line_items,omitempty" doc:"At least one."`
}

// QuotationUpdate changes the fields sent of a DRAFT or SENT quotation and
// reprices it (schemas/quotation.py:54-69).
type QuotationUpdate struct {
	CustomerID platform.NotNull[uuid.UUID] `json:"customer_id" doc:"It must exist."`
	Currency   platform.NotNull[string]    `json:"currency" minLength:"3" maxLength:"3" doc:"Stored as sent."`
	QuotationFields
	// A pointer, not Optional, for the reason QuotationCreate.LineItems is a slice.
	LineItems *[]QuotationLineItemIn `json:"line_items" doc:"A list replaces every line (an empty one leaves none); null or left out keeps them."`
}

// negativeSentence is the 422 of Python's ge=0 constraints.
const negativeSentence = "Input should be greater than or equal to 0"

// checkFields is the 422 for the first refused field Huma's validation does
// not check (Dec4 has no bounds of its own): a negative discount value,
// quantity or price.
func checkFields(f QuotationFields, lines []QuotationLineItemIn) error {
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

// QuotationLineItemOut is a stored line, in the field order of Python's
// QuotationLineItemOut (schemas/quotation.py:24-33).
type QuotationLineItemOut struct {
	LineItemID  uuid.UUID     `json:"line_item_id"`
	ItemID      *uuid.UUID    `json:"item_id"`
	Description string        `json:"description"`
	Quantity    platform.Dec4 `json:"quantity"`
	UnitPrice   platform.Dec4 `json:"unit_price"`
	Amount      platform.Dec4 `json:"amount"`
	Position    int           `json:"position"`
}

// QuotationOut is a quotation as the routes answer it, in the field order of
// Python's QuotationOut (schemas/quotation.py:90-114); line_items is [] when
// there are none.
type QuotationOut struct {
	QuotationID        uuid.UUID              `json:"quotation_id"`
	CustomerID         uuid.UUID              `json:"customer_id"`
	ProjectID          *uuid.UUID             `json:"project_id"`
	QuotationNumber    *string                `json:"quotation_number"`
	PoSoNumber         *string                `json:"po_so_number"`
	Currency           string                 `json:"currency"`
	Subtotal           *platform.Dec4         `json:"subtotal"`
	DiscountType       *string                `json:"discount_type"`
	DiscountValue      *platform.Dec4         `json:"discount_value"`
	Amount             platform.Dec4          `json:"amount"`
	Status             string                 `json:"status" enum:"DRAFT,SENT,ACCEPTED,DECLINED,EXPIRED,INVOICED,VOID"`
	IssueDate          *platform.Date         `json:"issue_date"`
	ValidUntil         *platform.Date         `json:"valid_until"`
	PaymentTerms       *string                `json:"payment_terms"`
	Notes              *string                `json:"notes"`
	Footer             *string                `json:"footer"`
	LastSentAt         *platform.Time         `json:"last_sent_at"`
	AcceptedAt         *platform.Time         `json:"accepted_at"`
	AcceptedBy         *string                `json:"accepted_by"`
	ConvertedInvoiceID *uuid.UUID             `json:"converted_invoice_id"`
	CreatedAt          platform.Time          `json:"created_at"`
	LineItems          []QuotationLineItemOut `json:"line_items" nullable:"false"`
}

// get is quotation id with its lines, or pgx.ErrNoRows when there is none.
func get(ctx context.Context, q *quotationsdb.Queries, id uuid.UUID) (QuotationOut, error) {
	row, err := q.GetQuotation(ctx, id)
	if err != nil {
		return QuotationOut{}, err
	}
	lines, err := q.ListQuotationLines(ctx, id)
	if err != nil {
		return QuotationOut{}, err
	}
	return quotationOut(row, lines), nil
}

// list renders a page of quotations, loading all their lines in one query.
func list(ctx context.Context, q *quotationsdb.Queries, rows []quotationsdb.Quotation) ([]QuotationOut, error) {
	out := make([]QuotationOut, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.QuotationID)
	}
	lines, err := q.ListLinesOfQuotations(ctx, ids)
	if err != nil {
		return nil, err
	}
	byQuote := map[uuid.UUID][]quotationsdb.QuotationLineItem{}
	for _, l := range lines {
		byQuote[l.QuotationID] = append(byQuote[l.QuotationID], l)
	}
	for _, r := range rows {
		out = append(out, quotationOut(r, byQuote[r.QuotationID]))
	}
	return out, nil
}

// quotationOut renders a stored quotation and its lines (in position,
// line_item_id order).
func quotationOut(r quotationsdb.Quotation, lines []quotationsdb.QuotationLineItem) QuotationOut {
	out := QuotationOut{
		QuotationID: r.QuotationID, CustomerID: r.CustomerID, ProjectID: platform.UUIDPtr(r.ProjectID),
		QuotationNumber: r.QuotationNumber, PoSoNumber: r.PoSoNumber, Currency: r.Currency,
		Subtotal: dec4Ptr(r.Subtotal), DiscountType: r.DiscountType, DiscountValue: dec4Ptr(r.DiscountValue),
		Amount: platform.NewDec4(r.Amount), Status: r.Status,
		IssueDate: datePtr(r.IssueDate), ValidUntil: datePtr(r.ValidUntil),
		PaymentTerms: r.PaymentTerms, Notes: r.Notes, Footer: r.Footer,
		LastSentAt: timePtr(r.LastSentAt), AcceptedAt: timePtr(r.AcceptedAt), AcceptedBy: r.AcceptedBy,
		ConvertedInvoiceID: platform.UUIDPtr(r.ConvertedInvoiceID), CreatedAt: platform.Time{Time: r.CreatedAt},
		LineItems: make([]QuotationLineItemOut, 0, len(lines)),
	}
	for _, l := range lines {
		out.LineItems = append(out.LineItems, QuotationLineItemOut{
			LineItemID: l.LineItemID, ItemID: platform.UUIDPtr(l.ItemID), Description: l.Description,
			Quantity: platform.NewDec4(l.Quantity), UnitPrice: platform.NewDec4(l.UnitPrice),
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

func timePtr(t *time.Time) *platform.Time {
	if t == nil {
		return nil
	}
	return &platform.Time{Time: *t}
}
