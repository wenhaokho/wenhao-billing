package invoices

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// documents are the routes that render an invoice as a PDF and mail it.
type documents struct {
	pool  *pgxpool.Pool
	mail  email.Sender
	clock platform.Clock
}

// RegisterDocuments adds GET .../pdf and POST .../send; both are admin-only.
// A nil mail logs each message (email.LogSender).
func RegisterDocuments(api huma.API, pool *pgxpool.Pool, clock platform.Clock, mail email.Sender) {
	if mail == nil {
		mail = email.LogSender{}
	}
	d := documents{pool: pool, mail: mail, clock: clock}
	const prefix = "/api/v1/invoices/{invoice_id}"
	tags := []string{"invoices"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-pdf", Method: http.MethodGet, Path: prefix + "/pdf", Tags: tags,
		Summary: "The invoice as a PDF (any status, templates included)",
		Errors:  []int{http.StatusNotFound},
		Responses: map[string]*huma.Response{"200": {Description: "The PDF", Content: map[string]*huma.MediaType{
			"application/pdf": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}},
	}), d.pdf)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-send", Method: http.MethodPost, Path: prefix + "/send", Tags: tags,
		Summary: "Email the invoice PDF; a DRAFT or OPEN invoice becomes SENT once it is sent",
		Description: "Refuses a VOID invoice and a send with no recipient (400). A send failure is 502 and changes nothing. " +
			"The status moves without finalizing the invoice.",
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway},
	}), d.send)
}

// SendInvoiceRequest is the body of send (Python's SendInvoiceRequest). to_email is
// checked as an email address when given (Go change 9).
type SendInvoiceRequest struct {
	ToEmail platform.Optional[string] `json:"to_email" doc:"The recipient; empty or null falls back to the customer's contact email."`
	CcEmail platform.Optional[string] `json:"cc_email" doc:"Copied addresses separated by commas, semicolons or spaces; each must be an email address."`
	Subject platform.Optional[string] `json:"subject" doc:"Empty or null: Invoice <number>."`
	Message platform.Optional[string] `json:"message" doc:"Empty or null: the default body."`
}

// pdfOut is a PDF response.
type pdfOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

type sendIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      SendInvoiceRequest
}

func notFound() error {
	return platform.Err(http.StatusNotFound, "invoice_not_found", "invoice not found")
}

func pdfFilename(number *string, id uuid.UUID) string {
	if number != nil && *number != "" {
		return "invoice-" + *number + ".pdf"
	}
	return "invoice-" + id.String()[:8] + ".pdf"
}

func (d documents) pdf(ctx context.Context, in *idIn) (*pdfOut, error) {
	data, err := pdf.LoadInvoice(ctx, d.pool, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	b, err := pdf.RenderInvoice(ctx, pdf.InvoiceDocument(data))
	if err != nil {
		return nil, err
	}
	return &pdfOut{
		ContentType:        "application/pdf",
		ContentDisposition: pdf.ContentDisposition(pdfFilename(data.Invoice.InvoiceNumber, in.InvoiceID)),
		Body:               b,
	}, nil
}

// orDefault is the value of an Optional text, or def when it is null or empty.
func orDefault(o platform.Optional[string], def string) string {
	if v := o.Merge(nil); v != nil && *v != "" {
		return *v
	}
	return def
}

func (d documents) send(ctx context.Context, in *sendIn) (*invoiceBody, error) {
	// Body validation runs before the handler in Python: a bad address is a
	// 422 even for an unknown invoice.
	toGiven := orDefault(in.Body.ToEmail, "")
	if toGiven != "" {
		norm, ok := platform.NormalizeEmail(toGiven)
		if !ok {
			return nil, platform.FieldError("to_email", "invalid email address: "+toGiven)
		}
		toGiven = norm
	}
	var cc []string
	if v := in.Body.CcEmail.Merge(nil); v != nil {
		joined, err := platform.ParseCcEmails(*v)
		if err != nil {
			return nil, err
		}
		if joined != nil {
			cc = strings.Split(*joined, ", ")
		}
	}

	data, err := pdf.LoadInvoice(ctx, d.pool, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	inv := data.Invoice
	if inv.Status == "VOID" {
		return nil, platform.Err(http.StatusBadRequest, "invoice_void", "cannot send a voided invoice")
	}
	to := toGiven
	if to == "" && data.Customer != nil && data.Customer.ContactEmail != nil {
		to = *data.Customer.ContactEmail
	}
	if to == "" {
		return nil, platform.Err(http.StatusBadRequest, "no_recipient",
			"no recipient: provide to_email or set a contact_email on the customer")
	}

	pdfBytes, err := pdf.RenderInvoice(ctx, pdf.InvoiceDocument(data))
	if err != nil {
		return nil, err
	}
	number := ""
	if inv.InvoiceNumber != nil {
		number = *inv.InvoiceNumber
	}
	defSubject := "Invoice"
	if number != "" {
		defSubject = "Invoice " + number
	}
	greeting := "there"
	if data.Customer != nil {
		greeting = data.Customer.Name
	}
	body := "Hi " + greeting + ",\n\n" +
		"Please find attached invoice " + number + " for " + pdf.FmtAmount(inv.Amount, inv.Currency) + ".\n"
	if inv.WhtType != "NONE" && inv.WhtType != "" {
		cash := withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked)
		body += "Amount to transfer (after withholding tax): " + pdf.FmtAmount(cash, inv.Currency) + ".\n"
	}
	if inv.DueDate.Valid {
		body += "Due date: " + inv.DueDate.Time.Format("2006-01-02") + ".\n"
	}
	body += "\nThank you."

	msg := email.Message{
		To: to, Cc: cc, Subject: orDefault(in.Body.Subject, defSubject), Text: orDefault(in.Body.Message, body),
		Attachments: []email.Attachment{{
			Filename: pdfFilename(inv.InvoiceNumber, in.InvoiceID), ContentType: "application/pdf", Content: pdfBytes,
		}},
	}
	msg.IdempotencyKey = email.IdempotencyKey(in.InvoiceID.String(), msg, d.clock.Now())
	if err := d.mail.Send(ctx, msg); err != nil {
		return nil, platform.Err(http.StatusBadGateway, "invoice_send_failed", "failed to send invoice email")
	}

	// Only now does the status move, and on the row as it is now. The email
	// is out, so a client that hung up must not skip the update.
	wctx := context.WithoutCancel(ctx)
	var out InvoiceOut
	err = db.InTx(wctx, d.pool, func(tx pgx.Tx) error {
		q := invoicesdb.New(tx)
		if _, err := lock(wctx, q, in.InvoiceID); err != nil {
			return err
		}
		if err := q.MarkInvoiceSent(wctx, in.InvoiceID); err != nil {
			return err
		}
		var err error
		out, err = Get(wctx, q, in.InvoiceID)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &invoiceBody{Body: out}, nil
}
