package invoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// lock reads invoice id FOR UPDATE; an unknown one is Python's refusal
// (a 400 on every write route).
func lock(ctx context.Context, q *invoicesdb.Queries, id uuid.UUID) (invoicesdb.Invoice, error) {
	inv, err := q.GetInvoiceForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return invoicesdb.Invoice{}, refuse("invoice_not_found", "invoice %s not found", id)
	}
	return inv, err
}

// FinalizeInvoice is the finalize route's transition: a DRAFT invoice in
// tx, posted to the ledger in base. Exported for the MCP finalize_invoice
// tool (inventory §4).
func FinalizeInvoice(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID) (InvoiceOut, error) {
	return finalizeInvoice(ctx, tx, base, id)
}

// finalizeInvoice is Python's finalize_invoice (invoicing.py:643-664): a
// DRAFT that owes something is issued as OPEN (SENT is for the email); one
// with nothing to collect is settled straight to PAID.
//
// Go's own (PORT-NOTES 162): an invoice issued as OPEN posts its issue
// entry under the row lock taken here; a ledger refusal or a missing rate
// leaves it DRAFT. A PAID one (amount 0) posts nothing.
func finalizeInvoice(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	inv, err := lock(ctx, q, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	if inv.Status != "DRAFT" {
		return InvoiceOut{}, refuse("invoice_not_draft", "only DRAFT invoices can be finalized (got %s)", inv.Status)
	}
	if inv.IsTemplate {
		return InvoiceOut{}, refuse("invoice_is_template", "cannot finalize a recurring template")
	}
	// Go's own: the receivable posted is the amount, so a balance that is
	// not the amount (never written by Go's DRAFT paths) is refused rather
	// than issued, or settled to PAID, out of step with the ledger.
	if inv.Amount.IsPositive() && !inv.BalanceDue.Equal(inv.Amount) {
		return InvoiceOut{}, ledger.Refuse("invoice %s balance_due %s is not its amount %s; cannot issue it",
			id, inv.BalanceDue.StringFixed(4), inv.Amount.StringFixed(4))
	}
	status, balance := "OPEN", inv.BalanceDue
	if !balance.IsPositive() {
		status, balance = "PAID", decimal.Zero
	}
	if status == "OPEN" && inv.Amount.IsPositive() {
		if err := postIssue(ctx, tx, base, inv); err != nil {
			return InvoiceOut{}, err
		}
	}
	return transition(ctx, q, id, status, balance)
}

// settleInvoice is Python's settle_zero_value_invoice (invoicing.py:667-690):
// an issued invoice with nothing outstanding becomes PAID; one that still
// owes is refused, so a real receivable is never wiped out.
func settleInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	inv, err := lock(ctx, q, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	switch inv.Status {
	case "OPEN", "SENT", "PARTIAL":
	default:
		return InvoiceOut{}, refuse("invoice_not_settleable", "only OPEN, SENT or PARTIAL invoices can be settled this way (got %s)", inv.Status)
	}
	if inv.BalanceDue.IsPositive() {
		return InvoiceOut{}, refuse("invoice_balance_outstanding",
			"invoice has an outstanding balance of %s — record a payment instead", platform.NewDec4(inv.BalanceDue).Decimal().StringFixed(4))
	}
	return transition(ctx, q, id, "PAID", decimal.Zero)
}

// VoidInvoice is the void route's transition: an invoice in tx. Exported
// for the MCP void_invoice tool (inventory §4).
func VoidInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (InvoiceOut, error) {
	return voidInvoice(ctx, tx, id)
}

// voidInvoice is Python's void_invoice (invoicing.py:693-700): anything but
// a PAID invoice (a template included) becomes VOID with nothing owing.
//
// Go's own (PORT-NOTES 162), under the invoice's row lock: an invoice with a
// CLEARED payment (a withholding-only one included) is refused, where Python
// voided it and left its payments; the issue entry is reversed. An issued
// invoice with an amount but no issue entry is refused until the back-post
// has run; a DRAFT, a template, a zero-amount or an already VOID invoice has
// nothing to reverse.
func voidInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	inv, err := lock(ctx, q, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	if inv.Status == "PAID" {
		return InvoiceOut{}, refuse("invoice_paid", "cannot void a PAID invoice — reverse payments first")
	}
	paid, err := q.HasClearedPayments(ctx, pgUUID(&id))
	if err != nil {
		return InvoiceOut{}, err
	}
	if paid {
		return InvoiceOut{}, refuse("invoice_has_payments", "cannot void an invoice with cleared payments — reverse payments first")
	}
	reversed, err := ledger.ReverseIssued(ctx, tx, ledger.SourceInvoice, id, "Void of invoice "+id.String())
	if err != nil {
		return InvoiceOut{}, err
	}
	if !reversed && inv.Status != "DRAFT" && inv.Status != "VOID" && !inv.IsTemplate && inv.Amount.IsPositive() {
		return InvoiceOut{}, ledger.Refuse("invoice %s has no issue entry — run api backpost-ledger", id)
	}
	return transition(ctx, q, id, "VOID", decimal.Zero)
}

func transition(ctx context.Context, q *invoicesdb.Queries, id uuid.UUID, status string, balance decimal.Decimal) (InvoiceOut, error) {
	if err := q.SetInvoiceStatus(ctx, invoicesdb.SetInvoiceStatusParams{InvoiceID: id, Status: status, BalanceDue: balance}); err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

// referenced is the 409 for deleting an invoice a payment or quotation
// points to (Python answered 500).
var referenced = map[string]platform.Constraint{
	"payments_invoice_id_fkey":             {Status: http.StatusConflict, Code: "invoice_referenced", Detail: "invoice is referenced by a payment or quotation"},
	"quotations_converted_invoice_id_fkey": {Status: http.StatusConflict, Code: "invoice_referenced", Detail: "invoice is referenced by a payment or quotation"},
}

// DeleteInvoice is the delete route's invoice: a DRAFT invoice in tx.
// Exported for the MCP delete_invoice tool (inventory §4).
func DeleteInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return deleteInvoice(ctx, tx, id)
}

// deleteInvoice is Python's delete_invoice (invoicing.py:467-474): only a
// DRAFT goes, and its lines with it.
func deleteInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := invoicesdb.New(tx)
	inv, err := lock(ctx, q, id)
	if err != nil {
		return err
	}
	if inv.Status != "DRAFT" {
		return refuse("invoice_not_draft", "only DRAFT invoices can be deleted (got %s)", inv.Status)
	}
	return platform.MapConstraint(q.DeleteInvoice(ctx, id), referenced)
}

func (r routes) finalize(ctx context.Context, in *idIn) (*invoiceBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return FinalizeInvoice(ctx, tx, r.base, in.InvoiceID) })
}

func (r routes) markPaid(ctx context.Context, in *idIn) (*invoiceBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return settleInvoice(ctx, tx, in.InvoiceID) })
}

// VoidRequest is the body of a void (schemas/invoice.py:33-34).
type VoidRequest struct {
	Reason string `json:"reason" doc:"Required, and not stored."`
}

type voidIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      VoidRequest
}

func (r routes) void(ctx context.Context, in *voidIn) (*invoiceBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return VoidInvoice(ctx, tx, in.InvoiceID) })
}

func (r routes) delete(ctx context.Context, in *idIn) (*struct{}, error) {
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error { return DeleteInvoice(ctx, tx, in.InvoiceID) })
	if err != nil {
		return nil, clientError(err)
	}
	return nil, nil
}
