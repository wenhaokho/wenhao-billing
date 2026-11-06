package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type invoiceListInput struct {
	Status      []string `query:"status"`
	CustomerID  string   `query:"customer_id" format:"uuid"`
	InvoiceType string   `query:"invoice_type"`
	Limit       int64    `query:"limit" default:"200" maximum:"500"`
}
type templateGetInput struct {
	ID string `path:"template_id" format:"uuid"`
}
type invoiceGetInput struct {
	ID string `path:"invoice_id" format:"uuid"`
}

func registerInvoiceReads(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"401": "Not authenticated", "403": "Admin required", "404": "Invoice not found", "422": "Validation error", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	responses["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
	registerRecurringRows(api, s, responses)
	op := func(path, id string) huma.Operation {
		return huma.Operation{Method: "GET", Path: path, OperationID: id, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceRead)}, Responses: responses}
	}
	huma.Register(api, op("/api/v1/invoices", "list-invoices"), func(context.Context, *invoiceListInput) (*struct {
		Body []invoiceOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, op("/api/v1/invoices/awaiting-finalization", "awaiting-finalization"), func(context.Context, *struct{}) (*struct {
		Body []invoiceOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, op("/api/v1/invoices/recurring-templates", "list-recurring-templates"), func(context.Context, *struct{}) (*struct {
		Body []invoiceOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, op("/api/v1/invoices/recurring-templates/{template_id}", "get-recurring-template"), func(context.Context, *templateGetInput) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	huma.Register(api, op("/api/v1/invoices/{invoice_id}", "get-invoice"), func(context.Context, *invoiceGetInput) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
}
func (s sessionHandler) invoiceRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	ctx := r.Context()
	raw := chi.URLParam(r, "invoice_id")
	template := strings.Contains(r.URL.Path, "/recurring-templates")
	pathName := "invoice_id"
	if template {
		raw = chi.URLParam(r, "template_id")
		pathName = "template_id"
	}
	var rows []dbquery.Invoice
	var err error
	if raw != "" {
		var id pgtype.UUID
		var ok bool
		id, ok = parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", pathName}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		var row dbquery.Invoice
		row, err = q.GetInvoice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && template && !row.IsTemplate) {
			message := "invoice not found"
			if template {
				message = "recurring template not found"
			}
			authError(w, 404, message)
			return
		}
		rows = []dbquery.Invoice{row}
	} else if template {
		rows, err = q.ListRecurringTemplates(ctx)
	} else if strings.HasSuffix(r.URL.Path, "/awaiting-finalization") {
		rows, err = q.AwaitingFinalization(ctx)
	} else {
		values := r.URL.Query()
		var errs []validationError
		var id pgtype.UUID
		if v, ok := values["customer_id"]; ok {
			v0 := v[len(v)-1]
			var valid bool
			id, valid = parsePathUUID(v0)
			if !valid {
				d := uuidErrorDetail(v0)
				errs = append(errs, validationError{"uuid_parsing", []any{"query", "customer_id"}, "Input should be a valid UUID, " + d, v0, map[string]string{"error": d}})
			}
		}
		limit := int64(200)
		if v, ok := values["limit"]; ok {
			v0 := v[len(v)-1]
			normalized := strings.TrimSpace(v0)
			if !fxPathInteger.MatchString(normalized) {
				errs = append(errs, validationError{"int_parsing", []any{"query", "limit"}, "Input should be a valid integer, unable to parse string as an integer", v0, nil})
			} else {
				normalized = strings.ReplaceAll(normalized, "_", "")
				if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
					normalized = normalized[:dot]
				}
				n, e := strconv.ParseInt(normalized, 10, 64)
				if e != nil {
					if strings.HasPrefix(normalized, "-") {
						n = -1
					} else {
						n = 501
					}
				}
				if n > 500 {
					errs = append(errs, validationError{"less_than_equal", []any{"query", "limit"}, "Input should be less than or equal to 500", v0, map[string]int{"le": 500}})
				} else {
					limit = n
				}
			}
		}
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
		if limit < 0 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
			return
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var typ pgtype.Text
		if v, ok := values["invoice_type"]; ok && v[len(v)-1] != "" {
			typ = pgtype.Text{String: v[len(v)-1], Valid: true}
		}
		rows, err = q.ListInvoices(ctx, dbquery.ListInvoicesParams{AllStatus: !ok, Statuses: statuses, CustomerID: id, InvoiceType: typ, RowLimit: limit})
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.InvoiceID)
	}
	lines, err := q.InvoiceLinesByIDs(ctx, ids)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	byID := map[pgtype.UUID][]dbquery.InvoiceLineItem{}
	for _, line := range lines {
		byID[line.InvoiceID] = append(byID[line.InvoiceID], line)
	}
	out := make([]invoiceOutput, 0, len(rows))
	for _, row := range rows {
		v, e := invoiceJSON(row, byID[row.InvoiceID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if raw != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
