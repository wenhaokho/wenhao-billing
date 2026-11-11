package payments

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ReceiptDeps are what send-receipt needs: the mail it sends with and the
// renderer of the PDF it attaches.
type ReceiptDeps struct {
	Sender   email.Sender
	Renderer ReceiptRenderer
}

// SendReceiptRequest is the body of send-receipt, Python's SendReceiptRequest
// (schemas/payment.py:69-76).
type SendReceiptRequest struct {
	ToEmail platform.Optional[string] `json:"to_email" doc:"The recipient, any text; empty or null falls back to the customer's contact email."`
	CcEmail platform.Optional[string] `json:"cc_email" doc:"Copied addresses separated by commas, semicolons or spaces; each must be an email address."`
}

type sendReceiptIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	PaymentID uuid.UUID `path:"payment_id"`
	Body      SendReceiptRequest
}

// SentTo is the answer of send-receipt.
type SentTo struct {
	SentTo string `json:"sent_to"`
}

type sentToBody struct{ Body SentTo }

func (r routes) registerReceipt(api huma.API) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-send-receipt", Method: http.MethodPost,
		Path: "/api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt", Tags: []string{"payments"},
		Summary: "Email a payment receipt (PDF attached) for a CLEARED payment of the invoice",
		Description: "Writes nothing, so a failed send can be retried. A payment that is missing, on another invoice or not CLEARED is 404. " +
			"The recipient is to_email, else the customer's contact email (400 when neither). Any sender error is 502.",
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway},
	}), r.sendReceipt)
}

func (r routes) sendReceipt(ctx context.Context, in *sendReceiptIn) (*sentToBody, error) {
	// Python's body validation runs before the handler: a bad CC is a 422
	// even for an unknown invoice.
	var cc *string
	if v := in.Body.CcEmail.Merge(nil); v != nil {
		var err error
		if cc, err = platform.ParseCcEmails(*v); err != nil {
			return nil, err
		}
	}
	inv, err := r.q.ReceiptInvoice(ctx, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, clientError(invoiceNotFound())
	}
	if err != nil {
		return nil, err
	}
	// Go change 7: only a CLEARED payment gets a receipt (Python mailed one
	// for a held payment too).
	pay, err := r.q.GetPayment(ctx, in.PaymentID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err != nil || !pay.InvoiceID.Valid || uuid.UUID(pay.InvoiceID.Bytes) != in.InvoiceID || pay.Status != matching.StatusCleared {
		return nil, platform.Err(http.StatusNotFound, "payment_not_on_invoice", "payment not found on this invoice")
	}

	to := stringOrEmpty(in.Body.ToEmail)
	if to == "" && inv.CustomerEmail != nil {
		to = *inv.CustomerEmail
	}
	if to == "" {
		return nil, platform.Err(http.StatusBadRequest, "no_recipient", "no recipient: provide to_email or set a contact_email on the customer")
	}

	business, err := r.q.BusinessName(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	number := ""
	if inv.InvoiceNumber != nil {
		number = *inv.InvoiceNumber
	}
	// The balance right after this payment, not the live one: the email and
	// the attached PDF must agree, and an old partial payment's receipt must
	// not say "paid in full" (pdf.BalanceAfter).
	loaded, err := pdf.LoadReceipt(ctx, r.pool, pay.PaymentID)
	if err != nil {
		return nil, err
	}
	balance := loaded.BalanceAfter // also the renderer's BalanceDue
	ref := ReceiptReference(pay.PaymentID)
	receiptPDF, err := r.receipts.Renderer.RenderReceipt(ctx, ReceiptData{
		Payment: PaymentOutFrom(pay), Reference: ref, InvoiceNumber: number, Currency: inv.Currency,
		BalanceDue: balance, CustomerName: inv.CustomerName, BusinessName: business,
	})
	if err != nil {
		return nil, err
	}

	subject, invRef := "Payment receipt", "your invoice"
	if number != "" {
		subject, invRef = "Payment receipt for invoice "+number, "invoice "+number
	}
	greeting := "there"
	if inv.CustomerName != nil {
		greeting = *inv.CustomerName
	}
	text := "Hi " + greeting + ",\n\n" +
		"We have received your payment of " + FormatAmount(pay.Amount, pay.Currency) + " against " + invRef +
		" on " + pay.PaymentDate.Time.Format("2006-01-02") + ".\n" +
		BalanceNote(balance, inv.Currency) + "\n\n" +
		"A PDF receipt is attached for your records.\n\n" +
		"Thank you."
	msg := email.Message{
		To: to, Subject: subject, Text: text,
		Attachments: []email.Attachment{{Filename: "receipt-" + ref + ".pdf", ContentType: "application/pdf", Content: receiptPDF}},
	}
	if cc != nil {
		msg.Cc = strings.Split(*cc, ", ")
	}
	if err := r.receipts.Sender.Send(ctx, msg); err != nil {
		return nil, platform.Err(http.StatusBadGateway, "receipt_send_failed", "failed to send receipt email")
	}
	return &sentToBody{Body: SentTo{SentTo: to}}, nil
}
