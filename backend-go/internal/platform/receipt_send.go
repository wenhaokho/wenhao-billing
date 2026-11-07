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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	maroto "github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

type receiptSendSchema struct {
	ToEmail *string `json:"to_email,omitempty"`
	CCEmail *string `json:"cc_email,omitempty"`
}

func registerReceiptSend(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"400", "401", "403", "404", "413", "422", "500", "502"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt", OperationID: "send-payment-receipt", Security: []map[string][]string{{"sessionCookie": {}}}, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.receiptSend)}, Responses: responses}, func(context.Context, *struct {
		InvoiceID string `path:"invoice_id" format:"uuid"`
		PaymentID string `path:"payment_id" format:"uuid"`
		Body      receiptSendSchema
	}) (*struct {
		Body struct {
			SentTo string `json:"sent_to"`
		}
	}, error) {
		panic("middleware")
	})
}

type receiptSendQueries interface {
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	LockInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	lockReceiptPayment(context.Context, pgtype.UUID, pgtype.UUID) (dbquery.Payment, error)
	GetCustomer(context.Context, pgtype.UUID) (dbquery.Customer, error)
	GetBusinessProfile(context.Context) (dbquery.BusinessProfile, error)
}

type receiptSender struct {
	pool    *pgxpool.Pool
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) receiptSendQueries
	sender  email.Sender
}

func (q legacyInvoiceSendQueries) lockReceiptPayment(ctx context.Context, invoiceID, paymentID pgtype.UUID) (dbquery.Payment, error) {
	// Preserve the legacy lock and lookup behavior.
	var locked bool
	if err := q.tx.QueryRow(ctx, `SELECT true FROM payments WHERE payment_id=$1 FOR UPDATE`, paymentID).Scan(&locked); err != nil {
		return dbquery.Payment{}, err
	}
	payment, err := q.GetPayment(ctx, paymentID)
	if err != nil {
		return dbquery.Payment{}, fmt.Errorf("load locked payment: %v", err)
	}
	if !payment.InvoiceID.Valid || payment.InvoiceID.Bytes != invoiceID.Bytes {
		return dbquery.Payment{}, pgx.ErrNoRows
	}
	return payment, nil
}

func (s sessionHandler) receiptSend(w http.ResponseWriter, r *http.Request) {
	sender := receiptSender{
		pool: s.db,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			user, ok := s.requireAdmin(w, r)
			return user.UserID, ok
		},
		queries: func(tx pgx.Tx) receiptSendQueries {
			return legacyInvoiceSendQueries{Queries: dbquery.New(tx), tx: tx}
		},
		sender: s.config.Sender,
	}
	sender.send(w, r)
}

func (s receiptSender) send(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var ok bool
	if requestJSONMediaType(r) {
		body, ok = readBodyWithSyntax(w, r)
	} else {
		body, ok = readItemBody(w, r)
	}
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
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
	rawPayment := chi.URLParam(r, "payment_id")
	paymentID, validPayment := parsePathUUID(rawPayment)
	if !validPayment {
		d := uuidErrorDetail(rawPayment)
		validation = append(validation, validationError{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + d, rawPayment, map[string]string{"error": d}})
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
	var input receiptSendSchema
	for _, field := range []struct {
		name   string
		target **string
	}{{"to_email", &input.ToEmail}, {"cc_email", &input.CCEmail}} {
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
			role, err := q.lockAdminRole(ctx, current)
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
			payment, err := q.lockReceiptPayment(ctx, id, paymentID)
			if errors.Is(err, pgx.ErrNoRows) {
				authError(w, 404, "payment not found on this invoice")
				return false
			}
			if err != nil {
				if invoiceSendSerialization(err) {
					return true
				}
				authError(w, 500, "internal server error")
				return false
			}
			// Every displayed financial value and date must be representable before delivery.
			for _, n := range []pgtype.Numeric{payment.Amount, inv.Amount, inv.BalanceDue} {
				if !n.Valid || n.Int == nil || n.NaN || n.InfinityModifier != pgtype.Finite {
					authError(w, 500, "internal server error")
					return false
				}
				if _, err := recurringDecimal(n); err != nil {
					authError(w, 500, "internal server error")
					return false
				}
			}
			if !payment.PaymentDate.Valid || payment.PaymentDate.InfinityModifier != pgtype.Finite || payment.PaymentDate.Time.Year() < 1 || payment.PaymentDate.Time.Year() > 9999 {
				authError(w, 500, "internal server error")
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
			address, e := mail.ParseAddress(to)
			if e != nil || address.Address == "" || strings.ContainsAny(to, "\r\n") {
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
			ref := fmt.Sprintf("RCT-%X", payment.PaymentID.Bytes[:4])
			subject := "Payment receipt"
			invRef := "your invoice"
			if inv.InvoiceNumber.String != "" {
				subject += " for invoice " + inv.InvoiceNumber.String
				invRef = "invoice " + inv.InvoiceNumber.String
			}
			if strings.ContainsAny(subject, "\r\n") {
				authError(w, 400, "invalid invoice number")
				return false
			}
			greeting := "there"
			if inv.CustomerID.Valid {
				greeting = customer.Name
			}
			balance, _ := recurringDecimal(inv.BalanceDue)
			note := "This invoice is now paid in full."
			if balance.IsPositive() {
				note = "Remaining balance: " + quotationNumber(inv.BalanceDue, inv.Currency) + " " + inv.Currency + "."
			}
			date := "None"
			if payment.PaymentDate.Valid {
				date = payment.PaymentDate.Time.Format("2006-01-02")
			}
			bodyText := fmt.Sprintf("Hi %s,\n\nWe have received your payment of %s %s against %s on %s.\n%s\n\nA PDF receipt is attached for your records.\n\nThank you.", greeting, quotationNumber(payment.Amount, payment.Currency), payment.Currency, invRef, date, note)
			pdf, err := renderReceiptPDF(ctx, payment, inv, customer, business, ref, note)
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			encoded, err := json.Marshal(map[string]string{"sent_to": to})
			if err != nil {
				authError(w, 500, "internal server error")
				return false
			}
			if s.sender == nil {
				authError(w, 500, "email delivery unavailable")
				return false
			}
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = s.sender.Send(sendCtx, email.Message{To: []string{to}, CC: cc, Subject: subject, Text: bodyText, Attachments: []email.Attachment{{Filename: "receipt-" + ref + ".pdf", Content: pdf, ContentType: "application/pdf"}}})
			cancel()
			if err != nil {
				authError(w, 502, "failed to send receipt email")
				return false
			}
			finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
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
func renderReceiptPDF(ctx context.Context, p dbquery.Payment, inv dbquery.Invoice, c dbquery.Customer, b dbquery.BusinessProfile, ref, note string) ([]byte, error) {
	if err := loadQuotationFont(); err != nil {
		return nil, err
	}
	fonts, err := fontrepository.New().AddUTF8FontFromBytes("Noto", fontstyle.Normal, quotationFont).Load()
	if err != nil {
		return nil, err
	}
	m := maroto.New(config.NewBuilder().WithCustomFonts(fonts).WithDefaultFont(&props.Font{Family: "Noto", Size: 10}).WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).WithBottomMargin(15).Build())
	add := func(s string) {
		for _, part := range quotationWrap(s, 180, 10) {
			m.AddRows(row.New(7).Add(text.NewCol(12, part, props.Text{Size: 10, Bottom: 2})))
		}
	}
	logo, logoType := quotationLogo(ctx, quotationText(b.LogoUrl))
	m.AddRows(documentMasthead(logo, logoType, "RECEIPT", ref, props.Text{Size: 18, Bottom: 4})...)
	add("Status: Received")
	add("Receipt No.  " + ref + "    Payment Date  " + quotationDate(p.PaymentDate) + "    Invoice No.  " + quotationText(inv.InvoiceNumber))
	name := quotationText(b.Name)
	if name == "" {
		name = "—"
	}
	add("Received By: " + name)
	add(quotationText(b.Address))
	add(strings.TrimSpace(quotationText(b.ContactEmail) + "  " + quotationText(b.ContactPhone)))
	name = c.Name
	if name == "" {
		name = p.PayerName
	}
	if name == "" {
		name = "—"
	}
	add("Received From: " + name)
	address := []string{quotationText(c.BillingAddress1), quotationText(c.BillingAddress2), quotationText(c.BillingState), quotationText(c.BillingPostalCode), quotationText(c.BillingCity), quotationText(c.BillingCountry)}
	hasAddress := false
	for _, v := range address {
		if v != "" {
			hasAddress = true
			add(v)
		}
	}
	if !hasAddress {
		add(quotationText(c.BillingAddress))
	}
	add(quotationText(c.ContactEmail))
	add(quotationText(c.ContactPhone))

	desc := "Payment received"
	if inv.InvoiceNumber.String != "" {
		desc = "Payment for invoice " + inv.InvoiceNumber.String
	}
	add(desc)
	method := strings.Title(strings.ToLower(strings.ReplaceAll(p.IntakeSource, "_", " ")))
	if p.IntakeSource == "MANUAL" {
		method = "Manual entry"
	}
	if p.PayerReference.String != "" {
		method += " · Ref: " + p.PayerReference.String
	}
	add(method)
	add("Amount Received  " + quotationNumber(p.Amount, p.Currency) + " " + p.Currency)
	add("Invoice total  " + quotationNumber(inv.Amount, inv.Currency) + " " + inv.Currency)
	add("Remaining balance  " + quotationNumber(inv.BalanceDue, inv.Currency) + " " + inv.Currency)
	add(note)
	add("Thank you for your payment.")
	doc, err := m.Generate()
	if err != nil {
		return nil, err
	}
	return doc.GetBytes(), nil
}
