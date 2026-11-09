package quotations

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// ConvertToInvoiceRequest is the body of a conversion
// (schemas/quotation.py:85-87): required, though both fields are optional.
type ConvertToInvoiceRequest struct {
	IssueDate platform.Optional[platform.Date] `json:"issue_date" doc:"The invoice's issue date; null is today."`
	DueInDays platform.NotNull[int]            `json:"due_in_days" doc:"Days from the issue date to the due date, used only when the quotation has no payment terms; may be negative. Defaults to 14."`
}

type convertIn struct {
	QuotationID uuid.UUID `path:"quotation_id"`
	Body        ConvertToInvoiceRequest
}

type invoiceBody struct{ Body invoices.InvoiceOut }

func (r routes) registerConvert(api huma.API, prefix string, tags []string) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-convert-to-invoice", Method: http.MethodPost,
		Path: prefix + "/{quotation_id}/convert-to-invoice", Tags: tags,
		Summary:       "Copy a quotation into a new DRAFT invoice and mark the quotation INVOICED",
		Description:   "Any status but INVOICED, VOID and DECLINED converts. Non-empty payment terms set the due date, even ones that do not parse (14 days); due_in_days counts only without them. The withholding is the customer's default.",
		DefaultStatus: http.StatusCreated, Errors: []int{http.StatusBadRequest},
	}), r.convert)
}

func (r routes) convert(ctx context.Context, in *convertIn) (*invoiceBody, error) {
	today := todayUTC(r.clock)
	var out invoices.InvoiceOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = convertToInvoice(ctx, tx, in.QuotationID, in.Body, today)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &invoiceBody{Body: out}, nil
}

// convertToInvoice is Python's convert_to_invoice (quoting.py:266-339) in tx.
//
// Go changes this (port note): the quotation is read FOR UPDATE before
// anything else, so of two concurrent conversions the second waits and then
// reads INVOICED (Python's 400) instead of making a second invoice. Lock
// order: the quotation row, then the invoice number lock
// (invoices.NextInvoiceNumber, always last).
func convertToInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID, b ConvertToInvoiceRequest, today time.Time) (invoices.InvoiceOut, error) {
	q := quotationsdb.New(tx)
	iq := invoicesdb.New(tx)
	quote, err := lock(ctx, q, id)
	if err != nil {
		return invoices.InvoiceOut{}, err
	}
	switch quote.Status {
	case "INVOICED", "VOID", "DECLINED":
		return invoices.InvoiceOut{}, refuse("quotation_not_convertible", "cannot convert a %s quotation to invoice", quote.Status)
	}
	lines, err := q.ListQuotationLines(ctx, id)
	if err != nil {
		return invoices.InvoiceOut{}, err
	}
	if len(lines) == 0 {
		return invoices.InvoiceOut{}, refuse("quotation_has_no_lines", "quotation has no line items to convert")
	}

	issue := today
	if v := b.IssueDate.Merge(nil); v != nil {
		issue = v.Time
	}
	due, err := dueDate(issue, quote.PaymentTerms, b.DueInDays.Or(14))
	if err != nil {
		return invoices.InvoiceOut{}, err
	}
	// The customer's default only: a quotation has no withholding of its own.
	whtType, whtRate, grossUp, err := invoices.ResolveWHT(ctx, iq, &quote.CustomerID, nil, nil, nil)
	if err != nil {
		return invoices.InvoiceOut{}, err
	}
	// On the stored amount (after discount), not on the lines.
	wht := withholding.Compute(quote.Amount, quote.Currency, whtType, whtRate, grossUp)
	// Today's year, never the issue date's.
	number, err := invoices.NextInvoiceNumber(ctx, tx, today)
	if err != nil {
		return invoices.InvoiceOut{}, err
	}

	invoiceID := uuid.New()
	err = iq.InsertInvoice(ctx, invoicesdb.InsertInvoiceParams{
		InvoiceID: invoiceID, CustomerID: pgUUID(&quote.CustomerID), ProjectID: quote.ProjectID,
		SourceQuoteID: pgUUID(&quote.QuotationID), InvoiceType: "MILESTONE", InvoiceNumber: &number,
		PoSoNumber: quote.PoSoNumber, Currency: quote.Currency,
		Subtotal: quote.Subtotal, DiscountType: quote.DiscountType, DiscountValue: quote.DiscountValue,
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&wht.Base),
		WhtAmount: wht.Amount, Amount: wht.Total, BalanceDue: wht.Total, Status: "DRAFT",
		IssueDate: pgDate(issue), DueDate: pgDate(due),
		PaymentTerms: quote.PaymentTerms, Notes: quote.Notes, Footer: quote.Footer,
	})
	if err != nil {
		return invoices.InvoiceOut{}, invoices.MapWriteError(err)
	}
	// Verbatim, the stored amount included. The items exist: the quotation's
	// lines reference them.
	for _, l := range lines {
		err := iq.InsertInvoiceLine(ctx, invoicesdb.InsertInvoiceLineParams{
			LineItemID: uuid.New(), InvoiceID: invoiceID, ItemID: l.ItemID, Position: l.Position,
			Description: l.Description, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Amount: l.Amount,
		})
		if err != nil {
			return invoices.InvoiceOut{}, err
		}
	}
	err = q.MarkQuotationInvoiced(ctx, quotationsdb.MarkQuotationInvoicedParams{
		QuotationID: id, ConvertedInvoiceID: pgUUID(&invoiceID),
	})
	if err != nil {
		return invoices.InvoiceOut{}, err
	}
	return invoices.Get(ctx, iq, invoiceID)
}

// dueDate is quoting.py:284-287 and :318: non-empty payment terms give the
// days (Python's truthiness: unparseable terms are 14 and still override
// dueInDays), otherwise dueInDays. A due date out of Python's date range,
// or terms whose N does not fit an int, is invoicing's 400
// due_date_out_of_range (Python: 500).
func dueDate(issue time.Time, terms *string, dueInDays int) (time.Time, error) {
	if terms != nil && *terms != "" {
		return invoices.DueAfterTerms(issue, terms)
	}
	return invoices.AddDays(issue, dueInDays)
}
