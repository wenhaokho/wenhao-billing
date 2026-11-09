package quotations

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
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// documents are the routes that render a quotation as a PDF and mail it.
type documents struct {
	pool  *pgxpool.Pool
	clock platform.Clock
	mail  email.Sender
}

// RegisterDocuments adds GET .../pdf and POST .../send; both are admin-only.
// A nil mail logs each message (email.LogSender).
func RegisterDocuments(api huma.API, pool *pgxpool.Pool, clock platform.Clock, mail email.Sender) {
	if mail == nil {
		mail = email.LogSender{}
	}
	d := documents{pool: pool, clock: clock, mail: mail}
	const prefix = "/api/v1/quotations/{quotation_id}"
	tags := []string{"quotations"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-pdf", Method: http.MethodGet, Path: prefix + "/pdf", Tags: tags,
		Summary: "The quotation as a PDF (any status)",
		Errors:  []int{http.StatusNotFound},
		Responses: map[string]*huma.Response{"200": {Description: "The PDF", Content: map[string]*huma.MediaType{
			"application/pdf": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}},
	}), d.pdf)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-send", Method: http.MethodPost, Path: prefix + "/send", Tags: tags,
		Summary:     "Email the quotation PDF; a DRAFT quotation becomes SENT and last_sent_at is stamped once it is sent",
		Description: "Refuses a VOID or INVOICED quotation and a send with no recipient (400). A send failure is 502 and changes nothing.",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway},
	}), d.send)
}

// SendQuotationRequest is the body of send (Python's SendQuotationRequest). to_email is
// checked as an email address when given (Go change 9).
type SendQuotationRequest struct {
	ToEmail platform.Optional[string] `json:"to_email" doc:"The recipient; empty or null falls back to the customer's contact email."`
	CcEmail platform.Optional[string] `json:"cc_email" doc:"Copied addresses separated by commas, semicolons or spaces; each must be an email address."`
	Subject platform.Optional[string] `json:"subject" doc:"Empty or null: Quotation <number>."`
	Message platform.Optional[string] `json:"message" doc:"Empty or null: the default body."`
}

// pdfOut is a PDF response.
type pdfOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

type sendIn struct {
	QuotationID uuid.UUID `path:"quotation_id"`
	Body        SendQuotationRequest
}

func notFound() error {
	return platform.Err(http.StatusNotFound, "quotation_not_found", "quotation not found")
}

func pdfFilename(number *string, id uuid.UUID) string {
	if number != nil && *number != "" {
		return "quotation-" + *number + ".pdf"
	}
	return "quotation-" + id.String()[:8] + ".pdf"
}

func (d documents) pdf(ctx context.Context, in *idIn) (*pdfOut, error) {
	data, err := pdf.LoadQuotation(ctx, d.pool, in.QuotationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	b, err := pdf.RenderQuotation(ctx, pdf.QuotationDocument(data))
	if err != nil {
		return nil, err
	}
	return &pdfOut{
		ContentType:        "application/pdf",
		ContentDisposition: pdf.ContentDisposition(pdfFilename(data.Quotation.QuotationNumber, in.QuotationID)),
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

func (d documents) send(ctx context.Context, in *sendIn) (*quotationBody, error) {
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

	data, err := pdf.LoadQuotation(ctx, d.pool, in.QuotationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	q := data.Quotation
	if q.Status == "VOID" || q.Status == "INVOICED" {
		return nil, platform.Err(http.StatusBadRequest, "quotation_not_sendable", "cannot send a "+q.Status+" quotation")
	}
	to := toGiven
	if to == "" && data.Customer != nil && data.Customer.ContactEmail != nil {
		to = *data.Customer.ContactEmail
	}
	if to == "" {
		return nil, platform.Err(http.StatusBadRequest, "no_recipient",
			"no recipient: provide to_email or set a contact_email on the customer")
	}

	pdfBytes, err := pdf.RenderQuotation(ctx, pdf.QuotationDocument(data))
	if err != nil {
		return nil, err
	}
	number := ""
	if q.QuotationNumber != nil {
		number = *q.QuotationNumber
	}
	defSubject := "Quotation"
	if number != "" {
		defSubject = "Quotation " + number
	}
	greeting := "there"
	if data.Customer != nil {
		greeting = data.Customer.Name
	}
	// Go change 3: the amount is FmtAmount's (Python printed the raw decimal).
	body := "Hi " + greeting + ",\n\n" +
		"Please find attached our quotation " + number + " for " + pdf.FmtAmount(q.Amount, q.Currency) + ".\n"
	if q.ValidUntil.Valid {
		body += "Valid until: " + q.ValidUntil.Time.Format("2006-01-02") + ".\n"
	}
	body += "\nThank you."

	msg := email.Message{
		To: to, Cc: cc, Subject: orDefault(in.Body.Subject, defSubject), Text: orDefault(in.Body.Message, body),
		Attachments: []email.Attachment{{
			Filename: pdfFilename(q.QuotationNumber, in.QuotationID), ContentType: "application/pdf", Content: pdfBytes,
		}},
	}
	msg.IdempotencyKey = email.IdempotencyKey(in.QuotationID.String(), msg, d.clock.Now())
	if err := d.mail.Send(ctx, msg); err != nil {
		return nil, platform.Err(http.StatusBadGateway, "quotation_send_failed", "failed to send quotation email")
	}

	// Only now do the status and last_sent_at change, on the row as it is
	// now. The email is out, so a client that hung up must not skip it.
	wctx := context.WithoutCancel(ctx)
	at := instant(d.clock)
	var out QuotationOut
	err = db.InTx(wctx, d.pool, func(tx pgx.Tx) error {
		qq := quotationsdb.New(tx)
		cur, err := lock(wctx, qq, in.QuotationID)
		if err != nil {
			return err
		}
		// Voided or converted while the email went out: leave the row alone.
		if cur.Status != "VOID" && cur.Status != "INVOICED" {
			if err := qq.MarkQuotationSentOnSend(wctx, quotationsdb.MarkQuotationSentOnSendParams{
				QuotationID: in.QuotationID, LastSentAt: &at,
			}); err != nil {
				return err
			}
		}
		out, err = get(wctx, qq, in.QuotationID)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &quotationBody{Body: out}, nil
}
