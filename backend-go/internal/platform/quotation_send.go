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
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

type quotationSendSchema struct {
	ToEmail *string `json:"to_email,omitempty"`
	CCEmail *string `json:"cc_email,omitempty"`
	Subject *string `json:"subject,omitempty"`
	Message *string `json:"message,omitempty"`
}

func registerQuotationSend(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid quotation or recipient", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Quotation not found", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Delivery failed", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/quotations/{quotation_id}/send", OperationID: "send-quotation", Security: []map[string][]string{{"sessionCookie": {}}}, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationSend)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"quotation_id" format:"uuid"`
		Body quotationSendSchema
	}) (*struct{ Body quotationSchema }, error) {
		panic("middleware")
	})
}
func (s sessionHandler) quotationSend(w http.ResponseWriter, r *http.Request) {
	s.quotationWriter().send(s.config.Sender, w, r)
}

func (s quotationWriter) send(sender email.Sender, w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "quotation_id")
	id, valid := parsePathUUID(raw)
	var validation []validationError
	if !valid {
		d := uuidErrorDetail(raw)
		validation = append(validation, validationError{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
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
	var input quotationSendSchema
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
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	q := s.queries(tx)
	role, err := q.lockAdminRole(ctx, current)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	quote, err := q.LockQuotation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "quotation not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if quote.Status == "VOID" || quote.Status == "INVOICED" {
		authError(w, 400, fmt.Sprintf("cannot send a %s quotation", quote.Status))
		return
	}
	for _, date := range []pgtype.Date{quote.IssueDate, quote.ValidUntil} {
		if date.Valid && (date.InfinityModifier != pgtype.Finite || date.Time.Year() < 1 || date.Time.Year() > 9999) {
			authError(w, 500, "internal server error")
			return
		}
	}
	details, err := q.sendDetails(ctx, quote.CustomerID)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	customer := details.customer
	to := customer.ContactEmail.String
	if input.ToEmail != nil && *input.ToEmail != "" {
		to = *input.ToEmail
	}
	if to == "" {
		authError(w, 400, "no recipient: provide to_email or set a contact_email on the customer")
		return
	}
	validAddress := func(s string) bool {
		a, e := mail.ParseAddress(s)
		return e == nil && a.Address != "" && !strings.ContainsAny(s, "\r\n")
	}
	if !validAddress(to) {
		authError(w, 400, "invalid recipient email")
		return
	}
	var cc []string
	seen := map[string]bool{}
	if input.CCEmail != nil {
		for _, v := range strings.FieldsFunc(*input.CCEmail, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
			key := strings.ToLower(v)
			if !seen[key] {
				cc = append(cc, v)
				seen[key] = true
			}
		}
	}
	subject := "Quotation"
	if quote.QuotationNumber.String != "" {
		subject += " " + quote.QuotationNumber.String
	}
	if input.Subject != nil && *input.Subject != "" {
		subject = *input.Subject
	}
	if strings.ContainsAny(subject, "\r\n") {
		authError(w, 400, "invalid subject")
		return
	}
	amount, err := recurringDecimal(quote.Amount)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	greeting := customer.Name
	if greeting == "" {
		greeting = "there"
	}
	text := fmt.Sprintf("Hi %s,\n\nPlease find attached our quotation %s for %s %s.\n", greeting, quote.QuotationNumber.String, amount.StringFixed(4), quote.Currency)
	if quote.ValidUntil.Valid {
		text += fmt.Sprintf("Valid until: %s.\n", quote.ValidUntil.Time.Format("2006-01-02"))
	}
	text += "\nThank you."
	if input.Message != nil && *input.Message != "" {
		text = *input.Message
	}
	lines, err := q.QuotationLines(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	preflight, err := quotationJSON(quote, lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if _, err = json.Marshal(preflight); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	pdf, err := renderQuotationPDF(ctx, quote, customer, details.profile, lines, details.accounts)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	name := quote.QuotationNumber.String
	if name == "" {
		name = fmt.Sprintf("%x", id.Bytes[:4])
	}
	if strings.ContainsAny(name, "\r\n") {
		authError(w, 400, "invalid quotation number")
		return
	}
	if sender == nil {
		authError(w, 500, "email delivery unavailable")
		return
	}
	if ctx.Err() != nil {
		authError(w, 500, "email delivery failed")
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = sender.Send(sendCtx, email.Message{To: []string{to}, CC: cc, Subject: subject, Text: text, Attachments: []email.Attachment{{Filename: "quotation-" + name + ".pdf", Content: pdf, ContentType: "application/pdf"}}})
	cancel()
	if err != nil {
		authError(w, 500, "email delivery failed")
		return
	}
	// Once accepted, finish recording the send even if the HTTP client leaves.
	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finalCancel()
	if err = q.recordQuotationSend(finalCtx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	quote, err = q.GetQuotation(finalCtx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := quotationJSON(quote, lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(finalCtx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}

func (q legacyQuotationWriteQueries) recordQuotationSend(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `UPDATE quotations SET status=CASE WHEN status='DRAFT' THEN 'SENT' ELSE status END,last_sent_at=(clock_timestamp() AT TIME ZONE 'UTC') WHERE quotation_id=$1`, id)
	return err
}

type quotationSendDetails struct {
	customer dbquery.Customer
	profile  dbquery.BusinessProfile
	accounts []dbquery.PaymentAccount
}

func (q legacyQuotationWriteQueries) sendDetails(ctx context.Context, id pgtype.UUID) (quotationSendDetails, error) {
	customer, err := q.GetCustomer(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return quotationSendDetails{}, err
	}
	profile, err := q.GetBusinessProfile(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return quotationSendDetails{}, err
	}
	accounts, err := q.ListBusinessPaymentAccounts(ctx)
	return quotationSendDetails{customer: customer, profile: profile, accounts: accounts}, err
}
