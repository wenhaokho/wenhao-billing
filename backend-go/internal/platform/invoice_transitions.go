package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type voidInvoiceBody struct {
	Reason string `json:"reason"`
}
type voidInvoiceInput struct {
	ID   string `path:"invoice_id" format:"uuid"`
	Body voidInvoiceBody
}

func registerInvoiceTransitions(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid invoice", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	for _, action := range []string{"finalize", "void", "mark-paid"} {
		op := huma.Operation{Method: "POST", Path: "/api/v1/invoices/{invoice_id}/" + action, OperationID: "invoice-" + action, SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceWriter().invoiceTransition)}, Responses: responses}
		if action == "void" {
			huma.Register(api, op, func(context.Context, *voidInvoiceInput) (*struct{ Body invoiceOutputSchema }, error) {
				panic("middleware")
			})
		} else {
			huma.Register(api, op, func(context.Context, *invoiceGetInput) (*struct{ Body invoiceOutputSchema }, error) {
				panic("middleware")
			})
		}
	}
}
func (s invoiceWriter) invoiceTransition(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if action == "void" && len(body) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(body))
		var value any
		err := decoder.Decode(&value)
		if err == io.EOF {
			validationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		extra := false
		if err == nil {
			var trailing any
			err = decoder.Decode(&trailing)
			extra = err != io.EOF
		}
		if extra || (err != nil && err != io.EOF) {
			validationResponse(w, invoiceVoidJSONError(body, err, extra))
			return
		}
	}
	raw := chi.URLParam(r, "invoice_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	if action == "void" {
		var data map[string]json.RawMessage
		if len(body) == 0 {
			validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
			return
		}
		if json.Unmarshal(body, &data) != nil || data == nil {
			validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
			return
		}
		reason, exists := data["reason"]
		if !exists {
			validationResponse(w, []validationError{{"missing", []any{"body", "reason"}, "Field required", nil, nil}})
			return
		}
		var str string
		if json.Unmarshal(reason, &str) != nil || string(reason) == "null" {
			validationResponse(w, []validationError{{"string_type", []any{"body", "reason"}, "Input should be a valid string", nil, nil}})
			return
		}
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
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
	row, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 400, fmt.Sprintf("invoice %s not found", id.String()))
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	val, err := row.BalanceDue.Value()
	if err != nil || val == nil {
		authError(w, 500, "internal server error")
		return
	}
	balance, err := decimal.NewFromString(val.(string))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	status := row.Status
	zero := false
	switch action {
	case "finalize":
		if status != "DRAFT" {
			authError(w, 400, fmt.Sprintf("only DRAFT invoices can be finalized (got %s)", status))
			return
		}
		if row.IsTemplate {
			authError(w, 400, "cannot finalize a recurring template")
			return
		}
		if balance.LessThanOrEqual(decimal.Zero) {
			status = "PAID"
			zero = true
		} else {
			status = "OPEN"
		}
	case "mark-paid":
		if status != "OPEN" && status != "SENT" && status != "PARTIAL" {
			authError(w, 400, fmt.Sprintf("only OPEN, SENT or PARTIAL invoices can be settled this way (got %s)", status))
			return
		}
		if balance.GreaterThan(decimal.Zero) {
			authError(w, 400, fmt.Sprintf("invoice has an outstanding balance of %s — record a payment instead", balance.StringFixed(4)))
			return
		}
		status = "PAID"
		zero = true
	case "void":
		if status == "PAID" {
			authError(w, 400, "cannot void a PAID invoice — reverse payments first")
			return
		}
		status = "VOID"
		zero = true
	}
	// Coordinate changes to open-balance eligibility with restoration. Invoice
	// comes first, then hosting items in UUID order, as in payment recording.
	if err = q.lockInvoiceHostingItems(ctx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if zero {
		row.BalanceDue = invoiceNumeric(decimal.Zero)
	}
	if err = q.TransitionInvoice(ctx, dbquery.TransitionInvoiceParams{InvoiceID: id, Status: status, BalanceDue: row.BalanceDue}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	row.Status = status
	lines, err := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := invoiceJSON(row, lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}

// invoiceVoidJSONError returns decoder locations without reflecting submitted data.
func invoiceVoidJSONError(body []byte, err error, extra bool) []validationError {
	errs := itemUpdateJSONError(body, err, extra)
	if extra {
		return errs
	}
	// encoding/json reports an unfinished string at the end; Python points to its opening quote.
	inString, escaped, start := false, false, 0
	for i, c := range body {
		if escaped {
			escaped = false
			continue
		}
		if inString && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			if !inString {
				start = i
			}
			inString = !inString
		}
	}
	if inString {
		errs[0].Loc = []any{"body", utf8.RuneCount(body[:start])}
		errs[0].Ctx = map[string]string{"error": "Unterminated string starting at"}
	} else if strings.Contains(err.Error(), "after object key:value pair") || strings.Contains(err.Error(), "after array element") {
		errs[0].Ctx = map[string]string{"error": "Expecting ',' delimiter"}
	} else if strings.Contains(err.Error(), "invalid escape sequence") {
		if syntax, ok := err.(*json.SyntaxError); ok && syntax.Offset >= 2 {
			errs[0].Loc = []any{"body", utf8.RuneCount(body[:syntax.Offset-2])}
		}
		errs[0].Ctx = map[string]string{"error": `Invalid \escape`}
	} else if strings.Contains(err.Error(), "in literal ") {
		if syntax, ok := err.(*json.SyntaxError); ok {
			pos := int(syntax.Offset) - 1
			for pos > 0 && (body[pos-1] >= 'a' && body[pos-1] <= 'z') {
				pos--
			}
			errs[0].Loc = []any{"body", utf8.RuneCount(body[:pos])}
		}
	} else if strings.Contains(err.Error(), "after object key") {
		errs[0].Ctx = map[string]string{"error": "Expecting ':' delimiter"}
	}
	return errs
}
