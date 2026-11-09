package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// LineItemIn is one line of a create or a PATCH (schemas/invoice.py:37-42).
// The description and price are stored as sent: nothing is copied from the
// item.
type LineItemIn struct {
	ItemID      platform.Optional[uuid.UUID] `json:"item_id" doc:"The catalogue item, if any; it must exist."`
	Description string                       `json:"description"`
	Quantity    platform.Dec4                `json:"quantity" doc:"At least 0, at most 4 decimal places."`
	UnitPrice   platform.Dec4                `json:"unit_price" doc:"At least 0, at most 4 decimal places."`
	Position    platform.NotNull[int32]      `json:"position" doc:"Lines are listed by position. Defaults to 0."`
}

// InvoiceFields are the fields a create and a PATCH share. On create a
// field left out or null is no value; on PATCH a field left out keeps the
// stored value and a null clears it.
type InvoiceFields struct {
	CustomerID    platform.Optional[uuid.UUID]     `json:"customer_id" doc:"It must exist."`
	ProjectID     platform.Optional[uuid.UUID]     `json:"project_id" doc:"It must exist."`
	InvoiceNumber platform.Optional[string]        `json:"invoice_number" maxLength:"50" doc:"Unique. On create, null or empty takes the next WH<year><NNNN> number."`
	PoSoNumber    platform.Optional[string]        `json:"po_so_number" maxLength:"100"`
	IssueDate     platform.Optional[platform.Date] `json:"issue_date" doc:"On create, null is today."`
	DueDate       platform.Optional[platform.Date] `json:"due_date" doc:"Null is the issue date plus the payment terms' days (Net N: N, on receipt: 0, otherwise 14)."`
	PaymentTerms  platform.Optional[string]        `json:"payment_terms" maxLength:"30"`
	Notes         platform.Optional[string]        `json:"notes"`
	Footer        platform.Optional[string]        `json:"footer"`
	InternalNote  platform.Optional[string]        `json:"internal_note" doc:"For the team only; never on the customer's PDF."`
	DiscountType  platform.Optional[string]        `json:"discount_type" enum:"PERCENT,AMOUNT"`
	DiscountValue platform.Optional[platform.Dec4] `json:"discount_value" doc:"At least 0, at most 4 decimal places; applies only with a discount_type."`
	WhtType       platform.Optional[string]        `json:"wht_type" enum:"NONE,PPH21,PPH23" doc:"On create, null takes the customer's default."`
	WhtRate       platform.Optional[platform.Dec4] `json:"wht_rate" doc:"Percent, above 0 and below 100. Null takes the type's default (PPH21 2.5, PPH23 2)."`
	WhtGrossUp    platform.Optional[bool]          `json:"wht_gross_up"`
}

// InvoiceCreate adds a DRAFT invoice (schemas/invoice.py:57-75).
type InvoiceCreate struct {
	InvoiceType platform.NotNull[string] `json:"invoice_type" maxLength:"20" doc:"Defaults to MILESTONE; empty is MILESTONE too."`
	Currency    string                   `json:"currency" minLength:"3" maxLength:"3"`
	InvoiceFields
	// A slice, not NotNull: Huma reaches into a slice's items (each line's optional
	// fields, its decimals' refusals at their line), not into a NotNull's Value.
	// A null is therefore taken as no lines (a 400), where Python answered 422.
	LineItems []LineItemIn `json:"line_items,omitempty" doc:"At least one."`
}

// InvoiceUpdate changes the fields sent of a DRAFT invoice and recomputes
// its totals (schemas/invoice.py:78-96).
type InvoiceUpdate struct {
	InvoiceType platform.NotNull[string] `json:"invoice_type" maxLength:"20"`
	Currency    platform.NotNull[string] `json:"currency" minLength:"3" maxLength:"3"`
	InvoiceFields
	// A pointer, not Optional, for the reason InvoiceCreate.LineItems is a slice.
	LineItems *[]LineItemIn `json:"line_items" doc:"A list replaces every line (an empty one leaves none); null or left out keeps them."`
}

// MilestoneInvoiceCreate adds a DRAFT milestone invoice with no lines and
// no number (schemas/invoice.py:12-19).
type MilestoneInvoiceCreate struct {
	CustomerID   uuid.UUID                        `json:"customer_id" doc:"It must exist."`
	ProjectID    platform.Optional[uuid.UUID]     `json:"project_id" doc:"It must exist."`
	Currency     string                           `json:"currency" minLength:"3" maxLength:"3"`
	Amount       platform.Dec4                    `json:"amount" doc:"Above 0, at most 4 decimal places."`
	MilestoneRef string                           `json:"milestone_ref"`
	IssueDate    platform.Optional[platform.Date] `json:"issue_date" doc:"Null is today."`
	DueInDays    platform.NotNull[int]            `json:"due_in_days" doc:"Days from the issue date to the due date; may be negative. Defaults to 14."`
}

// The 422 sentences of Python's field constraints.
const (
	negativeSentence    = "Input should be greater than or equal to 0"
	notPositiveSentence = "Input should be greater than 0"
)

// checkFields is the 422 for the first refused field Huma's validation does
// not check (Dec4 has no bounds of its own): a negative quantity, price or
// discount value.
func checkFields(f InvoiceFields, lines []LineItemIn) error {
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

// foreignKeys are the references a client names, as the 422 at the field
// each becomes (Python answered 500). The line items' is located by line.
var foreignKeys = map[string]struct{ field, sentence string }{
	"invoices_customer_id_fkey": {"customer_id", "customer not found"},
	"invoices_project_id_fkey":  {"project_id", "project not found"},
}

// MapWriteError is a failed write to the invoices table as the client error
// it is: an unknown customer or project (the invoices_*_fkey constraints) is
// the 422 at customer_id or project_id, and a taken invoice number
// (uq_invoices_invoice_number) is the 409 invoice_number_exists; Python
// answered 500 for both (port note 36). Any other error, Postgres or not,
// comes back unchanged. Every write of an invoice row passes its error here,
// including those of other packages (the quotations plan converts a quote
// into an invoice). A line item's foreign key is not an invoice-row error:
// it is mapped where the lines are inserted, located by line.
func MapWriteError(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	if fk, ok := foreignKeys[pg.ConstraintName]; ok {
		return platform.FieldError(fk.field, fk.sentence)
	}
	if pg.ConstraintName == "uq_invoices_invoice_number" {
		return platform.Err(http.StatusConflict, "invoice_number_exists", "invoice number already exists")
	}
	return err
}

// insertLines stores the lines of invoice id, each amount qty × price
// rounded half-up. An unknown item is the 422 at its line.
func insertLines(ctx context.Context, q *invoicesdb.Queries, id uuid.UUID, lines []LineItemIn) error {
	for i, ln := range lines {
		in := lineIn(ln)
		err := q.InsertInvoiceLine(ctx, invoicesdb.InsertInvoiceLineParams{
			LineItemID: uuid.New(), InvoiceID: id, ItemID: pgUUID(in.ItemID), Position: ln.Position.Value,
			Description: in.Description, Quantity: in.Quantity, UnitPrice: in.UnitPrice,
			Amount: LineAmount(in.Quantity, in.UnitPrice),
		})
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.ConstraintName == "invoice_line_items_item_id_fkey" {
			return platform.FieldError(fmt.Sprintf("line_items.%d.item_id", i), "item not found")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// lineIn is a request line as ComputeTotals takes it; Position is the one
// sent, 0 when left out (Python's default; its index fallback never fires).
func lineIn(ln LineItemIn) LineIn {
	return LineIn{
		ItemID: ln.ItemID.Merge(nil), Description: ln.Description,
		Quantity: ln.Quantity.Decimal(), UnitPrice: ln.UnitPrice.Decimal(), Position: int(ln.Position.Value),
	}
}

// createInvoice is Python's create_invoice (invoicing.py:228-280) in tx:
// the number comes from NextInvoiceNumber's lock, so the insert must be in
// the same tx.
func createInvoice(ctx context.Context, tx pgx.Tx, b InvoiceCreate, today time.Time) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	f := b.InvoiceFields
	if len(b.LineItems) == 0 {
		return InvoiceOut{}, refuse("line_items_required", "invoice must have at least one line item")
	}
	whtType, whtRate, grossUp, err := ResolveWHT(ctx, q, f.CustomerID.Merge(nil), f.WhtType.Merge(nil),
		decOf(f.WhtRate.Merge(nil)), f.WhtGrossUp.Merge(nil))
	if err != nil {
		return InvoiceOut{}, err
	}
	lines := make([]LineIn, 0, len(b.LineItems))
	for _, ln := range b.LineItems {
		lines = append(lines, lineIn(ln))
	}
	discountValue := decOf(f.DiscountValue.Merge(nil))
	totals := ComputeTotals(lines, f.DiscountType.Merge(nil), discountValue, b.Currency, whtType, whtRate, grossUp)

	number := f.InvoiceNumber.Merge(nil)
	if number == nil || *number == "" {
		n, err := NextInvoiceNumber(ctx, tx, today)
		if err != nil {
			return InvoiceOut{}, err
		}
		number = &n
	}
	issue := today
	if f.IssueDate.Set && !f.IssueDate.Null {
		issue = f.IssueDate.Value.Time
	}
	due, err := dueOr(f.DueDate.Merge(nil), issue, f.PaymentTerms.Merge(nil))
	if err != nil {
		return InvoiceOut{}, err
	}
	invoiceType := b.InvoiceType.Value
	if invoiceType == "" {
		invoiceType = "MILESTONE"
	}

	id := uuid.New()
	err = q.InsertInvoice(ctx, invoicesdb.InsertInvoiceParams{
		InvoiceID: id, CustomerID: pgUUID(f.CustomerID.Merge(nil)), ProjectID: pgUUID(f.ProjectID.Merge(nil)),
		InvoiceType: invoiceType, InvoiceNumber: number, PoSoNumber: f.PoSoNumber.Merge(nil), Currency: b.Currency,
		Subtotal: nullDec(&totals.Subtotal), DiscountType: f.DiscountType.Merge(nil), DiscountValue: nullDec(discountValue),
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&totals.WHT.Base),
		WhtAmount: totals.WHT.Amount, Amount: totals.Amount, BalanceDue: totals.Amount, Status: "DRAFT",
		IssueDate: pgDate(&issue), DueDate: pgDate(&due), PaymentTerms: f.PaymentTerms.Merge(nil),
		Notes: f.Notes.Merge(nil), Footer: f.Footer.Merge(nil), InternalNote: f.InternalNote.Merge(nil),
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}
	if err := insertLines(ctx, q, id, b.LineItems); err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

// dueOr is the due date sent, or the issue date plus the terms' days.
func dueOr(sent *platform.Date, issue time.Time, terms *string) (time.Time, error) {
	if sent != nil {
		return sent.Time, nil
	}
	return DueAfterTerms(issue, terms)
}

// updateInvoice is Python's update_invoice (invoicing.py:283-341) in tx:
// the fields sent overlay the row (read FOR UPDATE), the lines are replaced
// when line_items is a list, and the withholding and totals are recomputed
// from the stored lines. A due date is filled from the terms only when it
// is null and there is an issue date; a new issue date never moves it.
func updateInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID, b InvoiceUpdate) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	// The template's lock first, for every invoice (the key is namespaced, so
	// it costs an ordinary invoice nothing): this route edits a template too
	// (port note 43), and a trigger must see that edit whole. Lock order: the
	// template lock, the invoice row, the number lock (see Trigger).
	if err := q.LockRecurringTemplate(ctx, id.String()); err != nil {
		return InvoiceOut{}, err
	}
	inv, err := q.GetInvoiceForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceOut{}, refuse("invoice_not_found", "invoice %s not found", id)
	}
	if err != nil {
		return InvoiceOut{}, err
	}
	if inv.Status != "DRAFT" {
		return InvoiceOut{}, refuse("invoice_not_draft", "only DRAFT invoices can be edited (got %s)", inv.Status)
	}

	f := b.InvoiceFields
	inv.CustomerID = platform.MergeUUID(f.CustomerID, inv.CustomerID)
	inv.ProjectID = platform.MergeUUID(f.ProjectID, inv.ProjectID)
	inv.InvoiceType = b.InvoiceType.Or(inv.InvoiceType)
	inv.Currency = b.Currency.Or(inv.Currency)
	inv.InvoiceNumber = f.InvoiceNumber.Merge(inv.InvoiceNumber)
	inv.PoSoNumber = f.PoSoNumber.Merge(inv.PoSoNumber)
	inv.IssueDate = platform.MergeDate(f.IssueDate, inv.IssueDate)
	inv.DueDate = platform.MergeDate(f.DueDate, inv.DueDate)
	inv.PaymentTerms = f.PaymentTerms.Merge(inv.PaymentTerms)
	inv.Notes = f.Notes.Merge(inv.Notes)
	inv.Footer = f.Footer.Merge(inv.Footer)
	inv.InternalNote = f.InternalNote.Merge(inv.InternalNote)
	inv.DiscountType = f.DiscountType.Merge(inv.DiscountType)
	if f.DiscountValue.Set {
		inv.DiscountValue = nullDec(decOf(f.DiscountValue.Merge(nil)))
	}

	if b.LineItems != nil {
		if err := q.DeleteInvoiceLines(ctx, id); err != nil {
			return InvoiceOut{}, err
		}
		if err := insertLines(ctx, q, id, *b.LineItems); err != nil {
			return InvoiceOut{}, err
		}
	}

	whtType, whtRate, grossUp, err := MergeWHT(inv.WhtType, storedDec(inv.WhtRate), inv.WhtGrossUp, f.WhtType, f.WhtRate, f.WhtGrossUp)
	if err != nil {
		return InvoiceOut{}, err
	}
	stored, err := q.ListInvoiceLines(ctx, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	lines := make([]LineIn, 0, len(stored))
	for _, l := range stored {
		lines = append(lines, LineIn{Quantity: l.Quantity, UnitPrice: l.UnitPrice})
	}
	totals := ComputeTotals(lines, inv.DiscountType, storedDec(inv.DiscountValue), inv.Currency, whtType, whtRate, grossUp)

	if !inv.DueDate.Valid && inv.IssueDate.Valid {
		due, err := DueAfterTerms(inv.IssueDate.Time, inv.PaymentTerms)
		if err != nil {
			return InvoiceOut{}, err
		}
		inv.DueDate = pgDate(&due)
	}

	err = q.UpdateInvoice(ctx, invoicesdb.UpdateInvoiceParams{
		InvoiceID: id, CustomerID: inv.CustomerID, ProjectID: inv.ProjectID, InvoiceType: inv.InvoiceType,
		InvoiceNumber: inv.InvoiceNumber, PoSoNumber: inv.PoSoNumber, Currency: inv.Currency,
		Subtotal: nullDec(&totals.Subtotal), DiscountType: inv.DiscountType, DiscountValue: inv.DiscountValue,
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&totals.WHT.Base),
		WhtAmount: totals.WHT.Amount, Amount: totals.Amount, BalanceDue: totals.Amount,
		IssueDate: inv.IssueDate, DueDate: inv.DueDate, PaymentTerms: inv.PaymentTerms,
		Notes: inv.Notes, Footer: inv.Footer, InternalNote: inv.InternalNote,
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}
	return Get(ctx, q, id)
}

// createMilestone is Python's create_milestone_invoice (invoicing.py:192-225):
// the customer's default withholding on the amount, no discount, no number,
// no lines, and the reference kept in billing_cycle_ref.
func createMilestone(ctx context.Context, tx pgx.Tx, b MilestoneInvoiceCreate, today time.Time) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	whtType, whtRate, grossUp, err := ResolveWHT(ctx, q, &b.CustomerID, nil, nil, nil)
	if err != nil {
		return InvoiceOut{}, err
	}
	issue := today
	if b.IssueDate.Set && !b.IssueDate.Null {
		issue = b.IssueDate.Value.Time
	}
	due, err := AddDays(issue, b.DueInDays.Or(14))
	if err != nil {
		return InvoiceOut{}, err
	}
	ref, err := json.Marshal(map[string]string{"milestone_ref": b.MilestoneRef})
	if err != nil {
		return InvoiceOut{}, err
	}
	wht := withholding.Compute(b.Amount.Decimal(), b.Currency, whtType, whtRate, grossUp)
	id := uuid.New()
	err = q.InsertInvoice(ctx, invoicesdb.InsertInvoiceParams{
		InvoiceID: id, CustomerID: pgUUID(&b.CustomerID), ProjectID: pgUUID(b.ProjectID.Merge(nil)),
		InvoiceType: "MILESTONE", Currency: b.Currency,
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&wht.Base),
		WhtAmount: wht.Amount, Amount: wht.Total, BalanceDue: wht.Total, Status: "DRAFT",
		BillingCycleRef: ref, IssueDate: pgDate(&issue), DueDate: pgDate(&due),
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}
	return Get(ctx, q, id)
}

// list renders a page of invoices, loading all their lines in one query.
func list(ctx context.Context, q *invoicesdb.Queries, invs []invoicesdb.Invoice) ([]InvoiceOut, error) {
	out := make([]InvoiceOut, 0, len(invs))
	if len(invs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(invs))
	for _, inv := range invs {
		ids = append(ids, inv.InvoiceID)
	}
	rows, err := q.ListLinesOfInvoices(ctx, ids)
	if err != nil {
		return nil, err
	}
	byInvoice := map[uuid.UUID][]invoicesdb.InvoiceLineItem{}
	for _, l := range rows {
		byInvoice[l.InvoiceID] = append(byInvoice[l.InvoiceID], l)
	}
	for _, inv := range invs {
		o, err := invoiceOut(inv, byInvoice[inv.InvoiceID])
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// utcDate is t's UTC calendar date, at midnight UTC.
func utcDate(t time.Time) time.Time {
	n := t.UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
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

func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func pgDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}
