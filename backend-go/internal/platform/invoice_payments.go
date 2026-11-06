package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

func registerInvoicePayments(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"401": "Not authenticated", "403": "Admin required", "404": "Invoice not found", "422": "Validation error", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/invoices/{invoice_id}/payments", OperationID: "list-invoice-payments", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoicePayments)}, Responses: responses}, func(context.Context, *invoiceGetInput) (*struct {
		Body []paymentSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}

func (s sessionHandler) invoicePayments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	q := dbquery.New(s.db)
	if _, err := q.GetInvoice(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "invoice not found")
		return
	} else if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	rows, err := q.ListInvoicePayments(r.Context(), id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]paymentOutput, 0, len(rows))
	for _, p := range rows {
		v, err := paymentJSON(p)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
