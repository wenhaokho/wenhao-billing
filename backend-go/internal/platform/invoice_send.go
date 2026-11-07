package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

type invoiceSendSchema struct {
	ToEmail *string `json:"to_email,omitempty"`
	CCEmail *string `json:"cc_email,omitempty"`
	Subject *string `json:"subject,omitempty"`
	Message *string `json:"message,omitempty"`
}

func registerInvoiceSend(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid invoice or recipient", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Invoice not found", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Delivery failed", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/{invoice_id}/send", OperationID: "send-invoice", Security: []map[string][]string{{"sessionCookie": {}}}, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceSend)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"invoice_id" format:"uuid"`
		Body invoiceSendSchema
	}) (*struct{ Body invoiceOutput }, error) {
		panic("middleware")
	})
}

type invoiceSendQueries interface {
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	LockInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	GetInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	GetCustomer(context.Context, pgtype.UUID) (dbquery.Customer, error)
	GetBusinessProfile(context.Context) (dbquery.BusinessProfile, error)
	ListBusinessPaymentAccounts(context.Context) ([]dbquery.PaymentAccount, error)
	InvoiceLinesByIDs(context.Context, []pgtype.UUID) ([]dbquery.InvoiceLineItem, error)
	recordInvoiceSend(context.Context, pgtype.UUID) error
}

type invoiceSender struct {
	pool    transactionOptionsBeginner
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) invoiceSendQueries
	sender  email.Sender
}

type legacyInvoiceSendQueries struct {
	*dbquery.Queries
	tx pgx.Tx
}

func (q legacyInvoiceSendQueries) lockAdminRole(ctx context.Context, id pgtype.UUID) (string, error) {
	user, err := q.LockSessionUserByID(ctx, id)
	return user.Role, err
}

func (q legacyInvoiceSendQueries) recordInvoiceSend(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `UPDATE invoices SET status='SENT' WHERE invoice_id=$1 AND status IN ('DRAFT','OPEN')`, id)
	return err
}

func (s sessionHandler) invoiceSend(w http.ResponseWriter, r *http.Request) {
	sender := invoiceSender{
		pool: s.db,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			user, ok := s.requireAdmin(w, r)
			return user.UserID, ok
		},
		queries: func(tx pgx.Tx) invoiceSendQueries {
			return legacyInvoiceSendQueries{Queries: dbquery.New(tx), tx: tx}
		},
		sender: s.config.Sender,
	}
	sender.send(w, r)
}

func (s invoiceSender) send(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	userID, ok := s.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, valid := parsePathUUID(raw)
	var validation []validationError
	if !valid {
		d := uuidErrorDetail(raw)
		validation = append(validation, validationError{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
	}
	if len(body) == 0 {
		validationResponse(w, append(validation, validationError{"missing", []any{"body"}, "Field required", nil, nil}))
		return
	}
	if !requestJSONMediaType(r) {
		validationResponse(w, append(validation, validationError{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}))
		return
	}
	var value any
	_ = json.Unmarshal(body, &value)
	if _, ok := value.(map[string]any); !ok {
		validationResponse(w, append(validation, validationError{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}))
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	var input invoiceSendSchema
	for _, field := range []struct {
		name   string
		target **string
	}{{"to_email", &input.ToEmail}, {"cc_email", &input.CCEmail}, {"subject", &input.Subject}, {"message", &input.Message}} {
		raw, exists := fields[field.name]
		if !exists || string(raw) == "null" {
			continue
		}
		var value any
		_ = json.Unmarshal(raw, &value)
		str, ok := value.(string)
		if !ok {
			validation = append(validation, validationError{"string_type", []any{"body", field.name}, "Input should be a valid string", value, nil})
			continue
		}
		*field.target = &str
		if field.name == "cc_email" {
			for _, address := range strings.FieldsFunc(str, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
				if _, reason := normalizeEmail(address); reason != "" {
					validation = append(validation, validationError{"value_error", []any{"body", "cc_email"}, "Value error, invalid email address: " + address, str, map[string]any{"error": map[string]any{}}})
					break
				}
			}
		}
	}
	if len(validation) > 0 {
		validationResponse(w, validation)
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		retry := func() bool {
			ctx := r.Context()
			tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			defer tx.Rollback(context.Background())
			q := s.queries(tx)
			role, err := q.lockAdminRole(ctx, userID)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
				authError(w, 403, "admin required")
				return false
			}
			if err != nil {
				if invoiceSendSerialization(err) {
					return true
				}
				authError(w, 500, "internal server error")
				return false
			}
			inv, err := q.LockInvoice(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				authError(w, 404, "invoice not found")
				return false
			}
			if err != nil {
				if invoiceSendSerialization(err) {
					return true
				}
				authError(w, 500, "internal server error")
				return false
			}
			if inv.Status == "VOID" {
				authError(w, 400, "cannot send a voided invoice")
				return false
			}
			customer, err := q.GetCustomer(ctx, inv.CustomerID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				authError(w, 500, "internal server error")
				return false
			}
			business, err := q.GetBusinessProfile(ctx)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				authError(w, 500, "internal server error")
				return false
			}
			to := customer.ContactEmail.String
			if input.ToEmail != nil && *input.ToEmail != "" {
				to = *input.ToEmail
			}
			if to == "" {
				authError(w, 400, "no recipient: provide to_email or set a contact_email on the customer")
				return false
			}
			validAddress := func(s string) bool {
				a, e := mail.ParseAddress(s)
				return e == nil && a.Address != "" && !strings.ContainsAny(s, "\r\n")
			}
			if !validAddress(to) {
				authError(w, 400, "invalid recipient email")
				return false
			}
			var cc []string
			seen := map[string]bool{}
			if input.CCEmail != nil {
				for _, v := range strings.FieldsFunc(*input.CCEmail, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
					normalized, _ := normalizeEmail(v)
					key := strings.ToLower(normalized)
					if !seen[key] {
						cc = append(cc, normalized)
						seen[key] = true
					}
				}
			}
			subject := "Invoice"
			if inv.InvoiceNumber.String != "" {
				subject += " " + inv.InvoiceNumber.String
			}
			if input.Subject != nil && *input.Subject != "" {
				subject = *input.Subject
			}
			if strings.ContainsAny(subject, "\r\n") {
				authError(w, 400, "invalid subject")
				return false
			}
			lines, err := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			preflight, err := invoiceJSON(inv, lines)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			if _, err = json.Marshal(preflight); err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			greeting := customer.Name
			if !inv.CustomerID.Valid {
				greeting = "there"
			}
			text := fmt.Sprintf("Hi %s,\n\nPlease find attached invoice %s for %s %s.\n", greeting, inv.InvoiceNumber.String, quotationNumber(inv.Amount, inv.Currency), inv.Currency)
			if inv.WhtType != "NONE" {
				cash, err := decimal.NewFromString(preflight.ExpectedCash)
				if err != nil {
					authError(w, 500, "internal server error")
					return false
				}
				text += fmt.Sprintf("Amount to transfer (after withholding tax): %s %s.\n", quotationNumber(invoiceNumeric(cash), inv.Currency), inv.Currency)
			}
			if inv.DueDate.Valid {
				text += fmt.Sprintf("Due date: %s.\n", inv.DueDate.Time.Format("2006-01-02"))
			}
			text += "\nThank you."
			if input.Message != nil && *input.Message != "" {
				text = *input.Message
			}
			accounts, err := q.ListBusinessPaymentAccounts(ctx)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			pdf, err := renderInvoicePDF(ctx, inv, customer, business, lines, accounts)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			name := inv.InvoiceNumber.String
			if name == "" {
				name = fmt.Sprintf("%x", id.Bytes[:4])
			}
			if strings.ContainsAny(name, "\r\n") {
				authError(w, 400, "invalid invoice number")
				return false
			}
			if s.sender == nil {
				authError(w, 500, "email delivery unavailable")
				return false
			}
			if err = q.recordInvoiceSend(ctx, id); err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			inv, err = q.GetInvoice(ctx, id)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			out, err := invoiceJSON(inv, lines)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = s.sender.Send(sendCtx, email.Message{To: []string{to}, CC: cc, Subject: subject, Text: text, Attachments: []email.Attachment{{Filename: "invoice-" + name + ".pdf", Content: pdf, ContentType: "application/pdf"}}})
			cancel()
			if err != nil {
				authError(w, 500, "email delivery failed")
				return false
			}
			// Once accepted, finish recording the send even if the HTTP client leaves.
			finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer finalCancel()
			if err = tx.Commit(finalCtx); err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(append(encoded, '\n'))
			return false
		}()
		if !retry {
			return
		}
	}
	authError(w, 500, "internal server error")

}

func invoiceSendSerialization(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}
