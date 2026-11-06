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

type quotationLineOutput struct {
	LineItemID  pgtype.UUID `json:"line_item_id"`
	ItemID      pgtype.UUID `json:"item_id"`
	Description string      `json:"description"`
	Quantity    string      `json:"quantity"`
	UnitPrice   string      `json:"unit_price"`
	Amount      string      `json:"amount"`
	Position    int32       `json:"position"`
}
type quotationOutput struct {
	QuotationID        pgtype.UUID           `json:"quotation_id"`
	CustomerID         pgtype.UUID           `json:"customer_id"`
	ProjectID          pgtype.UUID           `json:"project_id"`
	QuotationNumber    pgtype.Text           `json:"quotation_number"`
	PoSoNumber         pgtype.Text           `json:"po_so_number"`
	Currency           string                `json:"currency"`
	Subtotal           *string               `json:"subtotal"`
	DiscountType       pgtype.Text           `json:"discount_type"`
	DiscountValue      *string               `json:"discount_value"`
	Amount             string                `json:"amount"`
	Status             string                `json:"status"`
	IssueDate          pgtype.Date           `json:"issue_date"`
	ValidUntil         pgtype.Date           `json:"valid_until"`
	PaymentTerms       pgtype.Text           `json:"payment_terms"`
	Notes              pgtype.Text           `json:"notes"`
	Footer             pgtype.Text           `json:"footer"`
	LastSentAt         *pythonTimestamp      `json:"last_sent_at"`
	AcceptedAt         *pythonTimestamp      `json:"accepted_at"`
	AcceptedBy         pgtype.Text           `json:"accepted_by"`
	ConvertedInvoiceID pgtype.UUID           `json:"converted_invoice_id"`
	CreatedAt          pythonTimestamp       `json:"created_at"`
	LineItems          []quotationLineOutput `json:"line_items"`
}
type quotationLineSchema struct {
	LineItemID  string  `json:"line_item_id" format:"uuid"`
	ItemID      *string `json:"item_id" format:"uuid"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unit_price"`
	Amount      string  `json:"amount"`
	Position    int32   `json:"position"`
}
type quotationSchema struct {
	QuotationID        string                `json:"quotation_id" format:"uuid"`
	CustomerID         string                `json:"customer_id" format:"uuid"`
	ProjectID          *string               `json:"project_id" format:"uuid"`
	QuotationNumber    *string               `json:"quotation_number"`
	PoSoNumber         *string               `json:"po_so_number"`
	Currency           string                `json:"currency"`
	Subtotal           *string               `json:"subtotal"`
	DiscountType       *string               `json:"discount_type"`
	DiscountValue      *string               `json:"discount_value"`
	Amount             string                `json:"amount"`
	Status             string                `json:"status"`
	IssueDate          *string               `json:"issue_date" format:"date"`
	ValidUntil         *string               `json:"valid_until" format:"date"`
	PaymentTerms       *string               `json:"payment_terms"`
	Notes              *string               `json:"notes"`
	Footer             *string               `json:"footer"`
	LastSentAt         *string               `json:"last_sent_at" format:"date-time"`
	AcceptedAt         *string               `json:"accepted_at" format:"date-time"`
	AcceptedBy         *string               `json:"accepted_by"`
	ConvertedInvoiceID *string               `json:"converted_invoice_id" format:"uuid"`
	CreatedAt          string                `json:"created_at" format:"date-time"`
	LineItems          []quotationLineSchema `json:"line_items" nullable:"false"`
}

func quotationJSON(b dbquery.Quotation, lines []dbquery.QuotationLineItem) (quotationOutput, error) {
	subtotal, e := billNumeric(b.Subtotal, true)
	if e != nil {
		return quotationOutput{}, e
	}
	discount, e := billNumeric(b.DiscountValue, true)
	if e != nil {
		return quotationOutput{}, e
	}
	amount, e := billNumeric(b.Amount, false)
	if e != nil {
		return quotationOutput{}, e
	}
	var sent, accepted *pythonTimestamp
	if b.LastSentAt.Valid {
		v := pythonTimestamp(b.LastSentAt)
		sent = &v
	}
	if b.AcceptedAt.Valid {
		v := pythonTimestamp(b.AcceptedAt)
		accepted = &v
	}
	out := quotationOutput{b.QuotationID, b.CustomerID, b.ProjectID, b.QuotationNumber, b.PoSoNumber, b.Currency, subtotal, b.DiscountType, discount, *amount, b.Status, b.IssueDate, b.ValidUntil, b.PaymentTerms, b.Notes, b.Footer, sent, accepted, b.AcceptedBy, b.ConvertedInvoiceID, pythonTimestamp(b.CreatedAt), make([]quotationLineOutput, 0, len(lines))}
	for _, l := range lines {
		q, e := billNumeric(l.Quantity, false)
		if e != nil {
			return quotationOutput{}, e
		}
		p, e := billNumeric(l.UnitPrice, false)
		if e != nil {
			return quotationOutput{}, e
		}
		a, e := billNumeric(l.Amount, false)
		if e != nil {
			return quotationOutput{}, e
		}
		out.LineItems = append(out.LineItems, quotationLineOutput{l.LineItemID, l.ItemID, l.Description, *q, *p, *a, l.Position})
	}
	return out, nil
}

type quotationListInput struct {
	CustomerID string   `query:"customer_id" format:"uuid"`
	ProjectID  string   `query:"project_id" format:"uuid"`
	Limit      int64    `query:"limit" default:"200" maximum:"500"`
	Status     []string `query:"status"`
}
type quotationGetInput struct {
	ID string `path:"quotation_id" format:"uuid"`
}

func registerQuotationReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.quotationRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		r["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
		if detail {
			r["404"] = response("Quotation not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/quotations", OperationID: "list-quotations", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *quotationListInput) (*struct {
		Body []quotationSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/quotations/{quotation_id}", OperationID: "get-quotation", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *quotationGetInput) (*struct{ Body quotationSchema }, error) {
		panic("middleware")
	})
}
func (s sessionHandler) quotationRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "quotation_id")
	var rows []dbquery.Quotation
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		b, e := q.GetQuotation(r.Context(), id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "quotation not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbquery.Quotation{b}
	} else {
		values := r.URL.Query()
		var errs []validationError
		var customer, project pgtype.UUID
		for _, field := range []struct {
			name string
			dst  *pgtype.UUID
		}{{"customer_id", &customer}, {"project_id", &project}} {
			if v, ok := values[field.name]; ok {
				raw = v[len(v)-1]
				id, valid := parsePathUUID(raw)
				if !valid {
					d := uuidErrorDetail(raw)
					errs = append(errs, validationError{"uuid_parsing", []any{"query", field.name}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
				} else {
					*field.dst = id
				}
			}
		}
		limit := int64(200)
		if v, ok := values["limit"]; ok {
			raw = v[len(v)-1]
			n := strings.TrimSpace(raw)
			if !fxPathInteger.MatchString(n) {
				errs = append(errs, validationError{"int_parsing", []any{"query", "limit"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil})
			} else {
				n = strings.ReplaceAll(n, "_", "")
				if i := strings.IndexByte(n, '.'); i >= 0 {
					n = n[:i]
				}
				x, e := strconv.ParseInt(n, 10, 64)
				if e != nil {
					if strings.HasPrefix(n, "-") {
						x = -1
					} else {
						x = 501
					}
				}
				if x > 500 {
					errs = append(errs, validationError{"less_than_equal", []any{"query", "limit"}, "Input should be less than or equal to 500", raw, map[string]int{"le": 500}})
				} else {
					limit = x
				}
			}
		}
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
		if limit < 0 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var e error
		rows, e = q.ListQuotations(r.Context(), dbquery.ListQuotationsParams{CustomerID: customer, ProjectID: project, AllStatus: !ok, Statuses: statuses, RowLimit: int32(limit)})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.QuotationID)
	}
	lines, e := q.QuotationLines(r.Context(), ids)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	grouped := map[pgtype.UUID][]dbquery.QuotationLineItem{}
	for _, l := range lines {
		grouped[l.QuotationID] = append(grouped[l.QuotationID], l)
	}
	out := make([]quotationOutput, 0, len(rows))
	for _, b := range rows {
		v, e := quotationJSON(b, grouped[b.QuotationID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "quotation_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
