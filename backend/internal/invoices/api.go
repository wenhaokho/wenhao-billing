// Package invoices ports the Python backend's invoicing service
// (app/services/invoicing.py) and its routes: totals, withholding, payment
// terms, numbering and the InvoiceOut every route answers. The helpers are
// exported for the quotations port, which reuses them as quoting.py does.
package invoices

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ErrInvoicing is matched (errors.Is) by every refusal of Python's
// InvoicingError: the routes answer it with a 400 whose detail is the
// error's text, Python's sentence. Withholding normalisation errors are
// refusals too (invoicing.py:76-82), with withholding's sentence.
var ErrInvoicing = errors.New("invoicing")

type invoicingError struct{ code, msg string }

func (e invoicingError) Error() string        { return e.msg }
func (e invoicingError) Is(target error) bool { return target == ErrInvoicing }

// refuse is an ErrInvoicing whose text is the formatted sentence and whose
// code (the 400's stable snake_case code) is code.
func refuse(code, format string, args ...any) error {
	return invoicingError{code: code, msg: fmt.Sprintf(format, args...)}
}

// RefusalCode is the code of an ErrInvoicing (the 400's stable snake_case
// code), "" for any other error. Other packages that call into invoicing
// (a quotation conversion) answer its refusals with it.
func RefusalCode(err error) string {
	var e invoicingError
	if errors.As(err, &e) {
		return e.code
	}
	return ""
}

// LineIn is one invoice line as a request gives it.
type LineIn struct {
	ItemID              *uuid.UUID
	Description         string
	Quantity, UnitPrice decimal.Decimal
	Position            int
}

// LineItemOut is a stored invoice line, in the field order of Python's
// InvoiceLineItemOut (schemas/invoice.py:45-54).
type LineItemOut struct {
	LineItemID  uuid.UUID     `json:"line_item_id"`
	ItemID      *uuid.UUID    `json:"item_id"`
	Description string        `json:"description"`
	Quantity    platform.Dec4 `json:"quantity"`
	UnitPrice   platform.Dec4 `json:"unit_price"`
	Amount      platform.Dec4 `json:"amount"`
	Position    int           `json:"position"`
}

// InvoiceOut is an invoice (or recurring template) as the routes answer it,
// in the field order of Python's InvoiceOut (schemas/invoice.py:137-176).
// billing_cycle_ref is the stored JSON object as Postgres returns it (its
// key order), null when NULL. line_items is [] when there are none, and
// expected_cash, a computed field in Python, comes last.
type InvoiceOut struct {
	InvoiceID     uuid.UUID      `json:"invoice_id"`
	CustomerID    *uuid.UUID     `json:"customer_id"`
	ProjectID     *uuid.UUID     `json:"project_id"`
	SourceQuoteID *uuid.UUID     `json:"source_quote_id"`
	InvoiceType   string         `json:"invoice_type"`
	InvoiceNumber *string        `json:"invoice_number"`
	PoSoNumber    *string        `json:"po_so_number"`
	Currency      string         `json:"currency"`
	Subtotal      *platform.Dec4 `json:"subtotal"`
	DiscountType  *string        `json:"discount_type"`
	DiscountValue *platform.Dec4 `json:"discount_value"`

	WhtType    string         `json:"wht_type"`
	WhtRate    *platform.Dec4 `json:"wht_rate"`
	WhtGrossUp bool           `json:"wht_gross_up"`
	WhtBase    *platform.Dec4 `json:"wht_base"`
	WhtAmount  platform.Dec4  `json:"wht_amount"`
	WhtBooked  platform.Dec4  `json:"wht_booked"`

	Amount          platform.Dec4   `json:"amount"`
	BalanceDue      platform.Dec4   `json:"balance_due"`
	Status          string          `json:"status"`
	BillingCycleRef json.RawMessage `json:"billing_cycle_ref"`

	IssueDate     *platform.Date `json:"issue_date"`
	DueDate       *platform.Date `json:"due_date"`
	CoverageStart *platform.Date `json:"coverage_start"`
	CoverageEnd   *platform.Date `json:"coverage_end"`
	PaymentTerms  *string        `json:"payment_terms"`
	Notes         *string        `json:"notes"`
	Footer        *string        `json:"footer"`
	InternalNote  *string        `json:"internal_note"`
	IsTemplate    bool           `json:"is_template"`
	CreatedAt     platform.Time  `json:"created_at"`

	LineItems    []LineItemOut `json:"line_items" nullable:"false"`
	ExpectedCash platform.Dec4 `json:"expected_cash" doc:"balance_due less the withholding not yet booked, never below zero."`
}

// InvoiceDetail is GET /invoices/{invoice_id}: InvoiceOut and who cleared a
// PAID invoice. The list, create, update and lifecycle routes (and the MCP
// tools) answer InvoiceOut alone, so they stay one query cheaper and their
// field order is unchanged.
type InvoiceDetail struct {
	InvoiceOut
	ClearedAt *platform.Time `json:"cleared_at" doc:"When the newest CLEARED or OVERRIDDEN reconciliation of one of the invoice's still-CLEARED payments was logged; null unless the invoice is PAID or when none was logged."`
	ClearedBy *string        `json:"cleared_by" doc:"Display name, else email, of the user behind that log row; null when the matching engine cleared it (or cleared_at is null)."`
}

// TransformSchema describes billing_cycle_ref, which Huma sees as any value
// (json.RawMessage), as the object or null it always is.
func (InvoiceOut) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	if p, ok := s.Properties["billing_cycle_ref"]; ok {
		p.Type = huma.TypeObject
		p.Nullable = true
		p.AdditionalProperties = true
		p.Description = "The schedule of a recurring template, the template and cycle of a generated invoice, the milestone reference, or the usage lock; null when unset."
	}
	return s
}
