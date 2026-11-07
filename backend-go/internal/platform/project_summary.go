package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type projectSummaryOutput struct {
	ProjectID         pgtype.UUID `json:"project_id"`
	InvoicedTotal     string      `json:"invoiced_total"`
	PaidTotal         string      `json:"paid_total"`
	OutstandingTotal  string      `json:"outstanding_total"`
	ContractValue     *string     `json:"contract_value"`
	ContractRemaining *string     `json:"contract_remaining"`
	InvoiceCount      int64       `json:"invoice_count"`
	BillTotal         string      `json:"bill_total"`
}
type projectSummarySchema struct {
	ProjectID         string  `json:"project_id" format:"uuid"`
	InvoicedTotal     string  `json:"invoiced_total"`
	PaidTotal         string  `json:"paid_total"`
	OutstandingTotal  string  `json:"outstanding_total"`
	ContractValue     *string `json:"contract_value"`
	ContractRemaining *string `json:"contract_remaining"`
	InvoiceCount      int64   `json:"invoice_count"`
	BillTotal         string  `json:"bill_total"`
}

func registerProjectSummary(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"401": "Not authenticated", "403": "Admin required", "404": "Project not found", "422": "Invalid project ID", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	op := func(path, id string) huma.Operation {
		return huma.Operation{Method: "GET", Path: "/api/v1/projects/{project_id}/" + path, OperationID: id, Security: []map[string][]string{{"sessionCookie": {}}}, Responses: responses, Middlewares: huma.Middlewares{sessionMiddleware(s.projectSummary)}}
	}
	huma.Register(api, op("summary", "project-summary"), func(context.Context, *projectGetInput) (*struct{ Body projectSummarySchema }, error) {
		panic("middleware")
	})
	huma.Register(api, op("invoices", "project-invoices"), func(context.Context, *projectGetInput) (*struct {
		Body []invoiceOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}
func projectNumeric(n pgtype.Numeric) (decimal.Decimal, int32, error) {
	if !n.Valid || n.Int == nil || n.NaN || n.InfinityModifier != pgtype.Finite {
		return decimal.Zero, 0, errors.New("invalid numeric")
	}
	scale := -n.Exp
	if scale < 0 {
		scale = 0
	}
	return decimal.NewFromBigInt(n.Int, n.Exp), scale, nil
}
func (s sessionHandler) projectSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "project_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	q := dbquery.New(s.db)
	ctx := r.Context()
	project, err := q.GetProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "project not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/invoices") {
		rows, e := q.ProjectInvoices(ctx, id)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		lines, e := q.ProjectInvoiceLines(ctx, id)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		writeCustomerInvoices(w, rows, lines)
		return
	}
	stats, err := q.ProjectInvoiceStats(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	bill, err := q.ProjectBillStats(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	writeProjectSummary(w, id, project.ContractValue, stats, bill)
}

func writeProjectSummary(w http.ResponseWriter, id pgtype.UUID, contractValue pgtype.Numeric, stats dbquery.ProjectInvoiceStatsRow, bill pgtype.Numeric) {
	a, as, e := projectNumeric(stats.Invoiced)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	b, bs, e := projectNumeric(stats.Outstanding)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	billval, cs, e := projectNumeric(bill)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	scale := as
	if bs > scale {
		scale = bs
	}
	out := projectSummaryOutput{ProjectID: id, InvoicedTotal: a.StringFixed(as), OutstandingTotal: b.StringFixed(bs), PaidTotal: a.Sub(b).StringFixed(scale), InvoiceCount: stats.InvoiceCount, BillTotal: billval.StringFixed(cs)}
	if contractValue.Valid {
		cv, cvs, e := projectNumeric(contractValue)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		v := cv.StringFixed(cvs)
		if as > cvs {
			cvs = as
		}
		remaining := cv.Sub(a).StringFixed(cvs)
		out.ContractValue = &v
		out.ContractRemaining = &remaining
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
